package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

type ModelParams struct {
	FVM        FVMParams        `json:"fvm"`
	Alarm      AlarmParams      `json:"alarm"`
	Poller     PollerParams     `json:"poller"`
	OPCUA      OPCUAParams      `json:"opcua"`
	Prediction PredictionParams `json:"prediction"`
}

type FVMParams struct {
	NumLayers           int       `json:"num_layers"`
	LNGSpecificHeat     float64   `json:"lng_specific_heat"`
	LNGThermalConduct   float64   `json:"lng_thermal_conduct"`
	LNGKinematicVisc    float64   `json:"lng_kinematic_visc"`
	LNGThermalExpansion float64   `json:"lng_thermal_expansion"`
	GravitationalAccel  float64   `json:"gravitational_accel"`
	AlphaTempRelax      float64   `json:"alpha_temp_relax"`
	AlphaDensityRelax   float64   `json:"alpha_density_relax"`
	CFLLimit            float64   `json:"cfl_limit"`
	MinTimeStep         float64   `json:"min_time_step"`
	MaxTimeStep         float64   `json:"max_time_step"`
	MaxSubIterations    int       `json:"max_sub_iterations"`
	ConvergenceTol      float64   `json:"convergence_tol"`
	MaxTempChange       float64   `json:"max_temp_change"`
	MaxDensityChange    float64   `json:"max_density_change"`
	TankHeight          float64   `json:"tank_height"`
	TankVolume          float64   `json:"tank_volume"`
	MassDiffusionCoeff  float64   `json:"mass_diffusion_coeff"`
	RACritical         float64   `json:"ra_critical"`
	TempRange           [2]float64 `json:"temp_range"`
	DensityRange        [2]float64 `json:"density_range"`
	HistoryWindow       int       `json:"history_window"`
}

type AlarmParams struct {
	TempDiffThreshold    float64 `json:"temp_diff_threshold"`
	DensityDiffThreshold float64 `json:"density_diff_threshold"`
	PressureThresholdPct float64 `json:"pressure_threshold_pct"`
	CooldownMinutes      int     `json:"cooldown_minutes"`
	EvalIntervalSec      int     `json:"eval_interval_sec"`
	BOGCompressorSpeedRPM float64 `json:"bog_compressor_speed_rpm"`
}

type PollerParams struct {
	CriticalIntervalSec int `json:"critical_interval_sec"`
	FullIntervalSec     int `json:"full_interval_sec"`
	ModbusTimeoutSec    int `json:"modbus_timeout_sec"`
}

type OPCUAParams struct {
	ReconnectIntervalSec int `json:"reconnect_interval_sec"`
	HeartbeatIntervalSec int `json:"heartbeat_interval_sec"`
	HeartbeatMaxFailures int `json:"heartbeat_max_failures"`
	MaxPendingAlarms     int `json:"max_pending_alarms"`
	StaleAlarmMinutes    int `json:"stale_alarm_minutes"`
}

type PredictionParams struct {
	IntervalSec      int              `json:"interval_sec"`
	RiskWeights      RiskWeights      `json:"risk_weights"`
	DriftWindow      int              `json:"drift_window"`
	RolloverTriggerRisk float64       `json:"rollover_trigger_risk"`
	RolloverTargetRisk  float64       `json:"rollover_target_risk"`
}

type RiskWeights struct {
	TempContrib    float64 `json:"temp_contrib"`
	DensityContrib float64 `json:"density_contrib"`
	RAContrib      float64 `json:"ra_contrib"`
}

var (
	params     *ModelParams
	paramsOnce sync.Once
)

func LoadModelParams(path string) (*ModelParams, error) {
	if params != nil {
		return params, nil
	}

	var loadErr error
	paramsOnce.Do(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			loadErr = fmt.Errorf("read model params %s: %w", path, err)
			return
		}

		var p ModelParams
		if err := json.Unmarshal(data, &p); err != nil {
			loadErr = fmt.Errorf("parse model params: %w", err)
			return
		}

		params = &p
	})

	if loadErr != nil {
		paramsOnce = sync.Once{}
		return nil, loadErr
	}

	return params, nil
}

func DefaultModelParams() *ModelParams {
	return &ModelParams{
		FVM: FVMParams{
			NumLayers:           5,
			LNGSpecificHeat:     3.47,
			LNGThermalConduct:   0.19,
			LNGKinematicVisc:    2.7e-7,
			LNGThermalExpansion: 3.6e-3,
			GravitationalAccel:  9.81,
			AlphaTempRelax:      0.6,
			AlphaDensityRelax:   0.5,
			CFLLimit:            0.45,
			MinTimeStep:         0.1,
			MaxTimeStep:         30.0,
			MaxSubIterations:    20,
			ConvergenceTol:      1e-4,
			MaxTempChange:       0.5,
			MaxDensityChange:    0.2,
			TankHeight:          38.0,
			TankVolume:          160000.0,
			MassDiffusionCoeff:  1e-9,
			RACritical:         1e8,
			TempRange:           [2]float64{-170, -150},
			DensityRange:        [2]float64{440, 470},
			HistoryWindow:       2880,
		},
		Alarm: AlarmParams{
			TempDiffThreshold:     8.0,
			DensityDiffThreshold:  2.0,
			PressureThresholdPct:  0.9,
			CooldownMinutes:       5,
			EvalIntervalSec:       30,
			BOGCompressorSpeedRPM: 3600,
		},
		Poller: PollerParams{
			CriticalIntervalSec: 10,
			FullIntervalSec:     30,
			ModbusTimeoutSec:    5,
		},
		OPCUA: OPCUAParams{
			ReconnectIntervalSec: 5,
			HeartbeatIntervalSec: 15,
			HeartbeatMaxFailures: 3,
			MaxPendingAlarms:     100,
			StaleAlarmMinutes:    10,
		},
		Prediction: PredictionParams{
			IntervalSec: 60,
			RiskWeights: RiskWeights{
				TempContrib:    0.4,
				DensityContrib: 0.4,
				RAContrib:      0.2,
			},
			DriftWindow:        10,
			RolloverTriggerRisk: 0.3,
			RolloverTargetRisk:  0.8,
		},
	}
}
