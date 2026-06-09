package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, cfg *config.Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	poolCfg.MaxConns = 20
	poolCfg.MinConns = 5
	poolCfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	log.Println("database connected successfully")
	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

func (db *DB) InsertTemperatureReadings(ctx context.Context, readings []models.TemperatureReading) error {
	batch := make([][]interface{}, 0, len(readings))
	for _, r := range readings {
		batch = append(batch, []interface{}{
			r.Time, r.TankID, r.SensorID, r.LayerIndex, r.PositionIndex, r.ValueCelsius, r.Quality,
		})
	}
	_, err := db.pool.CopyFrom(
		ctx,
		[]string{"temperature_readings"},
		[]string{"time", "tank_id", "sensor_id", "layer_index", "position_index", "value_celsius", "quality"},
		pgx.CopyFromSlice(len(batch), func(i int) ([]interface{}, error) {
			return batch[i], nil
		}),
	)
	return err
}

func (db *DB) InsertDensityReadings(ctx context.Context, readings []models.DensityReading) error {
	batch := make([][]interface{}, 0, len(readings))
	for _, r := range readings {
		batch = append(batch, []interface{}{
			r.Time, r.TankID, r.SensorID, r.LayerIndex, r.ValueKgM3, r.Quality,
		})
	}
	_, err := db.pool.CopyFrom(
		ctx,
		[]string{"density_readings"},
		[]string{"time", "tank_id", "sensor_id", "layer_index", "value_kg_m3", "quality"},
		pgx.CopyFromSlice(len(batch), func(i int) ([]interface{}, error) {
			return batch[i], nil
		}),
	)
	return err
}

func (db *DB) InsertPressureReadings(ctx context.Context, readings []models.PressureReading) error {
	batch := make([][]interface{}, 0, len(readings))
	for _, r := range readings {
		batch = append(batch, []interface{}{
			r.Time, r.TankID, r.SensorID, r.ValueKpa, r.Quality,
		})
	}
	_, err := db.pool.CopyFrom(
		ctx,
		[]string{"pressure_readings"},
		[]string{"time", "tank_id", "sensor_id", "value_kpa", "quality"},
		pgx.CopyFromSlice(len(batch), func(i int) ([]interface{}, error) {
			return batch[i], nil
		}),
	)
	return err
}

func (db *DB) InsertBOGStatus(ctx context.Context, statuses []models.BOGCompressorStatus) error {
	batch := make([][]interface{}, 0, len(statuses))
	for _, s := range statuses {
		batch = append(batch, []interface{}{
			s.Time, s.TankID, s.CompressorID, s.Running, s.SpeedRPM, s.OutletPressureKpa,
		})
	}
	_, err := db.pool.CopyFrom(
		ctx,
		[]string{"bog_compressor_status"},
		[]string{"time", "tank_id", "compressor_id", "running", "speed_rpm", "outlet_pressure_kpa"},
		pgx.CopyFromSlice(len(batch), func(i int) ([]interface{}, error) {
			return batch[i], nil
		}),
	)
	return err
}

func (db *DB) InsertPrediction(ctx context.Context, p models.RolloverPrediction) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO rollover_predictions (time, tank_id, risk_index, layer_stability_score,
		 predicted_rollover_time, max_temp_gradient, max_density_gradient, model_version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.Time, p.TankID, p.RiskIndex, p.LayerStabilityScore,
		p.PredictedRolloverTime, p.MaxTempGradient, p.MaxDensityGradient, p.ModelVersion,
	)
	return err
}

func (db *DB) InsertAlarm(ctx context.Context, a models.Alarm) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO alarms (time, tank_id, alarm_level, alarm_type, message, acknowledged, opcua_pushed, dcs_confirmed)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.Time, a.TankID, a.AlarmLevel, a.AlarmType, a.Message, a.Acknowledged, a.OPCUAPushed, a.DCSConfirmed,
	)
	return err
}

