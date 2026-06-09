-- TimescaleDB 初始化脚本 - LNG储罐翻滚预测与安全监控系统
-- 执行前提: PostgreSQL 15+ 已安装 TimescaleDB 扩展

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- 储罐基础信息表
CREATE TABLE IF NOT EXISTS tanks (
    id SERIAL PRIMARY KEY,
    tank_code VARCHAR(16) NOT NULL UNIQUE,
    volume_m3 NUMERIC(12,2) NOT NULL,
    design_pressure_kpa NUMERIC(8,2) NOT NULL,
    height_m NUMERIC(6,2) NOT NULL,
    diameter_m NUMERIC(6,2) NOT NULL,
    status VARCHAR(16) DEFAULT 'online'
);

-- 传感器配置表
CREATE TABLE IF NOT EXISTS sensors (
    id SERIAL PRIMARY KEY,
    tank_id INTEGER REFERENCES tanks(id),
    sensor_type VARCHAR(16) NOT NULL,
    layer_index INTEGER,
    position_index INTEGER,
    description VARCHAR(128),
    modbus_unit_id INTEGER NOT NULL,
    modbus_reg_start INTEGER NOT NULL
);

-- 温度数据时序表 (超表)
CREATE TABLE IF NOT EXISTS temperature_readings (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL,
    sensor_id INTEGER NOT NULL,
    layer_index INTEGER NOT NULL,
    position_index INTEGER NOT NULL,
    value_celsius DOUBLE PRECISION NOT NULL,
    quality VARCHAR(8) DEFAULT 'good'
);

