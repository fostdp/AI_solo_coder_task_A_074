async function showSensorPopup(sensorId, sensorType, x, y) {
    const popup = document.getElementById('sensor-popup');
    const title = document.getElementById('popup-title');
    const info = document.getElementById('popup-info');
    const riskDiv = document.getElementById('popup-risk');

    title.textContent = `${sensorType === 'temperature' ? '温度' : '密度'}传感器 #${sensorId}`;

    try {
        const resp = await fetch(`${App.API_BASE}/api/sensor/${sensorId}?hours=24`);
        const trendData = await resp.json();

        info.innerHTML = `
            <div>传感器ID: ${trendData.sensor_id}</div>
            <div>类型: ${trendData.sensor_type === 'temperature' ? '温度' : '密度'}</div>
            <div>储罐ID: ${trendData.tank_id}</div>
            <div>数据点数: ${trendData.points ? trendData.points.length : 0}</div>
        `;

        const prediction = await fetch(`${App.API_BASE}/api/prediction/${trendData.tank_id}`).then(r => r.json());
        const riskIndex = prediction.risk_index || 0;
        riskDiv.innerHTML = `<strong>翻滚风险指数: ${(riskIndex * 100).toFixed(1)}%</strong>`;
        riskDiv.style.background = riskIndex > 0.7 ? 'rgba(239,68,68,0.15)' :
            riskIndex > 0.4 ? 'rgba(245,158,11,0.15)' : 'rgba(16,185,129,0.15)';
        riskDiv.style.color = riskIndex > 0.7 ? '#ef4444' :
            riskIndex > 0.4 ? '#f59e0b' : '#10b981';

        drawTrendChart(trendData);
    } catch (e) {
        info.innerHTML = '<div style="color:#ef4444">加载趋势数据失败</div>';
        riskDiv.innerHTML = '';
    }

    popup.style.left = Math.min(x, window.innerWidth - 540) + 'px';
    popup.style.top = Math.min(y, window.innerHeight - 420) + 'px';
    popup.classList.remove('hidden');
}

function showDensityPopup(layerIndex, value, x, y) {
    const popup = document.getElementById('sensor-popup');
    const title = document.getElementById('popup-title');
    const info = document.getElementById('popup-info');
    const riskDiv = document.getElementById('popup-risk');

    title.textContent = `密度计 - 第${layerIndex}层`;
    info.innerHTML = `
        <div>层位: L${layerIndex}</div>
        <div>密度: ${value.toFixed(1)} kg/m³</div>
        <div>状态: 正常</div>
    `;
    riskDiv.innerHTML = '';

    popup.style.left = Math.min(x, window.innerWidth - 540) + 'px';
    popup.style.top = Math.min(y, window.innerHeight - 420) + 'px';
    popup.classList.remove('hidden');
}

function showPressurePopup(value, x, y) {
    const popup = document.getElementById('sensor-popup');
    const title = document.getElementById('popup-title');
    const info = document.getElementById('popup-info');
    const riskDiv = document.getElementById('popup-risk');

    title.textContent = '压力变送器 - 罐顶';
    info.innerHTML = `
        <div>当前压力: ${value.toFixed(1)} kPa</div>
        <div>设计压力: 25.0 kPa</div>
        <div>压力比: ${(value / 25.0 * 100).toFixed(1)}%</div>
    `;

    const pressureRatio = value / 25.0;
    riskDiv.innerHTML = `<strong>超压风险: ${pressureRatio > 0.9 ? '高危' : pressureRatio > 0.75 ? '警告' : '正常'}</strong>`;
    riskDiv.style.background = pressureRatio > 0.9 ? 'rgba(239,68,68,0.15)' :
        pressureRatio > 0.75 ? 'rgba(245,158,11,0.15)' : 'rgba(16,185,129,0.15)';
    riskDiv.style.color = pressureRatio > 0.9 ? '#ef4444' :
        pressureRatio > 0.75 ? '#f59e0b' : '#10b981';

    popup.style.left = Math.min(x, window.innerWidth - 540) + 'px';
    popup.style.top = Math.min(y, window.innerHeight - 420) + 'px';
    popup.classList.remove('hidden');
}

function closeSensorPopup() {
    document.getElementById('sensor-popup').classList.add('hidden');
}

