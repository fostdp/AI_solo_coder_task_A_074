package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"lng-monitor/internal/alarm"
	"lng-monitor/internal/api"
	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/modbus"
	"lng-monitor/internal/models"
	"lng-monitor/internal/opcua"
	"lng-monitor/internal/prediction"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== LNG储罐翻滚预测与安全监控系统 启动 ===")

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.New(ctx, cfg)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer db.Close()
	log.Println("[✓] 数据库已连接")

	opcuaClient := opcua.NewClient(cfg)
	if err := opcuaClient.Connect(ctx); err != nil {
		log.Printf("[!] OPC UA连接失败(将使用模拟模式): %v", err)
	} else {
		log.Println("[✓] OPC UA已连接")
	}
	defer opcuaClient.Close()

	apiServer := api.NewServer(cfg, db)

	alarmSvc := alarm.NewService(cfg, db, func(a models.Alarm) {
		_ = opcuaClient.PushAlarm(ctx, a)

		if a.AlarmLevel == 2 {
			_ = opcuaClient.ActivateBOGCompressor(ctx, a.TankID)
			_ = opcuaClient.SetBOGCompressorSpeed(ctx, a.TankID, 3600)
		}

		apiServer.BroadcastMessage(models.WSMessage{
			Type:    "alarm",
			Payload: a,
		})
	})

	fvmModel := prediction.NewFiniteVolumeModel(cfg, db)

	ingester := modbus.NewIngester(cfg, db, func(
		temps []models.TemperatureReading,
		dens []models.DensityReading,
		pres []models.PressureReading,
		bog []models.BOGCompressorStatus,
	) {
		apiServer.BroadcastMessage(models.WSMessage{
			Type:    "data_update",
			Payload: "new data available",
		})
	})

	if err := ingester.Start(ctx); err != nil {
		log.Fatalf("Modbus采集器启动失败: %v", err)
	}
	log.Println("[✓] Modbus TCP采集器已启动")

	fvmModel.Start(ctx)
	log.Println("[✓] 翻滚预测模型已启动")

	alarmSvc.Start(ctx)
	log.Println("[✓] 告警服务已启动")

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
}
