"""
LNG储罐 Modbus TCP 模拟器
模拟4座储罐×43个传感器的数据上报, 每30秒更新一次
支持HTTP注入API触发温度密度分层和翻滚条件

运行: python modbus_simulator.py
依赖: pip install pymodbus flask

环境变量:
  MODBUS_PORT      - Modbus TCP端口 (默认5020)
  HTTP_API_PORT    - 注入API端口 (默认8090)
  NUM_TANKS        - 储罐数量 (默认4)
  UPDATE_INTERVAL  - 数据更新间隔秒 (默认30)
"""

import struct
import math
import time
import random
import threading
import json
import os
from datetime import datetime

from pymodbus.server import StartTcpServer
from pymodbus.datastore import ModbusSequentialDataBlock, ModbusSlaveContext, ModbusServerContext

NUM_TANKS = int(os.environ.get("NUM_TANKS", "4"))
NUM_LAYERS = 5
NUM_TEMP_PER_LAYER = 8
NUM_DENSITY_PER_TANK = 3
NUM_PRESSURE_PER_TANK = 1
NUM_BOG_PER_TANK = 1
NUM_REGS_PER_TANK = 100
UPDATE_INTERVAL = int(os.environ.get("UPDATE_INTERVAL", "30"))

MODBUS_PORT = int(os.environ.get("MODBUS_PORT", "5020"))
HTTP_API_PORT = int(os.environ.get("HTTP_API_PORT", "8090"))

BASE_TEMP = -162.0
BASE_DENSITY = 450.0
BASE_PRESSURE = 15.0

LAYER_TEMP_OFFSETS = [0.0, 0.8, 1.5, 2.5, 3.5]
LAYER_DENSITY_OFFSETS = [0.0, 5.0, 12.0]


def float_to_registers(value):
    bits = struct.pack('>f', value)
    return [struct.unpack('>H', bits[0:2])[0], struct.unpack('>H', bits[2:4])[0]]


def registers_to_float(regs):
    bits = struct.pack('>HH', regs[0], regs[1])
    return struct.unpack('>f', bits)[0]


class InjectionState:
    def __init__(self):
        self.lock = threading.Lock()
        self.tank_states = {}
        for t in range(1, NUM_TANKS + 1):
            self.tank_states[t] = {
                "rollover_active": False,
                "rollover_intensity": 0.0,
                "stratification_temp": [0.0] * NUM_LAYERS,
                "stratification_density": [0.0] * NUM_DENSITY_PER_TANK,
                "pressure_override": None,
                "bog_override": None,
            }

    def trigger_rollover(self, tank_id, intensity=1.0):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["rollover_active"] = True
                st["rollover_intensity"] = min(max(intensity, 0.0), 3.0)

    def stop_rollover(self, tank_id):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["rollover_active"] = False
                st["rollover_intensity"] = 0.0

    def inject_stratification(self, tank_id, temp_offsets=None, density_offsets=None):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st and temp_offsets:
                for i, v in enumerate(temp_offsets):
                    if i < NUM_LAYERS:
                        st["stratification_temp"][i] = v
            if st and density_offsets:
                for i, v in enumerate(density_offsets):
                    if i < NUM_DENSITY_PER_TANK:
                        st["stratification_density"][i] = v

    def clear_stratification(self, tank_id):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["stratification_temp"] = [0.0] * NUM_LAYERS
                st["stratification_density"] = [0.0] * NUM_DENSITY_PER_TANK

    def inject_pressure(self, tank_id, pressure_kpa):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["pressure_override"] = pressure_kpa

    def clear_pressure(self, tank_id):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["pressure_override"] = None

    def inject_bog(self, tank_id, running):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["bog_override"] = running

    def clear_bog(self, tank_id):
        with self.lock:
            st = self.tank_states.get(tank_id)
            if st:
                st["bog_override"] = None

    def get_state(self):
        with self.lock:
            return {
                tid: dict(st) for tid, st in self.tank_states.items()
            }