function drawTrendChart(trendData) {
    const canvas = document.getElementById('trend-chart');
    const ctx = canvas.getContext('2d');
    const w = canvas.width;
    const h = canvas.height;

    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = '#0a0e1a';
    ctx.fillRect(0, 0, w, h);

    const points = trendData.points || [];
    if (points.length < 2) {
        ctx.fillStyle = '#8899aa';
        ctx.font = '14px sans-serif';
        ctx.textAlign = 'center';
        ctx.fillText('暂无足够数据', w / 2, h / 2);
        return;
    }

    const margin = { top: 20, right: 20, bottom: 30, left: 55 };
    const plotW = w - margin.left - margin.right;
    const plotH = h - margin.top - margin.bottom;

    const values = points.map(p => p.value);
    let minVal = Math.min(...values);
    let maxVal = Math.max(...values);
    const range = maxVal - minVal || 1;
    minVal -= range * 0.1;
    maxVal += range * 0.1;

    ctx.strokeStyle = '#2a3a52';
    ctx.lineWidth = 1;
    ctx.strokeRect(margin.left, margin.top, plotW, plotH);

    ctx.fillStyle = '#8899aa';
    ctx.font = '10px sans-serif';
    ctx.textAlign = 'right';

    for (let i = 0; i <= 4; i++) {
        const val = minVal + (maxVal - minVal) * (1 - i / 4);
        const y = margin.top + (i / 4) * plotH;
        ctx.fillText(val.toFixed(1), margin.left - 4, y + 3);

        ctx.beginPath();
        ctx.moveTo(margin.left, y);
        ctx.lineTo(margin.left + plotW, y);
        ctx.strokeStyle = 'rgba(42,58,82,0.4)';
        ctx.stroke();
    }

    ctx.textAlign = 'center';
    const timeStep = Math.max(1, Math.floor(points.length / 6));
    for (let i = 0; i < points.length; i += timeStep) {
        const x = margin.left + (i / (points.length - 1)) * plotW;
        const time = new Date(points[i].time);
        ctx.fillText(time.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }), x, margin.top + plotH + 16);
    }

    const isTemp = trendData.sensor_type === 'temperature';
    const lineColor = isTemp ? '#06b6d4' : '#8b5cf6';
    const fillColor = isTemp ? 'rgba(6,182,212,0.1)' : 'rgba(139,92,246,0.1)';

    ctx.beginPath();
    ctx.moveTo(margin.left, margin.top + plotH);
    points.forEach((p, i) => {
        const x = margin.left + (i / (points.length - 1)) * plotW;
        const y = margin.top + (1 - (p.value - minVal) / (maxVal - minVal)) * plotH;
        if (i === 0) ctx.lineTo(x, y);
        else ctx.lineTo(x, y);
    });
    ctx.lineTo(margin.left + plotW, margin.top + plotH);
    ctx.closePath();
    ctx.fillStyle = fillColor;
    ctx.fill();

    ctx.beginPath();
    points.forEach((p, i) => {
        const x = margin.left + (i / (points.length - 1)) * plotW;
        const y = margin.top + (1 - (p.value - minVal) / (maxVal - minVal)) * plotH;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
    });
    ctx.strokeStyle = lineColor;
    ctx.lineWidth = 2;
    ctx.stroke();

    const lastPt = points[points.length - 1];
    const lastX = margin.left + plotW;
    const lastY = margin.top + (1 - (lastPt.value - minVal) / (maxVal - minVal)) * plotH;
    ctx.beginPath();
    ctx.arc(lastX, lastY, 4, 0, Math.PI * 2);
    ctx.fillStyle = lineColor;
    ctx.fill();
    ctx.strokeStyle = '#fff';
    ctx.lineWidth = 1.5;
    ctx.stroke();

    ctx.fillStyle = lineColor;
    ctx.font = 'bold 11px sans-serif';
    ctx.textAlign = 'right';
    ctx.fillText(lastPt.value.toFixed(2), margin.left + plotW - 8, lastY - 8);

    ctx.fillStyle = '#e0e7ef';
    ctx.font = 'bold 12px sans-serif';
    ctx.textAlign = 'left';
    const unit = isTemp ? '°C' : 'kg/m³';
    ctx.fillText(`${isTemp ? '温度' : '密度'}趋势 (近24h ${unit})`, margin.left + 4, margin.top - 4);
}