SELECT create_hypertable('temperature_readings', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- 密度数据时序表 (超表)
CREATE TABLE IF NOT EXISTS density_readings (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL,
    sensor_id INTEGER NOT NULL,
    layer_index INTEGER NOT NULL,
    value_kg_m3 DOUBLE PRECISION NOT NULL,
    quality VARCHAR(8) DEFAULT 'good'
);

SELECT create_hypertable('density_readings', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- 压力数据时序表 (超表)
CREATE TABLE IF NOT EXISTS pressure_readings (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL,
    sensor_id INTEGER NOT NULL,
    value_kpa DOUBLE PRECISION NOT NULL,
    quality VARCHAR(8) DEFAULT 'good'
);

SELECT create_hypertable('pressure_readings', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- BOG压缩机状态表
CREATE TABLE IF NOT EXISTS bog_compressor_status (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL,
    compressor_id INTEGER NOT NULL,
    running BOOLEAN NOT NULL,
    speed_rpm DOUBLE PRECISION,
    outlet_pressure_kpa DOUBLE PRECISION
);

SELECT create_hypertable('bog_compressor_status', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- 翻滚预测结果表
CREATE TABLE IF NOT EXISTS rollover_predictions (
    time TIMESTAMPTZ NOT NULL,
    tank_id INTEGER NOT NULL,
    risk_index DOUBLE PRECISION NOT NULL,
    layer_stability_score DOUBLE PRECISION NOT NULL,
    predicted_rollover_time TIMESTAMPTZ,
    max_temp_gradient DOUBLE PRECISION NOT NULL,
    max_density_gradient DOUBLE PRECISION NOT NULL,
    model_version VARCHAR(16) DEFAULT 'fvm-v1'
);

SELECT create_hypertable('rollover_predictions', 'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- 告警记录表
CREATE TABLE IF NOT EXISTS alarms (
    id SERIAL PRIMARY KEY,
    time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tank_id INTEGER NOT NULL,
    alarm_level INTEGER NOT NULL CHECK (alarm_level IN (1, 2)),
    alarm_type VARCHAR(32) NOT NULL,
    message TEXT NOT NULL,
    acknowledged BOOLEAN DEFAULT FALSE,
    acknowledged_by VARCHAR(64),
    acknowledged_at TIMESTAMPTZ,
    opcua_pushed BOOLEAN DEFAULT FALSE,
    dcs_confirmed BOOLEAN DEFAULT FALSE
);

-- 层间统计聚合表 (连续聚合 - 温度)
CREATE MATERIALIZED VIEW IF NOT EXISTS temperature_layer_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    tank_id,
    layer_index,
    AVG(value_celsius) AS avg_temp,
    MIN(value_celsius) AS min_temp,
    MAX(value_celsius) AS max_temp,
    STDDEV(value_celsius) AS stddev_temp
FROM temperature_readings
GROUP BY bucket, tank_id, layer_index;

-- 层间统计聚合表 (连续聚合 - 密度)
CREATE MATERIALIZED VIEW IF NOT EXISTS density_layer_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', time) AS bucket,
    tank_id,
    layer_index,
    AVG(value_kg_m3) AS avg_density,
    MIN(value_kg_m3) AS min_density,
    MAX(value_kg_m3) AS max_density,
    STDDEV(value_kg_m3) AS stddev_density
FROM density_readings
GROUP BY bucket, tank_id, layer_index;

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_temp_tank_time ON temperature_readings (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_density_tank_time ON density_readings (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_pressure_tank_time ON pressure_readings (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_alarm_tank_time ON alarms (tank_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_prediction_tank_time ON rollover_predictions (tank_id, time DESC);

-- 数据保留策略 (原始数据保留90天, 聚合数据保留2年)
SELECT add_retention_policy('temperature_readings', INTERVAL '90 days', if_not_exists => TRUE);
SELECT add_retention_policy('density_readings', INTERVAL '90 days', if_not_exists => TRUE);
SELECT add_retention_policy('pressure_readings', INTERVAL '90 days', if_not_exists => TRUE);
SELECT add_retention_policy('bog_compressor_status', INTERVAL '90 days', if_not_exists => TRUE);

-- 插入4座16万立方米储罐基础数据
INSERT INTO tanks (tank_code, volume_m3, design_pressure_kpa, height_m, diameter_m, status) VALUES
('T-101', 160000, 25.0, 38.0, 80.0, 'online'),
('T-102', 160000, 25.0, 38.0, 80.0, 'online'),
('T-103', 160000, 25.0, 38.0, 80.0, 'online'),
('T-104', 160000, 25.0, 38.0, 80.0, 'online');

-- 插入传感器配置: 每罐5层x8个温度计 + 3台密度计 + 1个压力变送器
-- T-101 温度传感器 (Modbus Unit ID = 1)
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start) VALUES
(1, 'temperature', 1, 1, 'T-101-L1-P1', 1, 0),
(1, 'temperature', 1, 2, 'T-101-L1-P2', 1, 2),
(1, 'temperature', 1, 3, 'T-101-L1-P3', 1, 4),
(1, 'temperature', 1, 4, 'T-101-L1-P4', 1, 6),
(1, 'temperature', 1, 5, 'T-101-L1-P5', 1, 8),
(1, 'temperature', 1, 6, 'T-101-L1-P6', 1, 10),
(1, 'temperature', 1, 7, 'T-101-L1-P7', 1, 12),
(1, 'temperature', 1, 8, 'T-101-L1-P8', 1, 14),
(1, 'temperature', 2, 1, 'T-101-L2-P1', 1, 16),
(1, 'temperature', 2, 2, 'T-101-L2-P2', 1, 18),
(1, 'temperature', 2, 3, 'T-101-L2-P3', 1, 20),
(1, 'temperature', 2, 4, 'T-101-L2-P4', 1, 22),
(1, 'temperature', 2, 5, 'T-101-L2-P5', 1, 24),
(1, 'temperature', 2, 6, 'T-101-L2-P6', 1, 26),
(1, 'temperature', 2, 7, 'T-101-L2-P7', 1, 28),
(1, 'temperature', 2, 8, 'T-101-L2-P8', 1, 30),
(1, 'temperature', 3, 1, 'T-101-L3-P1', 1, 32),
(1, 'temperature', 3, 2, 'T-101-L3-P2', 1, 34),
(1, 'temperature', 3, 3, 'T-101-L3-P3', 1, 36),
(1, 'temperature', 3, 4, 'T-101-L3-P4', 1, 38),
(1, 'temperature', 3, 5, 'T-101-L3-P5', 1, 40),
(1, 'temperature', 3, 6, 'T-101-L3-P6', 1, 42),
(1, 'temperature', 3, 7, 'T-101-L3-P7', 1, 44),
(1, 'temperature', 3, 8, 'T-101-L3-P8', 1, 46),
(1, 'temperature', 4, 1, 'T-101-L4-P1', 1, 48),
(1, 'temperature', 4, 2, 'T-101-L4-P2', 1, 50),
(1, 'temperature', 4, 3, 'T-101-L4-P3', 1, 52),
(1, 'temperature', 4, 4, 'T-101-L4-P4', 1, 54),
(1, 'temperature', 4, 5, 'T-101-L4-P5', 1, 56),
(1, 'temperature', 4, 6, 'T-101-L4-P6', 1, 58),
(1, 'temperature', 4, 7, 'T-101-L4-P7', 1, 60),
(1, 'temperature', 4, 8, 'T-101-L4-P8', 1, 62),
(1, 'temperature', 5, 1, 'T-101-L5-P1', 1, 64),
(1, 'temperature', 5, 2, 'T-101-L5-P2', 1, 66),
(1, 'temperature', 5, 3, 'T-101-L5-P3', 1, 68),
(1, 'temperature', 5, 4, 'T-101-L5-P4', 1, 70),
(1, 'temperature', 5, 5, 'T-101-L5-P5', 1, 72),
(1, 'temperature', 5, 6, 'T-101-L5-P6', 1, 74),
(1, 'temperature', 5, 7, 'T-101-L5-P7', 1, 76),
(1, 'temperature', 5, 8, 'T-101-L5-P8', 1, 78);

-- T-101 密度计
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start) VALUES
(1, 'density', 1, 1, 'T-101-D-L1', 1, 80),
(1, 'density', 3, 1, 'T-101-D-L3', 1, 82),
(1, 'density', 5, 1, 'T-101-D-L5', 1, 84);

-- T-101 压力变送器
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start) VALUES
(1, 'pressure', 0, 1, 'T-101-PT', 1, 86);

-- T-101 BOG压缩机
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start) VALUES
(1, 'bog_compressor', 0, 1, 'T-101-BOG', 1, 88);

-- T-102 传感器 (Modbus Unit ID = 2, 寄存器偏移同T-101)
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start)
SELECT 2, sensor_type, layer_index, position_index,
       REPLACE(description, 'T-101', 'T-102'), 2, modbus_reg_start
FROM sensors WHERE tank_id = 1;

-- T-103 传感器 (Modbus Unit ID = 3)
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start)
SELECT 3, sensor_type, layer_index, position_index,
       REPLACE(description, 'T-101', 'T-103'), 3, modbus_reg_start
FROM sensors WHERE tank_id = 1;

-- T-104 传感器 (Modbus Unit ID = 4)
INSERT INTO sensors (tank_id, sensor_type, layer_index, position_index, description, modbus_unit_id, modbus_reg_start)
SELECT 4, sensor_type, layer_index, position_index,
       REPLACE(description, 'T-101', 'T-104'), 4, modbus_reg_start
FROM sensors WHERE tank_id = 1;
