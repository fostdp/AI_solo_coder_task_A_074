package alarm_forwarder

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"lng-monitor/internal/channels"
	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"
)

type Forwarder struct {
	alarmCfg *config.AlarmParams
	opcuaCfg *config.OPCUAParams
	db       *database.DB
	sensorIn <-chan channels.SensorBatch
	predIn   <-chan channels.PredictionOutput
	alarmOut chan<- channels.AlarmEvent

	connected    atomic.Bool
	mu           sync.Mutex
	cooldown     map[string]time.Time
	pendingAlarms []pendingAlarm
	pendingMu    sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	lastHeartbeat     time.Time
	heartbeatFailures int
}

type pendingAlarm struct {
	alarm models.Alarm
	time  time.Time
}

func NewForwarder(
	alarmCfg *config.AlarmParams,
	opcuaCfg *config.OPCUAParams,
	db *database.DB,
	sensorIn <-chan channels.SensorBatch,
	predIn <-chan channels.PredictionOutput,
	alarmOut chan<- channels.AlarmEvent,
) *Forwarder {
	return &Forwarder{
		alarmCfg:    alarmCfg,
		opcuaCfg:    opcuaCfg,
		db:          db,
		sensorIn:    sensorIn,
		predIn:      predIn,
		alarmOut:    alarmOut,
		cooldown:    make(map[string]time.Time),
	}
}

func (f *Forwarder) Start(ctx context.Context) {
	f.ctx, f.cancel = context.WithCancel(ctx)

	f.connected.Store(true)
	f.lastHeartbeat = time.Now()

	go f.consumeSensorBatch()
	go f.consumePrediction()
	go f.reconnectLoop()
	go f.heartbeatLoop()
	go f.periodicEval()

	log.Printf("alarm_forwarder started (temp_thresh=%.1f dens_thresh=%.1f pressure_pct=%.2f)",
		f.alarmCfg.TempDiffThreshold, f.alarmCfg.DensityDiffThreshold, f.alarmCfg.PressureThresholdPct)
}

func (f *Forwarder) consumeSensorBatch() {
	for {
		select {
		case <-f.ctx.Done():
			return
		case batch := <-f.sensorIn:
			if len(batch.Pressures) > 0 {
				f.evalPressure(batch)
			}
		}
	}
}

func (f *Forwarder) consumePrediction() {
	for {
		select {
		case <-f.ctx.Done():
			return
		case pred := <-f.predIn:
			f.evalRolloverRisk(pred)
		}
	}
}

func (f *Forwarder) periodicEval() {
	ticker := time.NewTicker(time.Duration(f.alarmCfg.EvalIntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			f.fullEval()
		}
	}
}

func (f *Forwarder) evalPressure(batch channels.SensorBatch) {
	for _, p := range batch.Pressures {
		tanks, _ := f.db.GetTanks(f.ctx)
		for _, tank := range tanks {
			if tank.ID != p.TankID {
				continue
			}
			threshold := tank.DesignPressure * f.alarmCfg.PressureThresholdPct
			if p.ValueKpa > threshold {
				key := fmt.Sprintf("level2_overpressure_%d", tank.ID)
				if !f.inCooldown(key) {
					a := models.Alarm{
						Time:       time.Now().UTC(),
						TankID:     tank.ID,
						AlarmLevel: 2,
						AlarmType:  "overpressure",
						Message: fmt.Sprintf("二级超压告警: %s 罐压%.1fkPa 超过设计压力%.0f%%(阈值%.1fkPa) BOG压缩机自动调节已启动",
							tank.TankCode, p.ValueKpa, f.alarmCfg.PressureThresholdPct*100, threshold),
					}
					f.fireAlarm(a, nil)

					f.pushOPCUAAlarm(a)
					f.activateBOG(tank.ID)
				}
			}
		}
	}
}

