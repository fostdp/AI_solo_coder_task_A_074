# LNG储罐翻滚预测与安全监控系统

4×160,000m³ 液化天然气储罐实时监测、有限体积法翻滚预测、两级告警与DCS联动系统。

## 架构

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Docker Network: lng-net                       │
│                                                                      │
│  ┌─────────────┐   Modbus TCP    ┌──────────────────┐               │
│  │   Modbus     │ ◄────────────── │   modbus_poller  │               │
│  │  Simulator   │   :5020        │  (优先级调度)     │               │
│  │  :5020/:8090 │                └────────┬─────────┘               │
│  └─────────────┘                         │ sensorBatchCh            │
│                                          ▼                          │
│  ┌─────────────┐                  ┌──────────────────┐              │
│  │  TimescaleDB │ ◄─── COPY ───── │rollover_predictor│              │
│  │   :5432      │     批量写入    │  (FVM求解+风险)  │              │
│  │  超表+压缩   │                └────────┬─────────┘              │
│  │  连续聚合    │                         │ predictionCh            │
│  └──────┬──────┘                         ▼                          │
│         │                        ┌──────────────────┐               │
│         │                        │ alarm_forwarder  │               │
│         │                        │ (告警+OPC UA+BOG)│               │
│         │                        └────┬────────┬────┘               │
│         │                             │        │ alarmEventCh       │
│         │                             ▼        ▼                    │
│         │                    ┌────────────┐  ┌────────────┐         │
│         │                    │ OPC UA Sim │  │  Go API    │         │
│         │                    │  :4840     │  │ :8080/WS   │         │
│         │                    └────────────┘  │ :6060 pprof│         │
│         │                                    │ /metrics   │         │
│         └──────────────────────────────────► └─────┬──────┘         │
│                                                │                  │
└────────────────────────────────────────────────┼──────────────────┘
                                                 │
                              ┌──────────────────┼──────────────────┐
                              │  Browser         ▼                   │
                              │  ┌─────────────────────────────────┐│
                              │  │ tank_3d_viewer.js (Three.js)    ││
                              │  │ risk_dashboard.js (仪表盘+告警) ││
                              │  │ app.js (协调器+等值线)           ││
                              │  └─────────────────────────────────┘│
                              └─────────────────────────────────────┘
```

### 数据流管道

```
Modbus TCP ──→ modbus_poller ──sensorBatchCh──→ rollover_predictor ──predictionCh──→ alarm_forwarder
                   │                                   │                              │
                   ├── 写DB(COPY)                     ├── 写DB                       ├── 写DB
                   └── WS推送                         └── WS推送                     ├── OPC UA推送
                                                                                     └── BOG指令
```

## 技术栈

| 层        | 技术                                              |
|-----------|--------------------------------------------------|
| 数据采集   | Go + simonvetter/modbus, 3级优先级调度            |
| 数值计算   | Go FVM (有限体积法), 欠松弛+自适应CFL+Rayleigh数  |
| 告警联动   | Go, OPC UA推送, BOG自动控制, 待发队列+心跳重连    |
| 时序存储   | TimescaleDB (超表+自动压缩+连续聚合+保留策略)     |
| 三维可视化 | Three.js WebGL, 3级设备质量自适应, 共享几何体      |
| 仪表盘    | Canvas 2D, Marching Squares等值线, 风险仪表盘     |
| 实时推送   | WebSocket, gorilla/websocket                      |
| 可观测性   | pprof (:6060), Prometheus /metrics, Gzip压缩      |
| 容器编排   | Docker Compose (4服务 + bridge网络)               |

## 快速部署

### 前置条件

- Docker 20.10+
- Docker Compose v2+

### 启动全部服务

```bash
cd scripts
docker-compose up -d --build
```

服务启动顺序: TimescaleDB(healthy) → Backend → Modbus Simulator + OPC UA Simulator

### 验证

```bash
# 检查服务状态
docker-compose ps

# 检查后端健康
curl http://localhost:8080/api/tanks

# 检查Prometheus指标
curl http://localhost:8080/metrics

# 检查pprof
curl http://localhost:6060/debug/pprof/

