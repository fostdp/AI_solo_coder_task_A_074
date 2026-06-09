package config

import (
	"os"
	"strconv"
)

type Config struct {
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string

	HTTPPort   int
	WSPath     string

	ModbusHost string
	ModbusPort int
	ModbusTimeout int

	OPCUAEndpoint  string
	OPCUASecurityMode string

	AlarmTempDiffThreshold    float64
	AlarmDensityDiffThreshold float64
	AlarmPressureThresholdPct float64

	PredictionIntervalSec int
	DataIntervalSec       int
}

func Load() *Config {
	return &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnvInt("DB_PORT", 5432),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", "postgres"),
		DBName:     getEnv("DB_NAME", "lng_monitor"),

		HTTPPort: getEnvInt("HTTP_PORT", 8080),
		WSPath:   getEnv("WS_PATH", "/ws"),

		ModbusHost:    getEnv("MODBUS_HOST", "localhost"),
		ModbusPort:    getEnvInt("MODBUS_PORT", 502),
		ModbusTimeout: getEnvInt("MODBUS_TIMEOUT", 5),

		OPCUAEndpoint:     getEnv("OPCUA_ENDPOINT", "opc.tcp://localhost:4840"),
		OPCUASecurityMode: getEnv("OPCUA_SECURITY_MODE", "None"),

		AlarmTempDiffThreshold:    getEnvFloat("ALARM_TEMP_DIFF_THRESHOLD", 8.0),
		AlarmDensityDiffThreshold: getEnvFloat("ALARM_DENSITY_DIFF_THRESHOLD", 2.0),
		AlarmPressureThresholdPct: getEnvFloat("ALARM_PRESSURE_THRESHOLD_PCT", 0.9),

		PredictionIntervalSec: getEnvInt("PREDICTION_INTERVAL_SEC", 60),
		DataIntervalSec:       getEnvInt("DATA_INTERVAL_SEC", 30),
	}
}

func (c *Config) DSN() string {
	return "postgres://" + c.DBUser + ":" + c.DBPassword +
		"@" + c.DBHost + ":" + strconv.Itoa(c.DBPort) + "/" + c.DBName +
		"?sslmode=disable"
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
