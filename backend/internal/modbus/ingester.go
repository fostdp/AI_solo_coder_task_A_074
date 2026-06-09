package modbus

import (
	"container/heap"
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"

	modbuscli "github.com/simonvetter/modbus"
)

type DataCallback func(
	temperatures []models.TemperatureReading,
	densities []models.DensityReading,
	pressures []models.PressureReading,
	bogStatuses []models.BOGCompressorStatus,
)

const (
	PriorityCritical = 0
	PriorityHigh     = 1
	PriorityNormal   = 2
)

type pollTask struct {
	sensor   models.Sensor
	tankID   int
	priority int
}

type pollTaskHeap []pollTask

func (h pollTaskHeap) Len() int           { return len(h) }
func (h pollTaskHeap) Less(i, j int) bool { return h[i].priority < h[j].priority }
func (h pollTaskHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *pollTaskHeap) Push(x interface{}) {
	*h = append(*h, x.(pollTask))
}

func (h *pollTaskHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[0 : n-1]
	return item
}

type Ingester struct {
	cfg      *config.Config
	db       *database.DB
	clients  map[int]*modbuscli.ModbusClient
	sensors  map[int][]models.Sensor
	callback DataCallback

	criticalQueue pollTaskHeap
	highQueue     pollTaskHeap
	normalQueue   pollTaskHeap
	queueMu       sync.Mutex

	lastCriticalPoll time.Time
	lastFullPoll     time.Time
}

func sensorPriority(s models.Sensor) int {
	switch s.SensorType {
	case "pressure", "bog_compressor":
		return PriorityCritical
	case "density":
		return PriorityHigh
	default:
		return PriorityNormal
	}
}

func NewIngester(cfg *config.Config, db *database.DB, callback DataCallback) *Ingester {
	return &Ingester{
		cfg:      cfg,
		db:       db,
		clients:  make(map[int]*modbuscli.ModbusClient),
		sensors:  make(map[int][]models.Sensor),
		callback: callback,
	}
}

func (ing *Ingester) Start(ctx context.Context) error {
	tanks, err := ing.db.GetTanks(ctx)
	if err != nil {
		return fmt.Errorf("load tanks: %w", err)
	}

	criticalQ := &pollTaskHeap{}
	highQ := &pollTaskHeap{}
	normalQ := &pollTaskHeap{}
	heap.Init(criticalQ)
	heap.Init(highQ)
	heap.Init(normalQ)

	for _, tank := range tanks {
		sensors, err := ing.db.GetSensorsByTank(ctx, tank.ID)
		if err != nil {
			return fmt.Errorf("load sensors for tank %d: %w", tank.ID, err)
		}
		ing.sensors[tank.ID] = sensors

		unitIDs := make(map[int]bool)
		for _, s := range sensors {
			unitIDs[s.ModbusUnitID] = true

			task := pollTask{
				sensor:   s,
				tankID:   tank.ID,
				priority: sensorPriority(s),
			}
			switch task.priority {
			case PriorityCritical:
				heap.Push(criticalQ, task)
			case PriorityHigh:
				heap.Push(highQ, task)
			default:
				heap.Push(normalQ, task)
			}
		}

		for unitID := range unitIDs {
			if _, exists := ing.clients[unitID]; !exists {
				client, err := modbuscli.NewClient(&modbuscli.ClientConfiguration{
					URL:     fmt.Sprintf("tcp://%s:%d", ing.cfg.ModbusHost, ing.cfg.ModbusPort),
					Timeout: time.Duration(ing.cfg.ModbusTimeout) * time.Second,
				})
				if err != nil {
					return fmt.Errorf("create modbus client for unit %d: %w", unitID, err)
				}
				ing.clients[unitID] = client
			}
		}
	}

	ing.criticalQueue = *criticalQ
	ing.highQueue = *highQ
	ing.normalQueue = *normalQ

	criticalInterval := 10 * time.Second
	fullInterval := time.Duration(ing.cfg.DataIntervalSec) * time.Second

	criticalTicker := time.NewTicker(criticalInterval)
	fullTicker := time.NewTicker(fullInterval)

	go func() {
		for {
			select {
			case <-ctx.Done():
				criticalTicker.Stop()
				fullTicker.Stop()
				return
			case <-criticalTicker.C:
				ing.pollCritical(ctx)
			case <-fullTicker.C:
				ing.pollFull(ctx)
			}
		}
	}()

	log.Printf("modbus ingester started (critical=%s full=%s)", criticalInterval, fullInterval)
	return nil
}

