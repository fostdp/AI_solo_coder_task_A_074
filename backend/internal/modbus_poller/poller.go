package modbus_poller

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	"lng-monitor/internal/channels"
	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"

	modbuscli "github.com/simonvetter/modbus"
)

const (
	PriorityCritical = 0
	PriorityHigh     = 1
	PriorityNormal   = 2
)

type Poller struct {
	cfg     *config.Config
	params  *config.PollerParams
	db      *database.DB
	clients map[int]*modbuscli.ModbusClient
	sensors map[int][]models.Sensor
	out     chan<- channels.SensorBatch
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

func NewPoller(cfg *config.Config, params *config.PollerParams, db *database.DB, out chan<- channels.SensorBatch) *Poller {
	return &Poller{
		cfg:     cfg,
		params:  params,
		db:      db,
		clients: make(map[int]*modbuscli.ModbusClient),
		sensors: make(map[int][]models.Sensor),
		out:     out,
	}
}

func (p *Poller) Start(ctx context.Context) error {
	tanks, err := p.db.GetTanks(ctx)
	if err != nil {
		return fmt.Errorf("poller load tanks: %w", err)
	}

	for _, tank := range tanks {
		sensors, err := p.db.GetSensorsByTank(ctx, tank.ID)
		if err != nil {
			return fmt.Errorf("poller load sensors tank %d: %w", tank.ID, err)
		}
		p.sensors[tank.ID] = sensors

		unitIDs := make(map[int]bool)
		for _, s := range sensors {
			unitIDs[s.ModbusUnitID] = true
		}
		for unitID := range unitIDs {
			if _, exists := p.clients[unitID]; !exists {
				client, err := modbuscli.NewClient(&modbuscli.ClientConfiguration{
					URL:     fmt.Sprintf("tcp://%s:%d", p.cfg.ModbusHost, p.cfg.ModbusPort),
					Timeout: time.Duration(p.params.ModbusTimeoutSec) * time.Second,
				})
				if err != nil {
					return fmt.Errorf("poller modbus client unit %d: %w", unitID, err)
				}
				p.clients[unitID] = client
			}
		}
	}

	criticalInterval := time.Duration(p.params.CriticalIntervalSec) * time.Second
	fullInterval := time.Duration(p.params.FullIntervalSec) * time.Second

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
				p.pollCritical(ctx)
			case <-fullTicker.C:
				p.pollFull(ctx)
			}
		}
	}()

	log.Printf("modbus_poller started (critical=%s full=%s)", criticalInterval, fullInterval)
	return nil
}

func (p *Poller) pollCritical(ctx context.Context) {
	now := time.Now().UTC()

	for tankID, sensors := range p.sensors {
		var pressures []models.PressureReading
		var bogStatuses []models.BOGCompressorStatus

		for _, s := range sensors {
			if s.SensorType != "pressure" && s.SensorType != "bog_compressor" {
				continue
			}
			client, ok := p.clients[s.ModbusUnitID]
			if !ok {
				continue
			}

			regs, err := client.ReadHoldingRegisters(uint16(s.ModbusUnitID), 0, 100)
			if err != nil {
				log.Printf("poller critical read error unit=%d: %v", s.ModbusUnitID, err)
				continue
			}

			regOffset := s.ModbusRegStart
			if regOffset+1 >= len(regs) {
				continue
			}
			rawVal := uint32(regs[regOffset])<<16 | uint32(regs[regOffset+1])
			value := math.Float64frombits(uint64(rawVal))

			switch s.SensorType {
			case "pressure":
				pressures = append(pressures, models.PressureReading{
					Time: now, TankID: tankID, SensorID: s.ID, ValueKpa: value, Quality: "good",
				})
			case "bog_compressor":
				running := value > 0.5
				speedRPM := 0.0
				if running {
					speedRPM = 3000 + value*100
				}
				outletP := 15.0
				if running {
					outletP = 20.0 + value*5
				}
				bogStatuses = append(bogStatuses, models.BOGCompressorStatus{
					Time: now, TankID: tankID, CompressorID: s.PositionIndex,
					Running: running, SpeedRPM: speedRPM, OutletPressureKpa: outletP,
				})
			}
		}

		if len(pressures) > 0 {
			_ = p.db.InsertPressureReadings(ctx, pressures)
		}
		if len(bogStatuses) > 0 {
			_ = p.db.InsertBOGStatus(ctx, bogStatuses)
		}

		p.out <- channels.SensorBatch{
			Timestamp:    now,
			TankID:       tankID,
			Pressures:    pressures,
			BOGStatuses:  bogStatuses,
		}
	}
}