# 打开前端
# 浏览器访问 http://localhost:8080
```

### 端口映射

| 端口   | 服务              | 说明                     |
|--------|-------------------|--------------------------|
| 8080   | Go Backend        | HTTP API + WebSocket + 前端 |
| 6060   | Go Backend        | pprof 性能诊断            |
| 5432   | TimescaleDB       | PostgreSQL               |
| 5020   | Modbus Simulator  | Modbus TCP 数据源         |
| 8090   | Modbus Simulator  | HTTP 注入API              |
| 4840   | OPC UA Simulator  | OPC UA DCS模拟端          |

## Modbus模拟器用法

### 基础状态查询

```bash
# 全局状态
curl http://localhost:8090/api/status

# 单罐快照
curl http://localhost:8090/api/tank/1

# 全部储罐
curl http://localhost:8090/api/tanks
```

### 注入翻滚条件

```bash
# 触发翻滚模拟 (intensity: 0.0~3.0, 越大层间温差增长越快)
curl -X POST http://localhost:8090/api/inject/rollover/1 \
  -H "Content-Type: application/json" \
  -d '{"intensity": 1.5}'

# 停止翻滚
curl -X POST http://localhost:8090/api/inject/stop_rollover/1
```

### 注入温度密度分层

```bash
# 5层温度偏移 + 3密度偏移 (模拟老LNG与新LNG分层)
curl -X POST http://localhost:8090/api/inject/stratification/1 \
  -H "Content-Type: application/json" \
  -d '{"temp_offsets": [0.0, 2.0, 5.0, 8.5, 12.0], "density_offsets": [0.0, 3.0, 5.0]}'

# 清除分层
curl -X POST http://localhost:8090/api/inject/clear_stratification/1
```

### 注入压力和BOG

```bash
# 强制设置压力 (模拟超压)
curl -X POST http://localhost:8090/api/inject/pressure/1 \
  -H "Content-Type: application/json" \
  -d '{"pressure_kpa": 23.5}'

# 强制启动/停止BOG压缩机
curl -X POST http://localhost:8090/api/inject/bog/1 \
  -H "Content-Type: application/json" \
  -d '{"running": true}'

# 清除覆盖
curl -X POST http://localhost:8090/api/inject/clear_pressure/1
curl -X POST http://localhost:8090/api/inject/clear_bog/1
```

### 预设场景

```bash
# 一级翻滚预警场景: 层间温差>8℃ + 密度差>2kg/m³
curl -X POST http://localhost:8090/api/inject/scenario/rollover_level1 \
  -H "Content-Type: application/json" \
  -d '{"tank_id": 1}'

# 二级超压告警场景: 罐压>设计压力90% + BOG启动
curl -X POST http://localhost:8090/api/inject/scenario/rollover_level2 \
  -H "Content-Type: application/json" \
  -d '{"tank_id": 1}'

# 重置所有注入
curl -X POST http://localhost:8090/api/inject/reset
# 或重置单罐
curl -X POST http://localhost:8090/api/inject/reset \
  -d '{"tank_id": 1}'
```

### 环境变量配置

| 变量             | 默认值  | 说明               |
|-----------------|---------|-------------------|
| NUM_TANKS       | 4       | 储罐数量            |
| UPDATE_INTERVAL | 30      | 数据更新间隔(秒)     |
| MODBUS_PORT     | 5020    | Modbus TCP端口      |
| HTTP_API_PORT   | 8090    | 注入API端口          |

## 可观测性

### pprof性能诊断

```bash
# CPU profile (30秒采样)
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# 堆内存
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine泄漏检查
go tool pprof http://localhost:6060/debug/pprof/goroutine

# 执行追踪 (5秒)
curl -o trace.out http://localhost:6060/debug/pprof/trace?seconds=5
go tool trace trace.out
```

### Prometheus指标

```bash
# 文本格式指标
curl http://localhost:8080/metrics
```

| 指标名                        | 类型    | 说明                |
|------------------------------|---------|---------------------|
| lng_http_requests_total       | counter | HTTP请求总数         |
| lng_ws_connections_total      | counter | WebSocket连接总数    |
| lng_alarms_fired_total        | counter | 告警触发总数         |
| lng_poller_cycles_total       | counter | Modbus轮询周期数     |
| lng_predictions_total         | counter | FVM预测计算次数      |
| lng_uptime_seconds            | gauge   | 系统运行时间(秒)     |
| lng_build_info                | gauge   | 构建版本信息         |

### Prometheus配置示例

```yaml
scrape_configs:
  - job_name: 'lng-monitor'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: /metrics
    scrape_interval: 15s
