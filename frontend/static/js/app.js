const App = {
    ws: null,
    selectedTank: 1,
    tankData: {},
    tanks: [],
    wsReconnectTimer: null,

    API_BASE: window.location.origin,

    async init() {
        this.updateClock();
        setInterval(() => this.updateClock(), 1000);
        await this.loadTanks();
        this.initWebSocket();
        this.selectTank(1);
    },

    updateClock() {
        const el = document.getElementById('current-time');
        if (el) {
            el.textContent = new Date().toLocaleString('zh-CN');
        }
    },

    async loadTanks() {
        try {
            const resp = await fetch(`${this.API_BASE}/api/tanks`);
            this.tanks = await resp.json();
            this.renderTankButtons();
        } catch (e) {
            console.error('加载储罐列表失败:', e);
        }
    },

    renderTankButtons() {
        const container = document.getElementById('tank-buttons');
        container.innerHTML = '';
        this.tanks.forEach(tank => {
            const btn = document.createElement('div');
            btn.className = 'tank-btn' + (tank.id === this.selectedTank ? ' active' : '');
            btn.dataset.tankId = tank.id;
            btn.innerHTML = `
                <span class="tank-code">${tank.tank_code}</span>
                <span class="tank-risk low" id="tank-risk-${tank.id}">--</span>
            `;
            btn.addEventListener('click', () => this.selectTank(tank.id));
            container.appendChild(btn);
        });
    },

    async selectTank(tankId) {
        this.selectedTank = tankId;
        document.querySelectorAll('.tank-btn').forEach(btn => {
            btn.classList.toggle('active', parseInt(btn.dataset.tankId) === tankId);
        });
        await this.loadTankData(tankId);
    },

    async loadTankData(tankId) {
        try {
            const resp = await fetch(`${this.API_BASE}/api/tank/${tankId}`);
            const data = await resp.json();
            this.tankData[tankId] = data;
            this.renderTankSnapshot(data);
            Tank3D.updateTank(data);
            DensityContour.update(data);
            RiskGauge.draw(data.risk_index || 0);
            this.updatePrediction(data);
        } catch (e) {
            console.error('加载储罐数据失败:', e);
        }
    },

    renderTankSnapshot(data) {
        this.renderTempLayers(data.temperatures || []);
        this.renderDensityLayers(data.densities || []);
        this.renderTopParams(data);
        this.renderRecentAlarms(data.alarms || []);
        this.updateTankRiskBadge(data);
    },

    renderTempLayers(layers) {
        const container = document.getElementById('temp-layers');
        container.innerHTML = '';
        const minT = -170, maxT = -150;
        layers.forEach(layer => {
            const avg = layer.avg_temp || 0;
            const pct = Math.max(0, Math.min(100, ((avg - minT) / (maxT - minT)) * 100));
            const color = this.tempToColor(avg);
            const row = document.createElement('div');
            row.className = 'layer-row';
            row.innerHTML = `
                <span class="layer-label">L${layer.layer_index}</span>
                <span class="layer-value" style="color:${color}">${avg.toFixed(1)}°C</span>
                <div class="layer-bar">
                    <div class="layer-bar-fill" style="width:${pct}%;background:${color}"></div>
                </div>
            `;
            container.appendChild(row);
        });
    },

    renderDensityLayers(densities) {
        const container = document.getElementById('density-layers');
        container.innerHTML = '';
        const minD = 440, maxD = 470;
        densities.forEach(d => {
            const val = d.value_kg_m3 || 0;
            const pct = Math.max(0, Math.min(100, ((val - minD) / (maxD - minD)) * 100));
            const color = this.densityToColor(val);
            const row = document.createElement('div');
            row.className = 'layer-row';
            row.innerHTML = `
                <span class="layer-label">L${d.layer_index}</span>
                <span class="layer-value" style="color:${color}">${val.toFixed(1)}</span>
                <div class="layer-bar">
                    <div class="layer-bar-fill" style="width:${pct}%;background:${color}"></div>
                </div>
            `;
            container.appendChild(row);
        });
    },

    renderTopParams(data) {
        document.getElementById('pressure-val').textContent = (data.pressure_kpa || 0).toFixed(1) + ' kPa';

        const tank = this.tanks.find(t => t.id === data.tank_id);
        if (tank) {
            document.getElementById('design-pressure-val').textContent = tank.design_pressure_kpa.toFixed(1) + ' kPa';
            const ratio = ((data.pressure_kpa || 0) / tank.design_pressure_kpa * 100);
            const ratioEl = document.getElementById('pressure-ratio');
            ratioEl.textContent = ratio.toFixed(1) + '%';
            ratioEl.style.color = ratio > 90 ? '#ef4444' : ratio > 75 ? '#f59e0b' : '#10b981';
        }

        const bogEl = document.getElementById('bog-status');
        bogEl.textContent = data.bog_running ? '运行中' : '停止';
        bogEl.style.color = data.bog_running ? '#10b981' : '#8899aa';
    },

    renderRecentAlarms(alarms) {
        const container = document.getElementById('recent-alarms');
        container.innerHTML = '';
        if (alarms.length === 0) {
            container.innerHTML = '<div style="color:#8899aa;font-size:12px">暂无告警</div>';
            return;
        }
        alarms.forEach(a => {
            const item = document.createElement('div');
            item.className = `alarm-item level${a.alarm_level}`;
            item.innerHTML = `
                <div class="alarm-time">${new Date(a.time).toLocaleString('zh-CN')}</div>
                <div class="alarm-msg">${a.message}</div>
            `;
            container.appendChild(item);
        });
    },

    updateTankRiskBadge(data) {
        const badge = document.getElementById(`tank-risk-${data.tank_id}`);
        if (!badge) return;
        const risk = data.risk_index || 0;
        badge.textContent = (risk * 100).toFixed(0) + '%';
        badge.className = 'tank-risk ' + (risk > 0.7 ? 'high' : risk > 0.4 ? 'medium' : 'low');
    },

    updatePrediction(data) {
        const container = document.getElementById('prediction-info');
        fetch(`${this.API_BASE}/api/prediction/${data.tank_id}`)
            .then(r => r.json())
            .then(pred => {
                container.innerHTML = `
                    <div class="pred-item"><span class="pred-label">风险指数</span><span class="pred-value">${(pred.risk_index || 0).toFixed(3)}</span></div>
                    <div class="pred-item"><span class="pred-label">稳定评分</span><span class="pred-value">${(pred.layer_stability_score || 0).toFixed(3)}</span></div>
                    <div class="pred-item"><span class="pred-label">最大温差</span><span class="pred-value">${(pred.max_temp_gradient || 0).toFixed(2)}°C</span></div>
                    <div class="pred-item"><span class="pred-label">最大密度差</span><span class="pred-value">${(pred.max_density_gradient || 0).toFixed(2)} kg/m³</span></div>
                    <div class="pred-item"><span class="pred-label">预测翻滚时间</span><span class="pred-value">${pred.predicted_rollover_time ? new Date(pred.predicted_rollover_time).toLocaleString('zh-CN') : '未预测到'}</span></div>
                    <div class="pred-item"><span class="pred-label">模型版本</span><span class="pred-value">${pred.model_version || '--'}</span></div>
                `;
            })
            .catch(() => {});
    },

    tempToColor(temp) {
        const t = (temp - (-170)) / ((-150) - (-170));
        const clamped = Math.max(0, Math.min(1, t));
        if (clamped < 0.25) {
            return this.lerpColor('#1e40af', '#06b6d4', clamped / 0.25);
        } else if (clamped < 0.5) {
            return this.lerpColor('#06b6d4', '#10b981', (clamped - 0.25) / 0.25);
        } else if (clamped < 0.75) {
            return this.lerpColor('#10b981', '#f59e0b', (clamped - 0.5) / 0.25);
        } else {
            return this.lerpColor('#f59e0b', '#ef4444', (clamped - 0.75) / 0.25);
        }
    },

    densityToColor(density) {
        const t = (density - 440) / (470 - 440);
        const clamped = Math.max(0, Math.min(1, t));
        if (clamped < 0.5) {
            return this.lerpColor('#06b6d4', '#8b5cf6', clamped / 0.5);
        } else {
            return this.lerpColor('#8b5cf6', '#ef4444', (clamped - 0.5) / 0.5);
        }
    },

    lerpColor(a, b, t) {
        const ar = parseInt(a.slice(1, 3), 16);
        const ag = parseInt(a.slice(3, 5), 16);
        const ab = parseInt(a.slice(5, 7), 16);
        const br = parseInt(b.slice(1, 3), 16);
        const bg = parseInt(b.slice(3, 5), 16);
        const bb = parseInt(b.slice(5, 7), 16);
        const r = Math.round(ar + (br - ar) * t);
        const g = Math.round(ag + (bg - ag) * t);
        const bl = Math.round(ab + (bb - ab) * t);
        return `rgb(${r},${g},${bl})`;
    },

    initWebSocket() {
        const wsUrl = `ws://${window.location.host}/ws`;
        this.ws = new WebSocket(wsUrl);

        this.ws.onopen = () => {
            const status = document.getElementById('ws-status');
            status.textContent = 'WS: 已连接';
            status.className = 'ws-status connected';
            if (this.wsReconnectTimer) {
                clearInterval(this.wsReconnectTimer);
                this.wsReconnectTimer = null;
            }
        };

        this.ws.onclose = () => {
            const status = document.getElementById('ws-status');
            status.textContent = 'WS: 已断开';
            status.className = 'ws-status disconnected';
            if (!this.wsReconnectTimer) {
                this.wsReconnectTimer = setInterval(() => this.initWebSocket(), 5000);
            }
        };

        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                this.handleWSMessage(msg);
            } catch (e) {
                console.error('WS消息解析失败:', e);
            }
        };
    },

    handleWSMessage(msg) {
        if (msg.type === 'alarm') {
            AlarmHandler.onAlarm(msg.payload);
        } else if (msg.type === 'data_update') {
            this.loadTankData(this.selectedTank);
        }
    }
};

