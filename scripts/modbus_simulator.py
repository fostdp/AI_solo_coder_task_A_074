"""
LNG储罐 Modbus TCP 模拟器
模拟4座储罐的传感器数据上报, 每30秒更新一次
运行: python modbus_simulator.py
依赖: pip install pymodbus
"""
import struct
import math
import time
import random
import threading
from datetime import datetime

from pymodbus.server import StartTcpServer
from pymodbus.datastore import ModbusSequentialDataBlock, ModbusSlaveContext, ModbusServerContext

NUM_TANKS = 4
NUM_LAYERS = 5
NUM_TEMP_PER_LAYER = 8
NUM_DENSITY = 3
NUM_REGS_PER_TANK = 100

BASE_TEMP = -162.0
BASE_DENSITY = 450.0
BASE_PRESSURE = 15.0

LAYER_TEMP_OFFSETS = [0.0, 0.8, 1.5, 2.5, 3.5]
LAYER_DENSITY_OFFSETS = [0.0, 5.0, 12.0]

rollover_sim_active = False
rollover_tank = -1
sim_tick = 0

def float_to_registers(value):
    bits = struct.pack('>f', value)
    return [struct.unpack('>H', bits[0:2])[0], struct.unpack('>H', bits[2:4])[0]]

def registers_to_float(regs):
    bits = struct.pack('>HH', regs[0], regs[1])
    return struct.unpack('>f', bits)[0]

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
                for i in range(NUM_DENSITY)
            ]
            self.tank_pressures[t] = BASE_PRESSURE + random.gauss(0, 0.3)
            self.tank_bog_running[t] = False

    def update(self):
        global sim_tick
        sim_tick += 1

        with self.lock:
            for t in range(1, NUM_TANKS + 1):
                for i in range(len(self.tank_temps[t])):
                    layer = i // NUM_TEMP_PER_LAYER
                    drift = 0.002 * layer
                    noise = random.gauss(0, 0.05)

                    if rollover_sim_active and t == rollover_tank:
                        drift += 0.01 * (NUM_LAYERS - 1 - layer)

                    self.tank_temps[t][i] += drift + noise
                    self.tank_temps[t][i] = max(-170, min(-150, self.tank_temps[t][i]))

                for i in range(len(self.tank_densities[t])):
                    noise = random.gauss(0, 0.1)
                    drift = 0.01 * (i + 1)

                    if rollover_sim_active and t == rollover_tank:
                        drift += 0.05 * (NUM_DENSITY - i)

                    self.tank_densities[t][i] += drift + noise
                    self.tank_densities[t][i] = max(440, min(470, self.tank_densities[t][i]))

                self.tank_pressures[t] += random.gauss(0.01, 0.05)
                if self.tank_bog_running[t]:
                    self.tank_pressures[t] -= 0.02
                self.tank_pressures[t] = max(10, min(25, self.tank_pressures[t]))

                if self.tank_pressures[t] > 22.0:
                    self.tank_bog_running[t] = True
                elif self.tank_pressures[t] < 16.0:
                    self.tank_bog_running[t] = False

        if sim_tick % 20 == 0:
            print(f"[{datetime.now().strftime('%H:%M:%S')}] 数据已更新 "
                  f"T-101: 压力={self.tank_pressures[1]:.1f}kPa "
                  f"BOG={'运行' if self.tank_bog_running[1] else '停止'}")

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
            time.sleep(30)
            sim.update()

            for unit_id in range(1, NUM_TANKS + 1):
                regs = sim.build_registers(unit_id)
                for i, val in enumerate(regs):
                    context[unit_id].setValues(3, i, [val])

    update_thread = threading.Thread(target=update_loop, daemon=True)
    update_thread.start()

    print("=" * 60)
    print("  LNG储罐 Modbus TCP 模拟器")
    print("  监听端口: 5020")
    print("  储罐数量: 4")
    print("  数据更新: 每30秒")
    print("=" * 60)
    print()
    print("可用命令:")
    print("  r <tank_id>  - 触发翻滚模拟 (1-4)")
    print("  s <tank_id>  - 停止翻滚模拟")
    print("  q            - 退出")
    print()

    StartTcpServer(context=context, address=("0.0.0.0", 5020))


if __name__ == "__main__":
    run_simulator()