```

## TimescaleDB数据管理

### 自动压缩

7天以上的数据块自动压缩, segmentby按`tank_id`+`layer_index`分割:

| 表                     | 压缩分割键                | 排序        |
|-----------------------|--------------------------|-------------|
| temperature_readings   | tank_id, layer_index     | time DESC   |
| density_readings       | tank_id, layer_index     | time DESC   |
| pressure_readings      | tank_id                  | time DESC   |
| bog_compressor_status  | tank_id                  | time DESC   |
| rollover_predictions   | tank_id                  | time DESC   |

### 保留策略

| 数据类型              | 原始保留  | 聚合保留 |
|----------------------|----------|---------|
| 温度/密度/压力/BOG    | 90天     | -       |
| 翻滚预测              | 365天    | -       |
| 温度小时聚合           | -        | 2年     |
| 密度小时聚合           | -        | 2年     |

### 手动操作

```bash
# 进入数据库
docker exec -it lng-timescaledb psql -U postgres -d lng_monitor

# 查看压缩状态
SELECT hypertable_name, compression_status, before_compression_bytes, after_compression_bytes
FROM timescaledb_information.compression_settings;

# 查看数据量
SELECT hypertable_name, num_chunks FROM timescaledb_information.dimensions;

# 手动触发压缩
SELECT compress_chunk(i) FROM show_chunks('temperature_readings', older_than => INTERVAL '7 days') i;

# 手动清理
SELECT drop_chunks('temperature_readings', older_than => INTERVAL '90 days');
```

## 模型参数配置

所有数值模型参数从JSON配置文件加载: `backend/configs/model_params.json`

| 参数组     | 关键参数                                                  |
|-----------|----------------------------------------------------------|
| FVM       | 层数=5, 欠松弛0.6/0.5, CFL=0.45, 自适应时间步0.1~30s     |
| 告警       | 温差阈值8℃, 密度差2kg/m³, 压力90%, 冷却5min             |
| 轮询       | 关键10s, 全量30s, Modbus超时5s                            |
| OPC UA    | 重连5s, 心跳15s, 3次失败断开, 待发队列100条              |
| 预测       | 权重(0.4/0.4/0.2), 漂移窗口10, 翻滚触发0.3              |

## 项目结构

```
├── backend/
│   ├── cmd/main.go                     # 入口: channel管道连接
│   ├── configs/model_params.json       # 模型参数JSON
│   ├── internal/
│   │   ├── channels/channels.go        # SensorBatch/PredictionOutput/AlarmEvent
│   │   ├── config/
│   │   │   ├── config.go               # 环境变量配置
│   │   │   └── loader.go               # JSON参数加载+默认值
│   │   ├── modbus_poller/poller.go     # 数据采集+3级优先级调度
│   │   ├── rollover_predictor/         # FVM求解+Rayleigh数风险计算
│   │   │   └── predictor.go
│   │   ├── alarm_forwarder/            # 告警评估+OPC UA+BOG指令
│   │   │   └── forwarder.go
│   │   ├── api/
│   │   │   ├── server.go               # HTTP/WS + Gzip中间件
│   │   │   └── metrics.go              # Prometheus + pprof
│   │   ├── database/database.go        # TimescaleDB (pgxpool+COPY)
│   │   └── models/models.go            # 数据模型
│   ├── Dockerfile                      # 多阶段构建→scratch静态二进制
│   └── go.mod
├── frontend/static/
│   ├── index.html
│   ├── css/style.css
│   └── js/
│       ├── tank_3d_viewer.js            # Three.js三维渲染
│       ├── risk_dashboard.js            # 风险仪表盘+告警+趋势图
│       ├── app.js                       # 协调器+等值线+WS
│       └── main.js                      # DOMContentLoaded
└── scripts/
    ├── docker-compose.yml               # 4服务编排
    ├── init_timescaledb.sql             # 超表+压缩+保留+聚合策略
    ├── modbus_simulator.py              # 增强版Modbus模拟器+注入API
    ├── opcua_simulator.py               # OPC UA DCS模拟端
    ├── Dockerfile.sim                   # Modbus模拟器镜像
    └── Dockerfile.opcua                 # OPC UA模拟器镜像
```

## 停止服务

```bash
cd scripts
docker-compose down

# 清除数据卷
docker-compose down -v
```
