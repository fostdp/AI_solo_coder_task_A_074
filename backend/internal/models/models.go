package models

import "time"

type Tank struct {
	ID             int     `json:"id"`
	TankCode       string  `json:"tank_code"`
	VolumeM3       float64 `json:"volume_m3"`
	DesignPressure float64 `json:"design_pressure_kpa"`
	HeightM        float64 `json:"height_m"`
	DiameterM      float64 `json:"diameter_m"`
	Status         string  `json:"status"`
}

type Sensor struct {
	ID              int    `json:"id"`
	TankID          int    `json:"tank_id"`
	SensorType      string `json:"sensor_type"`
	LayerIndex      int    `json:"layer_index"`
	PositionIndex   int    `json:"position_index"`
	Description     string `json:"description"`
	ModbusUnitID    int    `json:"modbus_unit_id"`
	ModbusRegStart  int    `json:"modbus_reg_start"`
}

type TemperatureReading struct {
	Time          time.Time `json:"time"`
	TankID        int       `json:"tank_id"`
	SensorID      int       `json:"sensor_id"`
	LayerIndex    int       `json:"layer_index"`
	PositionIndex int       `json:"position_index"`
	ValueCelsius  float64   `json:"value_celsius"`
	Quality       string    `json:"quality"`
}

type DensityReading struct {
	Time        time.Time `json:"time"`
	TankID      int       `json:"tank_id"`
	SensorID    int       `json:"sensor_id"`
	LayerIndex  int       `json:"layer_index"`
	ValueKgM3   float64   `json:"value_kg_m3"`
	Quality     string    `json:"quality"`
}

type PressureReading struct {
	Time     time.Time `json:"time"`
	TankID   int       `json:"tank_id"`
	SensorID int       `json:"sensor_id"`
	ValueKpa float64   `json:"value_kpa"`
	Quality  string    `json:"quality"`
}

type BOGCompressorStatus struct {
	Time             time.Time `json:"time"`
	TankID           int       `json:"tank_id"`
	CompressorID     int       `json:"compressor_id"`
	Running          bool      `json:"running"`
	SpeedRPM         float64   `json:"speed_rpm"`
	OutletPressureKpa float64  `json:"outlet_pressure_kpa"`
}

type RolloverPrediction struct {
	Time               time.Time  `json:"time"`
	TankID             int        `json:"tank_id"`
	RiskIndex          float64    `json:"risk_index"`
	LayerStabilityScore float64   `json:"layer_stability_score"`
	PredictedRolloverTime *time.Time `json:"predicted_rollover_time,omitempty"`
	MaxTempGradient    float64    `json:"max_temp_gradient"`
	MaxDensityGradient float64    `json:"max_density_gradient"`
	ModelVersion       string     `json:"model_version"`
}

type Alarm struct {
	ID              int       `json:"id"`
	Time            time.Time `json:"time"`
	TankID          int       `json:"tank_id"`
	AlarmLevel      int       `json:"alarm_level"`
	AlarmType       string    `json:"alarm_type"`
	Message         string    `json:"message"`
	Acknowledged    bool      `json:"acknowledged"`
	AcknowledgedBy  string    `json:"acknowledged_by,omitempty"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	OPCUAPushed     bool      `json:"opcua_pushed"`
	DCSConfirmed    bool      `json:"dcs_confirmed"`
}

type TankSnapshot struct {
	TankID       int                  `json:"tank_id"`
	TankCode     string               `json:"tank_code"`
	Temperatures []LayerTemperature   `json:"temperatures"`
	Densities    []LayerDensity       `json:"densities"`
	Pressure     float64              `json:"pressure_kpa"`
	BOGRunning   bool                 `json:"bog_running"`
	RiskIndex    float64              `json:"risk_index"`
	Alarms       []Alarm              `json:"alarms,omitempty"`
}

type LayerTemperature struct {
	LayerIndex int       `json:"layer_index"`
	AvgTemp    float64   `json:"avg_temp"`
	MinTemp    float64   `json:"min_temp"`
	MaxTemp    float64   `json:"max_temp"`
	Sensors    []SensorValue `json:"sensors"`
}

type LayerDensity struct {
	LayerIndex int     `json:"layer_index"`
	ValueKgM3  float64 `json:"value_kg_m3"`
}

type SensorValue struct {
	SensorID      int     `json:"sensor_id"`
	PositionIndex int     `json:"position_index"`
	Value         float64 `json:"value"`
}

type TrendData struct {
	SensorID  int          `json:"sensor_id"`
	SensorType string      `json:"sensor_type"`
	TankID    int          `json:"tank_id"`
	Points    []TrendPoint `json:"points"`
}

type TrendPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}
