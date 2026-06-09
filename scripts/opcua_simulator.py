"""
OPC UA 服务器模拟器
模拟DCS接收端, 接收告警推送并确认

运行: python opcua_simulator.py
依赖: pip install asyncua

环境变量:
  OPCUA_PORT - OPC UA端口 (默认4840)
"""

import asyncio
import os
import json
import logging
from datetime import datetime

logging.basicConfig(level=logging.INFO, format='[%(asctime)s] %(levelname)s %(message)s')
logger = logging.getLogger('opcua-sim')

OPCUA_PORT = int(os.environ.get("OPCUA_PORT", "4840"))
NUM_TANKS = 4


async def main():
    from asyncua import Server

    server = Server()
    await server.init()
    server.set_endpoint(f"opc.tcp://0.0.0.0:{OPCUA_PORT}/LNG/DCS")
    server.set_server_name("LNG DCS Simulator")

    uri = "urn:lng-monitor:dcs"
    idx = await server.register_namespace(uri)

    myobj = await server.nodes.objects.add_object(idx, "LNGTankFarm")

    tank_nodes = {}
    for t in range(1, NUM_TANKS + 1):
        tank_code = f"T-10{t}"
        tank_obj = await myobj.add_object(idx, tank_code)

        alarm_obj = await tank_obj.add_object(idx, "Alarm")

        level1 = await alarm_obj.add_variable(idx, "Level1", False)
        await level1.set_writable()

        level2 = await alarm_obj.add_variable(idx, "Level2", False)
        await level2.set_writable()

        alarm_type = await alarm_obj.add_variable(idx, "Type", "")
        await alarm_type.set_writable()

        alarm_msg = await alarm_obj.add_variable(idx, "Message", "")
        await alarm_msg.set_writable()

        alarm_time = await alarm_obj.add_variable(idx, "Timestamp", "")
        await alarm_time.set_writable()

        bog_obj = await tank_obj.add_object(idx, "BOG")

        bog_command_start = await bog_obj.add_variable(idx, "CommandStart", False)
        await bog_command_start.set_writable()

        bog_command_speed = await bog_obj.add_variable(idx, "CommandSpeed", 0.0)
        await bog_command_speed.set_writable()

        bog_status_running = await bog_obj.add_variable(idx, "Running", False)
        await bog_status_running.set_writable()

        tank_nodes[t] = {
            "level1": level1,
            "level2": level2,
            "alarm_type": alarm_type,
            "alarm_msg": alarm_msg,
            "alarm_time": alarm_time,
            "bog_start": bog_command_start,
            "bog_speed": bog_command_speed,
            "bog_running": bog_status_running,
        }

    async def on_bog_start(datachange):
        node = datachange[0].node
        val = datachange[0].value.Value.Value
        for t, nodes in tank_nodes.items():
            if nodes["bog_start"] == node:
                await nodes["bog_running"].write_value(val)
                logger.info(f"  T-10{t} BOG command: {'START' if val else 'STOP'} → running={val}")

    for t, nodes in tank_nodes.items():
        await server.subscribe_data_change(nodes["bog_start"], on_bog_start)

    async def on_alarm(datachange):
        node = datachange[0].node
        val = datachange[0].value.Value.Value
        for t, nodes in tank_nodes.items():
            if nodes["level1"] == node or nodes["level2"] == node:
                level = "Level1" if nodes["level1"] == node else "Level2"
                logger.info(f"  T-10{t} ALARM {level} = {val}")
                if val:
                    msg = await nodes["alarm_msg"].read_value()
                    atype = await nodes["alarm_type"].read_value()
                    logger.info(f"  → DCS确认: T-10{t} {level} {atype}: {msg}")

    for t, nodes in tank_nodes.items():
        await server.subscribe_data_change(nodes["level1"], on_alarm)
        await server.subscribe_data_change(nodes["level2"], on_alarm)

    logger.info("=" * 60)
    logger.info("  OPC UA DCS 模拟器")
    logger.info(f"  端点: opc.tcp://0.0.0.0:{OPCUA_PORT}/LNG/DCS")
    logger.info(f"  储罐: {NUM_TANKS}")
    logger.info("  节点: ns=2;s=T-10N.Alarm.Level1/Level2/Type/Message")
    logger.info("        ns=2;s=T-10N.BOG.CommandStart/CommandSpeed/Running")
    logger.info("=" * 60)

    async with server:
        while True:
            await asyncio.sleep(10)


if __name__ == "__main__":
    asyncio.run(main())
