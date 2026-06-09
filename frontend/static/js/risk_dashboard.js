const RiskDashboard = {
    riskGaugeCanvas: null,
    riskGaugeCtx: null,
    dataPanel: null,
    alarmBanner: null,
    alarmAudioCtx: null,
    alarmCount: 0,

    init() {
        this.riskGaugeCanvas = document.getElementById('risk-gauge');
        if (this.riskGaugeCanvas) {
            this.riskGaugeCtx = this.riskGaugeCanvas.getContext('2d');
        }
        this.dataPanel = document.getElementById('data-panel');
        this.alarmBanner = document.getElementById('alarm-banner');

        this._startGaugeAnimation();
    },

    updateRiskGauge(riskIndex) {
        if (!this.riskGaugeCtx) return;
        const ctx = this.riskGaugeCtx;
        const w = this.riskGaugeCanvas.width;
        const h = this.riskGaugeCanvas.height;
        const cx = w / 2;
        const cy = h - 20;
        const radius = Math.min(cx, cy) - 20;

        ctx.clearRect(0, 0, w, h);

        const arcStart = Math.PI;
        const arcEnd = 2 * Math.PI;

        const gradient = ctx.createLinearGradient(cx - radius, cy, cx + radius, cy);
        gradient.addColorStop(0, '#06b6d4');
        gradient.addColorStop(0.4, '#22c55e');
        gradient.addColorStop(0.7, '#eab308');
        gradient.addColorStop(1, '#ef4444');

        ctx.beginPath();
        ctx.arc(cx, cy, radius, arcStart, arcEnd);
        ctx.lineWidth = 16;
        ctx.strokeStyle = gradient;
        ctx.lineCap = 'round';
        ctx.stroke();

        ctx.beginPath();
        ctx.arc(cx, cy, radius, arcStart, arcStart + (arcEnd - arcStart) * riskIndex);
        ctx.lineWidth = 16;
        ctx.strokeStyle = this._riskToColor(riskIndex);
        ctx.lineCap = 'round';
        ctx.stroke();

        ctx.fillStyle = '#e2e8f0';
        ctx.font = 'bold 36px "Segoe UI", sans-serif';
        ctx.textAlign = 'center';
        ctx.fillText((riskIndex * 100).toFixed(1) + '%', cx, cy - 30);

        ctx.fillStyle = '#94a3b8';
        ctx.font = '14px "Segoe UI", sans-serif';
        ctx.fillText('翻滚风险指数', cx, cy - 5);

        ctx.fillStyle = '#64748b';
        ctx.font = '11px "Segoe UI", sans-serif';
        ctx.textAlign = 'left';
        ctx.fillText('安全', cx - radius, cy + 25);
        ctx.textAlign = 'right';
        ctx.fillText('危险', cx + radius, cy + 25);
    },

    updateDataPanel(data) {
        if (!this.dataPanel) return;
        const sections = [];

        if (data.pressure_kpa !== undefined) {
            const pressurePct = data.design_pressure ? (data.pressure_kpa / data.design_pressure * 100) : 0;
            const pressureColor = pressurePct > 90 ? '#ef4444' : pressurePct > 75 ? '#eab308' : '#06b6d4';
            sections.push(`
                <div class="panel-section">
                    <h4>罐压状态</h4>
                    <div class="data-row"><span>当前压力</span><span style="color:${pressureColor}">${data.pressure_kpa.toFixed(1)} kPa</span></div>
                    <div class="data-row"><span>设计压力</span><span>${data.design_pressure ? data.design_pressure.toFixed(0) : '-'} kPa</span></div>
                    <div class="data-row"><span>压力占比</span><span style="color:${pressureColor}">${pressurePct.toFixed(1)}%</span></div>
                </div>
            `);
        }

        if (data.bog_compressors && data.bog_compressors.length > 0) {
            const bogRows = data.bog_compressors.map(c => {
                const status = c.running ? '运行' : '停止';
                const color = c.running ? '#22c55e' : '#ef4444';
                return `<div class="data-row"><span>BOG-${c.compressor_id}</span><span style="color:${color}">${status} ${c.running ? c.speed_rpm.toFixed(0) + 'RPM' : ''}</span></div>`;
            }).join('');
            sections.push(`<div class="panel-section"><h4>BOG压缩机</h4>${bogRows}</div>`);
        }

        if (data.temperatures && data.temperatures.length > 0) {
            const tempRows = data.temperatures.map(l =>
                `<div class="data-row"><span>层${l.layer_index}</span><span>${l.avg_temp.toFixed(2)}℃</span></div>`
            ).join('');
            sections.push(`<div class="panel-section"><h4>层均温度</h4>${tempRows}</div>`);
        }

        if (data.densities && data.densities.length > 0) {
            const densRows = data.densities.map(d =>
                `<div class="data-row"><span>层${d.layer_index}</span><span>${d.value_kg_m3.toFixed(1)}kg/m³</span></div>`
            ).join('');
            sections.push(`<div class="panel-section"><h4>密度分布</h4>${densRows}</div>`);
        }

        if (data.prediction) {
            const p = data.prediction;
            const riskColor = this._riskToColor(p.risk_index);
            const rolloverTime = p.predicted_rollover_time ? new Date(p.predicted_rollover_time).toLocaleString('zh-CN') : '暂无预测';
            sections.push(`
                <div class="panel-section">
                    <h4>翻滚预测</h4>
                    <div class="data-row"><span>风险指数</span><span style="color:${riskColor}">${(p.risk_index * 100).toFixed(1)}%</span></div>
                    <div class="data-row"><span>稳定性评分</span><span>${(p.stability * 100).toFixed(1)}%</span></div>
                    <div class="data-row"><span>预测翻滚时间</span><span>${rolloverTime}</span></div>
                    <div class="data-row"><span>最大温度梯度</span><span>${p.max_temp_gradient.toFixed(2)}℃</span></div>
                    <div class="data-row"><span>最大密度梯度</span><span>${p.max_density_gradient.toFixed(2)}kg/m³</span></div>
                </div>
            `);
        }

        this.dataPanel.innerHTML = sections.join('');
    },

    showAlarm(alarm) {
        this.alarmCount++;
        const levelColor = alarm.alarm_level === 1 ? '#f59e0b' : '#ef4444';
        const levelText = alarm.alarm_level === 1 ? '一级预警' : '二级告警';
        const icon = alarm.alarm_level === 1 ? '⚠️' : '🔴';

        if (this.alarmBanner) {
            this.alarmBanner.innerHTML = `<span>${icon} [${levelText}] ${alarm.message}</span>`;
            this.alarmBanner.style.borderColor = levelColor;
            this.alarmBanner.style.display = 'block';
            this.alarmBanner.classList.add('alarm-flash');

            setTimeout(() => {
                this.alarmBanner.style.display = 'none';
                this.alarmBanner.classList.remove('alarm-flash');
            }, 10000);
        }

        this._playAlarmSound(alarm.alarm_level);

        const badge = document.getElementById('alarm-count');
        if (badge) badge.textContent = this.alarmCount;
    },

    _playAlarmSound(level) {
        try {
            if (!this.alarmAudioCtx) {
                this.alarmAudioCtx = new (window.AudioContext || window.webkitAudioContext)();
            }
            const osc = this.alarmAudioCtx.createOscillator();
            const gain = this.alarmAudioCtx.createGain();
            osc.connect(gain);
            gain.connect(this.alarmAudioCtx.destination);
            osc.frequency.value = level === 1 ? 800 : 1200;
            osc.type = 'sine';
            gain.gain.setValueAtTime(0.3, this.alarmAudioCtx.currentTime);
            gain.gain.exponentialRampToValueAtTime(0.01, this.alarmAudioCtx.currentTime + 0.5);
            osc.start();
            osc.stop(this.alarmAudioCtx.currentTime + 0.5);
        } catch (e) { }
    },

    _riskToColor(risk) {
        if (risk < 0.3) return '#06b6d4';
        if (risk < 0.5) return '#22c55e';
        if (risk < 0.7) return '#eab308';
        if (risk < 0.85) return '#f97316';
        return '#ef4444';
    },

    _startGaugeAnimation() {
        this.updateRiskGauge(0);
    },

    showSensorPopup(sensorData, x, y) {
        const popup = document.getElementById('sensor-popup');
        if (!popup) return;

        const title = sensorData.sensorType === 'temperature'
            ? `温度传感器 L${sensorData.layerIndex}-P${sensorData.positionIndex}`
            : sensorData.sensorType === 'density'
                ? `密度传感器 层${sensorData.layerIndex}`
                : '压力变送器';

        popup.querySelector('.popup-title').textContent = title;
        popup.querySelector('.popup-value').textContent = sensorData.value !== undefined
            ? (sensorData.sensorType === 'temperature' ? sensorData.value.toFixed(2) + '℃'
                : sensorData.sensorType === 'density' ? sensorData.value.toFixed(1) + 'kg/m³'
                    : sensorData.value.toFixed(1) + 'kPa')
            : '-';

        popup.style.display = 'block';
        popup.style.left = Math.min(x + 10, window.innerWidth - 320) + 'px';
        popup.style.top = Math.min(y + 10, window.innerHeight - 400) + 'px';

        this._loadSensorChart(sensorData);
    },

    hideSensorPopup() {
        const popup = document.getElementById('sensor-popup');
        if (popup) popup.style.display = 'none';
    },

    async _loadSensorChart(sensorData) {
        const chartCanvas = document.getElementById('sensor-chart');
        if (!chartCanvas || !sensorData.sensorId) return;

        const ctx = chartCanvas.getContext('2d');
        try {
            const resp = await fetch(`/api/sensor/${sensorData.sensorId}?hours=24`);
            const data = await resp.json();
            this._drawTrendChart(ctx, chartCanvas.width, chartCanvas.height, data.trend || []);
        } catch (e) {
            ctx.clearRect(0, 0, chartCanvas.width, chartCanvas.height);
            ctx.fillStyle = '#64748b';
            ctx.textAlign = 'center';
            ctx.fillText('数据加载失败', chartCanvas.width / 2, chartCanvas.height / 2);
        }
    },

    _drawTrendChart(ctx, w, h, data) {
        ctx.clearRect(0, 0, w, h);
        if (!data || data.length === 0) {
            ctx.fillStyle = '#64748b';
            ctx.textAlign = 'center';
            ctx.fillText('暂无趋势数据', w / 2, h / 2);
            return;
        }

        const pad = { top: 20, right: 15, bottom: 30, left: 50 };
        const cw = w - pad.left - pad.right;
        const ch = h - pad.top - pad.bottom;

        const vals = data.map(d => d.value);
        const minV = Math.min(...vals) - 0.5;
        const maxV = Math.max(...vals) + 0.5;

        ctx.strokeStyle = '#334155';
        ctx.lineWidth = 0.5;
        for (let i = 0; i <= 4; i++) {
            const y = pad.top + ch * i / 4;
            ctx.beginPath(); ctx.moveTo(pad.left, y); ctx.lineTo(pad.left + cw, y); ctx.stroke();
            ctx.fillStyle = '#64748b';
            ctx.textAlign = 'right';
            ctx.font = '10px sans-serif';
            ctx.fillText((maxV - (maxV - minV) * i / 4).toFixed(1), pad.left - 5, y + 3);
        }

        const gradient = ctx.createLinearGradient(0, pad.top, 0, pad.top + ch);
        gradient.addColorStop(0, 'rgba(6,182,212,0.3)');
        gradient.addColorStop(1, 'rgba(6,182,212,0)');

        ctx.beginPath();
        data.forEach((d, i) => {
            const x = pad.left + (i / (data.length - 1)) * cw;
            const y = pad.top + (1 - (d.value - minV) / (maxV - minV)) * ch;
            if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
        });
        ctx.strokeStyle = '#06b6d4';
        ctx.lineWidth = 2;
        ctx.stroke();

        const lastX = pad.left + cw;
        const lastY = pad.top + (1 - (vals[vals.length - 1] - minV) / (maxV - minV)) * ch;
        ctx.beginPath();
        ctx.arc(lastX, lastY, 4, 0, Math.PI * 2);
        ctx.fillStyle = '#06b6d4';
        ctx.fill();
    }
};
