package modbus

import (
	"context"
	"fmt"
	"log"
	"math"
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

type Ingester struct {
	cfg      *config.Config
	db       *database.DB
	clients  map[int]*modbuscli.ModbusClient
	sensors  map[int][]models.Sensor
	callback DataCallback
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

	for _, tank := range tanks {
		sensors, err := ing.db.GetSensorsByTank(ctx, tank.ID)
		if err != nil {
			return fmt.Errorf("load sensors for tank %d: %w", tank.ID, err)
		}
		ing.sensors[tank.ID] = sensors

		unitIDs := make(map[int]bool)
		for _, s := range sensors {
			unitIDs[s.ModbusUnitID] = true
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

	ticker := time.NewTicker(time.Duration(ing.cfg.DataIntervalSec) * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				ing.poll(ctx)
			}
		}
	}()

	log.Println("modbus ingester started")
	return nil
}

func (ing *Ingester) poll(ctx context.Context) {
	now := time.Now().UTC()

	var allTemps []models.TemperatureReading
	var allDensities []models.DensityReading
	var allPressures []models.PressureReading
	var allBOG []models.BOGCompressorStatus

	for tankID, sensors := range ing.sensors {
		unitSensors := make(map[int][]models.Sensor)
		for _, s := range sensors {
			unitSensors[s.ModbusUnitID] = append(unitSensors[s.ModbusUnitID], s)
		}

		for unitID, unitSensorList := range unitSensors {
			client, ok := ing.clients[unitID]
			if !ok {
				continue
			}

			regs, err := client.ReadHoldingRegisters(uint16(unitID), 0, 100)
			if err != nil {
				log.Printf("modbus read error unit=%d: %v", unitID, err)
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
}
