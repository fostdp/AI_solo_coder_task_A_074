package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"lng-monitor/internal/alarm_forwarder"
	"lng-monitor/internal/api"
	"lng-monitor/internal/channels"
	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/modbus_poller"
	"lng-monitor/internal/models"
	"lng-monitor/internal/rollover_predictor"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== LNG储罐翻滚预测与安全监控系统 启动 (channel架构) ===")

	cfg := config.Load()

	modelParamsPath := os.Getenv("MODEL_PARAMS_PATH")
	if modelParamsPath == "" {
		exePath, _ := os.Executable()
		modelParamsPath = exePath + "/../configs/model_params.json"
	}

	params, err := config.LoadModelParams(modelParamsPath)
	if err != nil {
		log.Printf("加载模型参数JSON失败(%v)，使用默认值", err)
		params = config.DefaultModelParams()
	}
	log.Printf("[✓] 模型参数已加载 (layers=%d tank_height=%.0f)", params.FVM.NumLayers, params.FVM.TankHeight)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.New(ctx, cfg)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer db.Close()
	log.Println("[✓] 数据库已连接")

	sensorBatchCh := channels.NewSensorBatchChan()
	predictionCh := channels.NewPredictionChan()
	alarmEventCh := channels.NewAlarmEventChan()

	poller := modbus_poller.NewPoller(cfg, &params.Poller, db, sensorBatchCh)
	if err := poller.Start(ctx); err != nil {
		log.Fatalf("Modbus采集器启动失败: %v", err)
	}
	log.Println("[✓] modbus_poller 已启动 → sensorBatchCh")

	predictor := rollover_predictor.NewPredictor(&params.FVM, &params.Prediction, db, sensorBatchCh, predictionCh)
	predictor.Start(ctx)
	log.Println("[✓] rollover_predictor 已启动 (sensorBatchCh → predictionCh)")

	forwarder := alarm_forwarder.NewForwarder(&params.Alarm, &params.OPCUA, db, sensorBatchCh, predictionCh, alarmEventCh)
	forwarder.Start(ctx)
	log.Println("[✓] alarm_forwarder 已启动 (sensorBatchCh + predictionCh → alarmEventCh)")

	apiServer := api.NewServer(cfg, db)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-alarmEventCh:
				apiServer.BroadcastMessage(models.WSMessage{
					Type:    "alarm",
					Payload: event.Alarm,
				})
			case batch := <-sensorBatchCh:
				_ = batch
				apiServer.BroadcastMessage(models.WSMessage{
					Type:    "data_update",
					Payload: "new data available",
				})
			}
		}
	}()

	go func() {
		if err := apiServer.Start(ctx); err != nil {
			log.Printf("API服务异常: %v", err)
		}
	}()
	log.Println("[✓] API服务已启动")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("=== 系统关闭中 ===")
	cancel()
	forwarder.Close()
}
