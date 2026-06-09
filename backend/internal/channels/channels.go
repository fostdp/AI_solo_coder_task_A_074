package channels

import (
	"time"

	"lng-monitor/internal/models"
)

type SensorBatch struct {
	Timestamp    time.Time
	TankID       int
	Temperatures []models.TemperatureReading
	Densities    []models.DensityReading
	Pressures    []models.PressureReading
	BOGStatuses  []models.BOGCompressorStatus
}

type PredictionOutput struct {
	Timestamp time.Time
	TankID    int
	RiskIndex float64
	Stability float64
	MaxTempGrad float64
	MaxDensGrad float64
	PredictedRollover *time.Time
}

type AlarmEvent struct {
	Alarm      models.Alarm
	Prediction *PredictionOutput
}

const (
	SensorBatchChanSize   = 64
	PredictionChanSize    = 16
	AlarmEventChanSize    = 64
)

func NewSensorBatchChan() chan SensorBatch {
	return make(chan SensorBatch, SensorBatchChanSize)
}

func NewPredictionChan() chan PredictionOutput {
	return make(chan PredictionOutput, PredictionChanSize)
}

func NewAlarmEventChan() chan AlarmEvent {
	return make(chan AlarmEvent, AlarmEventChanSize)
}