func (f *Forwarder) evalRolloverRisk(pred channels.PredictionOutput) {
	if pred.RiskIndex < 0.3 {
		return
	}

	tanks, _ := f.db.GetTanks(f.ctx)
	for _, tank := range tanks {
		if tank.ID != pred.TankID {
			continue
		}

		if pred.MaxTempGrad > f.alarmCfg.TempDiffThreshold && pred.MaxDensGrad > f.alarmCfg.DensityDiffThreshold {
			key := fmt.Sprintf("level1_rollover_%d", tank.ID)
			if !f.inCooldown(key) {
				a := models.Alarm{
					Time:       time.Now().UTC(),
					TankID:     tank.ID,
					AlarmLevel: 1,
					AlarmType:  "rollover_warning",
					Message: fmt.Sprintf("一级翻滚预警: %s 层间温差%.1f℃(阈值%.1f℃) 密度差%.1fkg/m³(阈值%.1fkg/m³) 风险指数%.1f%% 建议开启低压泵循环混合",
						tank.TankCode, pred.MaxTempGrad, f.alarmCfg.TempDiffThreshold,
						pred.MaxDensGrad, f.alarmCfg.DensityDiffThreshold, pred.RiskIndex*100),
				}
				f.fireAlarm(a, &pred)
				f.pushOPCUAAlarm(a)
			}
		}
	}
}

func (f *Forwarder) fullEval() {
	tanks, err := f.db.GetTanks(f.ctx)
	if err != nil {
		return
	}

	for _, tank := range tanks {
		temps, err := f.db.GetLatestTemperatures(f.ctx, tank.ID)
		if err != nil || len(temps) == 0 {
			continue
		}
		densities, _ := f.db.GetLatestDensities(f.ctx, tank.ID)

		layerTempAvg := make(map[int]float64)
		layerTempCount := make(map[int]int)
		for _, t := range temps {
			layerTempAvg[t.LayerIndex] += t.ValueCelsius
			layerTempCount[t.LayerIndex]++
		}
		for k, v := range layerTempAvg {
			layerTempAvg[k] = v / float64(layerTempCount[k])
		}

		layerDensAvg := make(map[int]float64)
		layerDensCount := make(map[int]int)
		for _, d := range densities {
			layerDensAvg[d.LayerIndex] += d.ValueKgM3
			layerDensCount[d.LayerIndex]++
		}
		for k, v := range layerDensAvg {
			layerDensAvg[k] = v / float64(layerDensCount[k])
		}

		maxTempDiff := 0.0
		maxDensDiff := 0.0
		for i := range layerTempAvg {
			for j := range layerTempAvg {
				if i >= j {
					continue
				}
				td := math.Abs(layerTempAvg[i] - layerTempAvg[j])
				dd := 0.0
				if _, okI := layerDensAvg[i]; okI {
					if _, okJ := layerDensAvg[j]; okJ {
						dd = math.Abs(layerDensAvg[i] - layerDensAvg[j])
					}
				}
				if td > maxTempDiff {
					maxTempDiff = td
				}
				if dd > maxDensDiff {
					maxDensDiff = dd
				}
			}
		}

		if maxTempDiff > f.alarmCfg.TempDiffThreshold && maxDensDiff > f.alarmCfg.DensityDiffThreshold {
			key := fmt.Sprintf("level1_rollover_%d", tank.ID)
			if !f.inCooldown(key) {
				a := models.Alarm{
					Time:       time.Now().UTC(),
					TankID:     tank.ID,
					AlarmLevel: 1,
					AlarmType:  "rollover_warning",
					Message: fmt.Sprintf("一级翻滚预警: %s 层间温差%.1f℃(阈值%.1f℃) 密度差%.1fkg/m³(阈值%.1fkg/m³) 建议开启低压泵循环混合",
						tank.TankCode, maxTempDiff, f.alarmCfg.TempDiffThreshold,
						maxDensDiff, f.alarmCfg.DensityDiffThreshold),
				}
				f.fireAlarm(a, nil)
				f.pushOPCUAAlarm(a)
			}
		}

		pressure, err := f.db.GetLatestPressure(f.ctx, tank.ID)
		if err == nil {
			threshold := tank.DesignPressure * f.alarmCfg.PressureThresholdPct
			if pressure > threshold {
				key := fmt.Sprintf("level2_overpressure_%d", tank.ID)
				if !f.inCooldown(key) {
					a := models.Alarm{
						Time:       time.Now().UTC(),
						TankID:     tank.ID,
						AlarmLevel: 2,
						AlarmType:  "overpressure",
						Message: fmt.Sprintf("二级超压告警: %s 罐压%.1fkPa 超过设计压力%.0f%%(阈值%.1fkPa) BOG压缩机自动调节已启动",
							tank.TankCode, pressure, f.alarmCfg.PressureThresholdPct*100, threshold),
					}
					f.fireAlarm(a, nil)
					f.pushOPCUAAlarm(a)
					f.activateBOG(tank.ID)
				}
			}
		}
	}
}

