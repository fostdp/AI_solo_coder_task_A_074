package alarm

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"
)

type AlarmCallback func(alarm models.Alarm)

type Service struct {
	cfg      *config.Config
	db       *database.DB
	mu       sync.Mutex
	callback AlarmCallback

	cooldown map[string]time.Time
}

func NewService(cfg *config.Config, db *database.DB, callback AlarmCallback) *Service {
	return &Service{
		cfg:      cfg,
		db:       db,
		callback: callback,
		cooldown: make(map[string]time.Time),
	}
}

func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.evaluate(ctx)
			}
		}
	}()
	log.Println("alarm service started")
}

func (s *Service) evaluate(ctx context.Context) {
	tanks, err := s.db.GetTanks(ctx)
	if err != nil {
		log.Printf("alarm: get tanks error: %v", err)
		return
	}

	for _, tank := range tanks {
		s.evaluateTank(ctx, tank)
	}
}

func (s *Service) evaluateTank(ctx context.Context, tank models.Tank) {
	temps, err := s.db.GetLatestTemperatures(ctx, tank.ID)
	if err != nil || len(temps) == 0 {
		return
	}

	densities, _ := s.db.GetLatestDensities(ctx, tank.ID)

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

	if maxTempDiff > s.cfg.AlarmTempDiffThreshold && maxDensDiff > s.cfg.AlarmDensityDiffThreshold {
		key := fmt.Sprintf("level1_rollover_%d", tank.ID)
		if !s.inCooldown(key) {
			a := models.Alarm{
				Time:       time.Now().UTC(),
				TankID:     tank.ID,
				AlarmLevel: 1,
				AlarmType:  "rollover_warning",
				Message: fmt.Sprintf("一级翻滚预警: %s 层间温差%.1f℃(阈值%.1f℃) 密度差%.1fkg/m³(阈值%.1fkg/m³) 建议开启低压泵循环混合",
					tank.TankCode, maxTempDiff, s.cfg.AlarmTempDiffThreshold,
					maxDensDiff, s.cfg.AlarmDensityDiffThreshold),
			}
			s.fireAlarm(ctx, key, a)
		}
	}

	pressure, err := s.db.GetLatestPressure(ctx, tank.ID)
	if err == nil {
		threshold := tank.DesignPressure * s.cfg.AlarmPressureThresholdPct
		if pressure > threshold {
			key := fmt.Sprintf("level2_overpressure_%d", tank.ID)
			if !s.inCooldown(key) {
				a := models.Alarm{
					Time:       time.Now().UTC(),
					TankID:     tank.ID,
					AlarmLevel: 2,
					AlarmType:  "overpressure",
					Message: fmt.Sprintf("二级超压告警: %s 罐压%.1fkPa 超过设计压力%.0f%%(阈值%.1fkPa) BOG压缩机自动调节已启动",
						tank.TankCode, pressure, s.cfg.AlarmPressureThresholdPct*100, threshold),
				}
				s.fireAlarm(ctx, key, a)
			}
		}
	}
}

func (s *Service) fireAlarm(ctx context.Context, cooldownKey string, a models.Alarm) {
	s.mu.Lock()
	s.cooldown[cooldownKey] = time.Now().Add(5 * time.Minute)
	s.mu.Unlock()

	if err := s.db.InsertAlarm(ctx, a); err != nil {
		log.Printf("alarm: insert error: %v", err)
	}

	log.Printf("ALARM [Level %d] %s", a.AlarmLevel, a.Message)

	if s.callback != nil {
		s.callback(a)
	}
}

func (s *Service) inCooldown(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.cooldown[key]; ok {
		return time.Now().Before(t)
	}
	return false
}