const RiskGauge = {
    draw(risk) {
        const canvas = document.getElementById('risk-gauge');
        const ctx = canvas.getContext('2d');
        const w = canvas.width;
        const h = canvas.height;
        ctx.clearRect(0, 0, w, h);

        const cx = w / 2;
        const cy = h - 10;
        const r = 80;

        const startAngle = Math.PI;
        const endAngle = 2 * Math.PI;

        const gradient = ctx.createLinearGradient(cx - r, cy, cx + r, cy);
        gradient.addColorStop(0, '#10b981');
        gradient.addColorStop(0.4, '#f59e0b');
        gradient.addColorStop(0.7, '#f97316');
        gradient.addColorStop(1, '#ef4444');

        ctx.beginPath();
        ctx.arc(cx, cy, r, startAngle, endAngle);
        ctx.lineWidth = 14;
        ctx.strokeStyle = gradient;
        ctx.stroke();

        ctx.beginPath();
        ctx.arc(cx, cy, r, startAngle, endAngle);
        ctx.lineWidth = 14;
        ctx.strokeStyle = 'rgba(255,255,255,0.1)';
        ctx.stroke();

        const riskAngle = startAngle + risk * Math.PI;
        ctx.beginPath();
        ctx.arc(cx, cy, r, startAngle, riskAngle);
        ctx.lineWidth = 14;
        ctx.strokeStyle = gradient;
        ctx.stroke();

        const needleX = cx + r * Math.cos(riskAngle);
        const needleY = cy + r * Math.sin(riskAngle);
        ctx.beginPath();
        ctx.arc(needleX, needleY, 5, 0, 2 * Math.PI);
        ctx.fillStyle = '#fff';
        ctx.fill();

        const valueEl = document.getElementById('risk-value');
        valueEl.textContent = (risk * 100).toFixed(1) + '%';
        valueEl.style.color = risk > 0.7 ? '#ef4444' : risk > 0.4 ? '#f59e0b' : '#10b981';
    }
};