func (f *Forwarder) fireAlarm(a models.Alarm, pred *channels.PredictionOutput) {
	f.mu.Lock()
	key := fmt.Sprintf("level%d_%s_%d", a.AlarmLevel, a.AlarmType, a.TankID)
	f.cooldown[key] = time.Now().Add(time.Duration(f.alarmCfg.CooldownMinutes) * time.Minute)
	f.mu.Unlock()

	_ = f.db.InsertAlarm(f.ctx, a)

	log.Printf("ALARM [Level %d] %s", a.AlarmLevel, a.Message)

	if f.alarmOut != nil {
		select {
		case f.alarmOut <- channels.AlarmEvent{Alarm: a, Prediction: pred}:
		default:
			log.Printf("alarm_forwarder: alarm output channel full, dropping")
		}
	}
}

func (f *Forwarder) inCooldown(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.cooldown[key]; ok {
		return time.Now().Before(t)
	}
	return false
}

func (f *Forwarder) pushOPCUAAlarm(a models.Alarm) {
	if !f.connected.Load() {
		f.enqueuePending(a)
		return
	}
	nodeID := fmt.Sprintf("ns=2;s=Tank%d.Alarm.Level%d.Type%s", a.TankID, a.AlarmLevel, a.AlarmType)
	log.Printf("OPC UA: push alarm node=%s level=%d tank=%d", nodeID, a.AlarmLevel, a.TankID)
}

func (f *Forwarder) activateBOG(tankID int) {
	if f.connected.Load() {
		nodeID := fmt.Sprintf("ns=2;s=Tank%d.BOG.Command.Start", tankID)
		log.Printf("OPC UA: activate BOG node=%s tank=%d speed=%.0f", nodeID, tankID, f.alarmCfg.BOGCompressorSpeedRPM)

		speedNodeID := fmt.Sprintf("ns=2;s=Tank%d.BOG.Command.Speed", tankID)
		log.Printf("OPC UA: set BOG speed node=%s speed=%.0f", speedNodeID, f.alarmCfg.BOGCompressorSpeedRPM)
	} else {
		log.Printf("OPC UA: BOG command queued (disconnected) tank=%d", tankID)
	}
}

func (f *Forwarder) enqueuePending(a models.Alarm) {
	f.pendingMu.Lock()
	defer f.pendingMu.Unlock()
	if len(f.pendingAlarms) >= f.opcuaCfg.MaxPendingAlarms {
		f.pendingAlarms = f.pendingAlarms[1:]
	}
	f.pendingAlarms = append(f.pendingAlarms, pendingAlarm{alarm: a, time: time.Now()})
}

func (f *Forwarder) reconnectLoop() {
	interval := time.Duration(f.opcuaCfg.ReconnectIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			if !f.connected.Load() {
				log.Printf("OPC UA: attempting reconnection")
				f.connected.Store(true)
				f.heartbeatFailures = 0
				f.lastHeartbeat = time.Now()
				f.flushPending()
			}
		}
	}
}

func (f *Forwarder) heartbeatLoop() {
	interval := time.Duration(f.opcuaCfg.HeartbeatIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			if !f.connected.Load() {
				continue
			}
			f.heartbeatFailures++
			if f.heartbeatFailures >= f.opcuaCfg.HeartbeatMaxFailures {
				log.Println("OPC UA: heartbeat failures exceeded, marking disconnected")
				f.connected.Store(false)
			} else {
				f.lastHeartbeat = time.Now()
				f.heartbeatFailures = 0
			}
		}
	}
}

func (f *Forwarder) flushPending() {
	f.pendingMu.Lock()
	pending := f.pendingAlarms
	f.pendingAlarms = nil
	f.pendingMu.Unlock()

	staleLimit := time.Duration(f.opcuaCfg.StaleAlarmMinutes) * time.Minute
	for _, pa := range pending {
		if time.Since(pa.time) > staleLimit {
			continue
		}
		log.Printf("OPC UA: flushing pending alarm tank=%d level=%d", pa.alarm.TankID, pa.alarm.AlarmLevel)
	}
}

func (f *Forwarder) Close() {
	if f.cancel != nil {
		f.cancel()
	}
	f.connected.Store(false)
}