func (ing *Ingester) pollCritical(ctx context.Context) {
	now := time.Now().UTC()
	ing.lastCriticalPoll = now

	var allPressures []models.PressureReading
	var allBOG []models.BOGCompressorStatus

	for tankID, sensors := range ing.sensors {
		unitSensors := make(map[int][]models.Sensor)
		for _, s := range sensors {
			if s.SensorType == "pressure" || s.SensorType == "bog_compressor" {
				unitSensors[s.ModbusUnitID] = append(unitSensors[s.ModbusUnitID], s)
			}
		}

		if len(unitSensors) == 0 {
			continue
		}

		for unitID, unitSensorList := range unitSensors {
			client, ok := ing.clients[unitID]
			if !ok {
				continue
			}

			regs, err := client.ReadHoldingRegisters(uint16(unitID), 0, 100)
			if err != nil {
				log.Printf("modbus critical read error unit=%d: %v", unitID, err)
				continue
			}

			for _, s := range unitSensorList {
				regOffset := s.ModbusRegStart
				if regOffset+1 >= len(regs) {
					continue
				}

				rawVal := uint32(regs[regOffset])<<16 | uint32(regs[regOffset+1])
				value := math.Float64frombits(uint64(rawVal))

				switch s.SensorType {
				case "pressure":
					allPressures = append(allPressures, models.PressureReading{
						Time:     now,
						TankID:   tankID,
						SensorID: s.ID,
						ValueKpa: value,
						Quality:  "good",
					})
				case "bog_compressor":
					running := value > 0.5
					speedRPM := 0.0
					if running {
						speedRPM = 3000 + value*100
					}
					outletPressure := 15.0
					if running {
						outletPressure = 20.0 + value*5
					}
					allBOG = append(allBOG, models.BOGCompressorStatus{
						Time:              now,
						TankID:            tankID,
						CompressorID:      s.PositionIndex,
						Running:           running,
						SpeedRPM:          speedRPM,
						OutletPressureKpa: outletPressure,
					})
				}
			}
		}
	}

	if len(allPressures) > 0 {
		if err := ing.db.InsertPressureReadings(ctx, allPressures); err != nil {
			log.Printf("insert pressures error: %v", err)
		}
	}
	if len(allBOG) > 0 {
		if err := ing.db.InsertBOGStatus(ctx, allBOG); err != nil {
			log.Printf("insert BOG status error: %v", err)
		}
	}

	log.Printf("modbus critical poll: %d pressures, %d BOG statuses", len(allPressures), len(allBOG))
}

func (ing *Ingester) pollFull(ctx context.Context) {
	now := time.Now().UTC()
	ing.lastFullPoll = now

	var allTemps []models.TemperatureReading
	var allDensities []models.DensityReading
	var allPressures []models.PressureReading
	var allBOG []models.BOGCompressorStatus

	for tankID, sensors := range ing.sensors {
		criticalSensors := make(map[int][]models.Sensor)
		highSensors := make(map[int][]models.Sensor)
		normalSensors := make(map[int][]models.Sensor)

		for _, s := range sensors {
			switch sensorPriority(s) {
			case PriorityCritical:
				criticalSensors[s.ModbusUnitID] = append(criticalSensors[s.ModbusUnitID], s)
			case PriorityHigh:
				highSensors[s.ModbusUnitID] = append(highSensors[s.ModbusUnitID], s)
			default:
				normalSensors[s.ModbusUnitID] = append(normalSensors[s.ModbusUnitID], s)
			}
		}

		phaseOrder := []map[int][]models.Sensor{criticalSensors, highSensors, normalSensors}
		phaseNames := []string{"critical", "high", "normal"}

		for phaseIdx, phaseSensors := range phaseOrder {
			if len(phaseSensors) == 0 {
				continue
			}

			for unitID, unitSensorList := range phaseSensors {
				client, ok := ing.clients[unitID]
				if !ok {
					continue
				}

				regs, err := client.ReadHoldingRegisters(uint16(unitID), 0, 100)
				if err != nil {
					log.Printf("modbus %s read error unit=%d: %v", phaseNames[phaseIdx], unitID, err)
					continue
				}

				for _, s := range unitSensorList {
					regOffset := s.ModbusRegStart
					if regOffset+1 >= len(regs) {
						continue
					}

					rawVal := uint32(regs[regOffset])<<16 | uint32(regs[regOffset+1])
					value := math.Float64frombits(uint64(rawVal))

					switch s.SensorType {
					case "temperature":
						allTemps = append(allTemps, models.TemperatureReading{
							Time:          now,
							TankID:        tankID,
							SensorID:      s.ID,
							LayerIndex:    s.LayerIndex,
							PositionIndex: s.PositionIndex,
							ValueCelsius:  value,
							Quality:       "good",
						})
					case "density":
						allDensities = append(allDensities, models.DensityReading{
							Time:       now,
							TankID:     tankID,
							SensorID:   s.ID,
							LayerIndex: s.LayerIndex,
							ValueKgM3:  value,
							Quality:    "good",
						})
					case "pressure":
						allPressures = append(allPressures, models.PressureReading{
							Time:     now,
							TankID:   tankID,
							SensorID: s.ID,
							ValueKpa: value,
							Quality:  "good",
						})
					case "bog_compressor":
						running := value > 0.5
						speedRPM := 0.0
						if running {
							speedRPM = 3000 + value*100
						}
						outletPressure := 15.0
						if running {
							outletPressure = 20.0 + value*5
						}
						allBOG = append(allBOG, models.BOGCompressorStatus{
							Time:              now,
							TankID:            tankID,
							CompressorID:      s.PositionIndex,
							Running:           running,
							SpeedRPM:          speedRPM,
							OutletPressureKpa: outletPressure,
						})
					}
				}
			}
		}
	}

	if len(allTemps) > 0 {
		if err := ing.db.InsertTemperatureReadings(ctx, allTemps); err != nil {
			log.Printf("insert temperatures error: %v", err)
		}
	}
	if len(allDensities) > 0 {
		if err := ing.db.InsertDensityReadings(ctx, allDensities); err != nil {
			log.Printf("insert densities error: %v", err)
		}
	}
	if len(allPressures) > 0 {
		if err := ing.db.InsertPressureReadings(ctx, allPressures); err != nil {
			log.Printf("insert pressures error: %v", err)
		}
	}
	if len(allBOG) > 0 {
		if err := ing.db.InsertBOGStatus(ctx, allBOG); err != nil {
			log.Printf("insert BOG status error: %v", err)
		}
	}

	if ing.callback != nil {
		ing.callback(allTemps, allDensities, allPressures, allBOG)
	}

	log.Printf("modbus full poll: %d temps, %d densities, %d pressures, %d BOG",
		len(allTemps), len(allDensities), len(allPressures), len(allBOG))
}