func (db *DB) GetTanks(ctx context.Context) ([]models.Tank, error) {
	rows, err := db.pool.Query(ctx, `SELECT id, tank_code, volume_m3, design_pressure_kpa, height_m, diameter_m, status FROM tanks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tanks []models.Tank
	for rows.Next() {
		var t models.Tank
		if err := rows.Scan(&t.ID, &t.TankCode, &t.VolumeM3, &t.DesignPressure, &t.HeightM, &t.DiameterM, &t.Status); err != nil {
			return nil, err
		}
		tanks = append(tanks, t)
	}
	return tanks, nil
}

func (db *DB) GetSensorsByTank(ctx context.Context, tankID int) ([]models.Sensor, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start
		 FROM sensors WHERE tank_id = $1 ORDER BY sensor_type, layer_index, position_index`, tankID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sensors []models.Sensor
	for rows.Next() {
		var s models.Sensor
		if err := rows.Scan(&s.ID, &s.TankID, &s.SensorType, &s.LayerIndex, &s.PositionIndex,
			&s.Description, &s.ModbusUnitID, &s.ModbusRegStart); err != nil {
			return nil, err
		}
		sensors = append(sensors, s)
	}
	return sensors, nil
}

func (db *DB) GetLatestTemperatures(ctx context.Context, tankID int) ([]models.TemperatureReading, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT DISTINCT ON (layer_index, position_index)
		 time, tank_id, sensor_id, layer_index, position_index, value_celsius, quality
		 FROM temperature_readings
		 WHERE tank_id = $1 AND time > NOW() - INTERVAL '5 minutes'
		 ORDER BY layer_index, position_index, time DESC`, tankID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var readings []models.TemperatureReading
	for rows.Next() {
		var r models.TemperatureReading
		if err := rows.Scan(&r.Time, &r.TankID, &r.SensorID, &r.LayerIndex, &r.PositionIndex, &r.ValueCelsius, &r.Quality); err != nil {
			return nil, err
		}
		readings = append(readings, r)
	}
	return readings, nil
}

func (db *DB) GetLatestDensities(ctx context.Context, tankID int) ([]models.DensityReading, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT DISTINCT ON (layer_index)
		 time, tank_id, sensor_id, layer_index, value_kg_m3, quality
		 FROM density_readings
		 WHERE tank_id = $1 AND time > NOW() - INTERVAL '5 minutes'
		 ORDER BY layer_index, time DESC`, tankID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var readings []models.DensityReading
	for rows.Next() {
		var r models.DensityReading
		if err := rows.Scan(&r.Time, &r.TankID, &r.SensorID, &r.LayerIndex, &r.ValueKgM3, &r.Quality); err != nil {
			return nil, err
		}
		readings = append(readings, r)
	}
	return readings, nil
}

func (db *DB) GetLatestPressure(ctx context.Context, tankID int) (float64, error) {
	var val float64
	err := db.pool.QueryRow(ctx,
		`SELECT value_kpa FROM pressure_readings
		 WHERE tank_id = $1 ORDER BY time DESC LIMIT 1`, tankID).Scan(&val)
	return val, err
}

func (db *DB) GetLatestBOGStatus(ctx context.Context, tankID int) (models.BOGCompressorStatus, error) {
	var s models.BOGCompressorStatus
	err := db.pool.QueryRow(ctx,
		`SELECT time, tank_id, compressor_id, running, speed_rpm, outlet_pressure_kpa
		 FROM bog_compressor_status
		 WHERE tank_id = $1 ORDER BY time DESC LIMIT 1`, tankID).Scan(
		&s.Time, &s.TankID, &s.CompressorID, &s.Running, &s.SpeedRPM, &s.OutletPressureKpa)
	return s, err
}

func (db *DB) GetTemperatureTrend(ctx context.Context, sensorID int, hours int) ([]models.TrendPoint, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT time_bucket('5 minutes', time) AS bucket, AVG(value_celsius) AS avg_val
		 FROM temperature_readings
		 WHERE sensor_id = $1 AND time > NOW() - make_interval(hours => $2)
		 GROUP BY bucket ORDER BY bucket`, sensorID, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []models.TrendPoint
	for rows.Next() {
		var p models.TrendPoint
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, nil
}

func (db *DB) GetDensityTrend(ctx context.Context, sensorID int, hours int) ([]models.TrendPoint, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT time_bucket('5 minutes', time) AS bucket, AVG(value_kg_m3) AS avg_val
		 FROM density_readings
		 WHERE sensor_id = $1 AND time > NOW() - make_interval(hours => $2)
		 GROUP BY bucket ORDER BY bucket`, sensorID, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []models.TrendPoint
	for rows.Next() {
		var p models.TrendPoint
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, nil
}

func (db *DB) GetRecentAlarms(ctx context.Context, tankID int, limit int) ([]models.Alarm, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, time, tank_id, alarm_level, alarm_type, message, acknowledged,
		 acknowledged_by, acknowledged_at, opcua_pushed, dcs_confirmed
		 FROM alarms WHERE tank_id = $1 ORDER BY time DESC LIMIT $2`, tankID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alarms []models.Alarm
	for rows.Next() {
		var a models.Alarm
		if err := rows.Scan(&a.ID, &a.Time, &a.TankID, &a.AlarmLevel, &a.AlarmType, &a.Message,
			&a.Acknowledged, &a.AcknowledgedBy, &a.AcknowledgedAt, &a.OPCUAPushed, &a.DCSConfirmed); err != nil {
			return nil, err
		}
		alarms = append(alarms, a)
	}
	return alarms, nil
}

func (db *DB) GetLatestPrediction(ctx context.Context, tankID int) (models.RolloverPrediction, error) {
	var p models.RolloverPrediction
	err := db.pool.QueryRow(ctx,
		`SELECT time, tank_id, risk_index, layer_stability_score,
		 predicted_rollover_time, max_temp_gradient, max_density_gradient, model_version
		 FROM rollover_predictions WHERE tank_id = $1 ORDER BY time DESC LIMIT 1`, tankID).Scan(
		&p.Time, &p.TankID, &p.RiskIndex, &p.LayerStabilityScore,
		&p.PredictedRolloverTime, &p.MaxTempGradient, &p.MaxDensityGradient, &p.ModelVersion)
	return p, err
}

func (db *DB) AcknowledgeAlarm(ctx context.Context, alarmID int, user string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE alarms SET acknowledged = TRUE, acknowledged_by = $1, acknowledged_at = NOW() WHERE id = $2`,
		user, alarmID)
	return err
}