injection = InjectionState()
sim_tick = 0


class LNGDataSimulator:
    def __init__(self):
        self.tank_temps = {}
        self.tank_densities = {}
        self.tank_pressures = {}
        self.tank_bog_running = {}
        self.lock = threading.Lock()

        for t in range(1, NUM_TANKS + 1):
            self.tank_temps[t] = [
                BASE_TEMP + LAYER_TEMP_OFFSETS[l] + random.gauss(0, 0.2)
                for l in range(NUM_LAYERS)
                for _ in range(NUM_TEMP_PER_LAYER)
            ]
            self.tank_densities[t] = [
                BASE_DENSITY + LAYER_DENSITY_OFFSETS[i] + random.gauss(0, 0.5)
                for i in range(NUM_DENSITY_PER_TANK)
            ]
            self.tank_pressures[t] = BASE_PRESSURE + random.gauss(0, 0.3)
            self.tank_bog_running[t] = False

    def update(self):
        global sim_tick
        sim_tick += 1

        with self.lock:
            inj_snapshot = injection.get_state()

            for t in range(1, NUM_TANKS + 1):
                inj = inj_snapshot.get(t, {})

                for i in range(len(self.tank_temps[t])):
                    layer = i // NUM_TEMP_PER_LAYER
                    drift = 0.002 * layer
                    noise = random.gauss(0, 0.05)

                    strat_offset = 0.0
                    if inj.get("stratification_temp"):
                        strat_offset = inj["stratification_temp"][layer]

                    if inj.get("rollover_active"):
                        intensity = inj.get("rollover_intensity", 1.0)
                        drift += 0.01 * intensity * (NUM_LAYERS - 1 - layer)

                    self.tank_temps[t][i] += drift + noise + strat_offset * 0.01
                    self.tank_temps[t][i] = max(-170, min(-150, self.tank_temps[t][i]))

                for i in range(len(self.tank_densities[t])):
                    noise = random.gauss(0, 0.1)
                    drift = 0.01 * (i + 1)

                    strat_offset = 0.0
                    if inj.get("stratification_density"):
                        strat_offset = inj["stratification_density"][i]

                    if inj.get("rollover_active"):
                        intensity = inj.get("rollover_intensity", 1.0)
                        drift += 0.05 * intensity * (NUM_DENSITY_PER_TANK - i)

                    self.tank_densities[t][i] += drift + noise + strat_offset * 0.01
                    self.tank_densities[t][i] = max(440, min(470, self.tank_densities[t][i]))

                if inj.get("pressure_override") is not None:
                    self.tank_pressures[t] = inj["pressure_override"]
                else:
                    self.tank_pressures[t] += random.gauss(0.01, 0.05)
                    if self.tank_bog_running[t]:
                        self.tank_pressures[t] -= 0.02
                    self.tank_pressures[t] = max(10, min(25, self.tank_pressures[t]))

                if inj.get("bog_override") is not None:
                    self.tank_bog_running[t] = inj["bog_override"]
                else:
                    if self.tank_pressures[t] > 22.0:
                        self.tank_bog_running[t] = True
                    elif self.tank_pressures[t] < 16.0:
                        self.tank_bog_running[t] = False

        if sim_tick % 10 == 0:
            tank_status = "  ".join(
                f"T-10{t}: P={self.tank_pressures[t]:.1f}kPa BOG={'R' if self.tank_bog_running[t] else 'S'}"
                for t in range(1, NUM_TANKS + 1)
            )
            print(f"[{datetime.now().strftime('%H:%M:%S')}] tick={sim_tick} {tank_status}")

    def build_registers(self, tank_id):
        regs = [0] * NUM_REGS_PER_TANK

        with self.lock:
            temps = self.tank_temps[tank_id]
            for i, temp in enumerate(temps):
                reg_idx = i * 2
                if reg_idx + 1 < len(regs):
                    r = float_to_registers(temp)
                    regs[reg_idx] = r[0]
                    regs[reg_idx + 1] = r[1]

            for i, dens in enumerate(self.tank_densities[tank_id]):
                reg_idx = 80 + i * 2
                if reg_idx + 1 < len(regs):
                    r = float_to_registers(dens)
                    regs[reg_idx] = r[0]
                    regs[reg_idx + 1] = r[1]

            r = float_to_registers(self.tank_pressures[tank_id])
            regs[86] = r[0]
            regs[87] = r[1]

            bog_val = 0.0
            if self.tank_bog_running[tank_id]:
                bog_val = 1.0
            r = float_to_registers(bog_val)
            regs[88] = r[0]
            regs[89] = r[1]

        return regs

    def get_snapshot(self, tank_id):
        with self.lock:
            temps = list(self.tank_temps[tank_id])
            densities = list(self.tank_densities[tank_id])
            pressure = self.tank_pressures[tank_id]
            bog = self.tank_bog_running[tank_id]

        layer_temps = []
        for l in range(NUM_LAYERS):
            layer_vals = temps[l * NUM_TEMP_PER_LAYER:(l + 1) * NUM_TEMP_PER_LAYER]
            layer_temps.append({
                "layer": l + 1,
                "avg": sum(layer_vals) / len(layer_vals),
                "min": min(layer_vals),
                "max": max(layer_vals),
            })

        return {
            "tank_id": tank_id,
            "temperatures": layer_temps,
            "densities": [{"layer_idx": i + 1, "value": densities[i]} for i in range(len(densities))],
            "pressure_kpa": pressure,
            "bog_running": bog,
        }