func (p *Poller) pollFull(ctx context.Context) {
	now := time.Now().UTC()

	for tankID, sensors := range p.sensors {
		criticalSensors, highSensors, normalSensors := p.classifySensors(sensors)
		phaseOrder := []map[int][]models.Sensor{criticalSensors, highSensors, normalSensors}

		var allTemps []models.TemperatureReading
		var allDens []models.DensityReading
		var allPres []models.PressureReading
		var allBOG []models.BOGCompressorStatus

		for _, phaseSensors := range phaseOrder {
			for unitID, unitSensorList := range phaseSensors {
				client, ok := p.clients[unitID]
				if !ok {
					continue
				}

				regs, err := client.ReadHoldingRegisters(uint16(unitID), 0, 100)
				if err != nil {
					log.Printf("poller full read error unit=%d: %v", unitID, err)
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
							Time: now, TankID: tankID, SensorID: s.ID,
							LayerIndex: s.LayerIndex, PositionIndex: s.PositionIndex,
							ValueCelsius: value, Quality: "good",
						})
					case "density":
						allDens = append(allDens, models.DensityReading{
							Time: now, TankID: tankID, SensorID: s.ID,
							LayerIndex: s.LayerIndex, ValueKgM3: value, Quality: "good",
						})
					case "pressure":
						allPres = append(allPres, models.PressureReading{
							Time: now, TankID: tankID, SensorID: s.ID, ValueKpa: value, Quality: "good",
						})
					case "bog_compressor":
						running := value > 0.5
						speedRPM := 0.0
						if running {
							speedRPM = 3000 + value*100
						}
						outletP := 15.0
						if running {
							outletP = 20.0 + value*5
						}
						allBOG = append(allBOG, models.BOGCompressorStatus{
							Time: now, TankID: tankID, CompressorID: s.PositionIndex,
							Running: running, SpeedRPM: speedRPM, OutletPressureKpa: outletP,
						})
					}
				}
			}
		}

		if len(allTemps) > 0 {
			_ = p.db.InsertTemperatureReadings(ctx, allTemps)
		}
		if len(allDens) > 0 {
			_ = p.db.InsertDensityReadings(ctx, allDens)
		}
		if len(allPres) > 0 {
			_ = p.db.InsertPressureReadings(ctx, allPres)
		}
		if len(allBOG) > 0 {
			_ = p.db.InsertBOGStatus(ctx, allBOG)
		}

		p.out <- channels.SensorBatch{
			Timestamp:    now,
			TankID:       tankID,
			Temperatures: allTemps,
			Densities:    allDens,
			Pressures:    allPres,
			BOGStatuses:  allBOG,
		}
	}
}

func (p *Poller) classifySensors(sensors []models.Sensor) (
	map[int][]models.Sensor,
	map[int][]models.Sensor,
	map[int][]models.Sensor,
) {
	critical := make(map[int][]models.Sensor)
	high := make(map[int][]models.Sensor)
	normal := make(map[int][]models.Sensor)

	for _, s := range sensors {
		switch sensorPriority(s) {
		case PriorityCritical:
			critical[s.ModbusUnitID] = append(critical[s.ModbusUnitID], s)
		case PriorityHigh:
			high[s.ModbusUnitID] = append(high[s.ModbusUnitID], s)
		default:
			normal[s.ModbusUnitID] = append(normal[s.ModbusUnitID], s)
		}
	}
	return critical, high, normal
}