def run_http_api(sim):
    from http.server import HTTPServer, BaseHTTPRequestHandler

    class APIHandler(BaseHTTPRequestHandler):
        def log_message(self, format, *args):
            pass

        def _send_json(self, data, code=200):
            body = json.dumps(data, ensure_ascii=False, indent=2).encode("utf-8")
            self.send_response(code)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _read_body(self):
            length = int(self.headers.get("Content-Length", 0))
            if length > 0:
                return json.loads(self.rfile.read(length))
            return {}

        def do_GET(self):
            path = self.path.rstrip("/")

            if path == "/api/status":
                result = {
                    "num_tanks": NUM_TANKS,
                    "sensors_per_tank": NUM_LAYERS * NUM_TEMP_PER_LAYER + NUM_DENSITY_PER_TANK + NUM_PRESSURE_PER_TANK + NUM_BOG_PER_TANK,
                    "update_interval_sec": UPDATE_INTERVAL,
                    "tick": sim_tick,
                    "injection": injection.get_state(),
                }
                self._send_json(result)
                return

            if path.startswith("/api/tank/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                if tank_id < 1 or tank_id > NUM_TANKS:
                    self._send_json({"error": f"tank_id must be 1-{NUM_TANKS}"}, 404)
                    return
                self._send_json(sim.get_snapshot(tank_id))
                return

            if path == "/api/tanks":
                self._send_json([sim.get_snapshot(t) for t in range(1, NUM_TANKS + 1)])
                return

            self._send_json({"error": "not found"}, 404)

        def do_POST(self):
            path = self.path.rstrip("/")
            body = self._read_body()

            if path.startswith("/api/inject/rollover/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                if tank_id < 1 or tank_id > NUM_TANKS:
                    self._send_json({"error": f"tank_id must be 1-{NUM_TANKS}"}, 404)
                    return
                intensity = body.get("intensity", 1.0)
                injection.trigger_rollover(tank_id, intensity)
                self._send_json({"status": "rollover_triggered", "tank_id": tank_id, "intensity": intensity})
                print(f"[INJECT] 翻滚触发 tank={tank_id} intensity={intensity}")
                return

            if path.startswith("/api/inject/stop_rollover/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                injection.stop_rollover(tank_id)
                self._send_json({"status": "rollover_stopped", "tank_id": tank_id})
                print(f"[INJECT] 翻滚停止 tank={tank_id}")
                return

            if path.startswith("/api/inject/stratification/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                temp_offsets = body.get("temp_offsets", [0.0] * NUM_LAYERS)
                density_offsets = body.get("density_offsets", [0.0] * NUM_DENSITY_PER_TANK)
                injection.inject_stratification(tank_id, temp_offsets, density_offsets)
                self._send_json({
                    "status": "stratification_injected",
                    "tank_id": tank_id,
                    "temp_offsets": temp_offsets,
                    "density_offsets": density_offsets,
                })
                print(f"[INJECT] 分层注入 tank={tank_id} temp={temp_offsets} dens={density_offsets}")
                return

            if path.startswith("/api/inject/clear_stratification/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                injection.clear_stratification(tank_id)
                self._send_json({"status": "stratification_cleared", "tank_id": tank_id})
                return

            if path.startswith("/api/inject/pressure/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                pressure = body.get("pressure_kpa")
                if pressure is None:
                    self._send_json({"error": "pressure_kpa required"}, 400)
                    return
                injection.inject_pressure(tank_id, pressure)
                self._send_json({"status": "pressure_injected", "tank_id": tank_id, "pressure_kpa": pressure})
                print(f"[INJECT] 压力注入 tank={tank_id} pressure={pressure}kPa")
                return

            if path.startswith("/api/inject/clear_pressure/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                injection.clear_pressure(tank_id)
                self._send_json({"status": "pressure_cleared", "tank_id": tank_id})
                return

            if path.startswith("/api/inject/bog/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                running = body.get("running", True)
                injection.inject_bog(tank_id, running)
                self._send_json({"status": "bog_injected", "tank_id": tank_id, "running": running})
                print(f"[INJECT] BOG注入 tank={tank_id} running={running}")
                return

            if path.startswith("/api/inject/clear_bog/"):
                try:
                    tank_id = int(path.split("/")[-1])
                except ValueError:
                    self._send_json({"error": "invalid tank_id"}, 400)
                    return
                injection.clear_bog(tank_id)
                self._send_json({"status": "bog_cleared", "tank_id": tank_id})
                return

            if path == "/api/inject/scenario/rollover_level1":
                tank_id = body.get("tank_id", 1)
                injection.inject_stratification(tank_id,
                    temp_offsets=[0.0, 2.0, 5.0, 8.5, 12.0],
                    density_offsets=[0.0, 3.0, 5.0])
                injection.trigger_rollover(tank_id, 0.8)
                self._send_json({"status": "scenario_rollover_level1", "tank_id": tank_id,
                                 "description": "层间温差>8℃ 密度差>2kg/m³ 触发一级翻滚预警"})
                print(f"[SCENARIO] 一级翻滚预警 tank={tank_id}")
                return

            if path == "/api/inject/scenario/rollover_level2":
                tank_id = body.get("tank_id", 1)
                injection.inject_pressure(tank_id, 23.5)
                injection.inject_bog(tank_id, True)
                self._send_json({"status": "scenario_rollover_level2", "tank_id": tank_id,
                                 "description": "罐压>设计压力90% 触发二级超压告警+BOG自动启动"})
                print(f"[SCENARIO] 二级超压告警 tank={tank_id}")
                return

            if path == "/api/inject/reset":
                tank_id = body.get("tank_id")
                if tank_id:
                    injection.stop_rollover(tank_id)
                    injection.clear_stratification(tank_id)
                    injection.clear_pressure(tank_id)
                    injection.clear_bog(tank_id)
                else:
                    for tid in range(1, NUM_TANKS + 1):
                        injection.stop_rollover(tid)
                        injection.clear_stratification(tid)
                        injection.clear_pressure(tid)
                        injection.clear_bog(tid)
                self._send_json({"status": "reset", "tank_id": tank_id or "all"})
                print(f"[INJECT] 重置 tank={tank_id or 'all'}")
                return

            self._send_json({"error": "not found"}, 404)

    server = HTTPServer(("0.0.0.0", HTTP_API_PORT), APIHandler)
    print(f"  注入API: http://0.0.0.0:{HTTP_API_PORT}")
    server.serve_forever()


def run_simulator():
    sim = LNGDataSimulator()

    slaves = {}
    for unit_id in range(1, NUM_TANKS + 1):
        regs = sim.build_registers(unit_id)
        hr_block = ModbusSequentialDataBlock(0, regs)
        slaves[unit_id] = ModbusSlaveContext(hr=hr_block, zero_mode=True)

    context = ModbusServerContext(slaves=slaves, single=False)

    def update_loop():
        while True:
            time.sleep(UPDATE_INTERVAL)
            sim.update()

            for unit_id in range(1, NUM_TANKS + 1):
                regs = sim.build_registers(unit_id)
                for i, val in enumerate(regs):
                    context[unit_id].setValues(3, i, [val])

    update_thread = threading.Thread(target=update_loop, daemon=True)
    update_thread.start()

    api_thread = threading.Thread(target=run_http_api, args=(sim,), daemon=True)
    api_thread.start()

    print("=" * 60)
    print("  LNG储罐 Modbus TCP 模拟器 (增强版)")
    print(f"  储罐数量: {NUM_TANKS}")
    print(f"  每罐传感器: {NUM_LAYERS * NUM_TEMP_PER_LAYER}温度 + {NUM_DENSITY_PER_TANK}密度 + {NUM_PRESSURE_PER_TANK}压力 + {NUM_BOG_PER_TANK}BOG = {NUM_LAYERS * NUM_TEMP_PER_LAYER + NUM_DENSITY_PER_TANK + NUM_PRESSURE_PER_TANK + NUM_BOG_PER_TANK}")
    print(f"  Modbus端口: {MODBUS_PORT}")
    print(f"  注入API端口: {HTTP_API_PORT}")
    print(f"  更新间隔: {UPDATE_INTERVAL}秒")
    print("=" * 60)
    print()
    print("注入API用法:")
    print(f"  GET  /api/status                              - 全局状态")
    print(f"  GET  /api/tank/{{id}}                          - 单罐快照")
    print(f"  GET  /api/tanks                               - 全部储罐快照")
    print(f"  POST /api/inject/rollover/{{id}}               - 触发翻滚 {{\"intensity\": 1.0}}")
    print(f"  POST /api/inject/stop_rollover/{{id}}          - 停止翻滚")
    print(f"  POST /api/inject/stratification/{{id}}         - 注入分层 {{\"temp_offsets\":[0,2,5,8,12], \"density_offsets\":[0,3,5]}}")
    print(f"  POST /api/inject/clear_stratification/{{id}}   - 清除分层")
    print(f"  POST /api/inject/pressure/{{id}}               - 注入压力 {{\"pressure_kpa\": 23.5}}")
    print(f"  POST /api/inject/clear_pressure/{{id}}         - 清除压力")
    print(f"  POST /api/inject/bog/{{id}}                    - 注入BOG {{\"running\": true}}")
    print(f"  POST /api/inject/clear_bog/{{id}}              - 清除BOG")
    print(f"  POST /api/inject/scenario/rollover_level1      - 一级翻滚预警场景 {{\"tank_id\": 1}}")
    print(f"  POST /api/inject/scenario/rollover_level2      - 二级超压告警场景 {{\"tank_id\": 1}}")
    print(f"  POST /api/inject/reset                        - 重置 {{\"tank_id\": 1}} 或空=全部")
    print()

    StartTcpServer(context=context, address=("0.0.0.0", MODBUS_PORT))


if __name__ == "__main__":
    run_simulator()
