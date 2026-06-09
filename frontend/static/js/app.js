const App = {
    selectedTank: 1,
    ws: null,
    wsReconnectTimer: null,

    async init() {
        Tank3DViewer.init({
            onSensorClick: (sensorData, x, y) => {
                RiskDashboard.showSensorPopup(sensorData, x, y);
            }
        });

        RiskDashboard.init();

        this._loadTankNav();
        this._connectWebSocket();

        document.getElementById('close-popup')?.addEventListener('click', () => {
            RiskDashboard.hideSensorPopup();
        });

        document.getElementById('density-contour-canvas')?.addEventListener('click', (e) => {
            this._onContourClick(e);
        });
    },

    async selectTank(tankId) {
        this.selectedTank = tankId;
        document.querySelectorAll('.tank-btn').forEach(btn => {
            btn.classList.toggle('active', parseInt(btn.dataset.tankId) === tankId);
        });
        await this._loadTankData(tankId);
    },

    async _loadTankNav() {
        try {
            const resp = await fetch('/api/tanks');
            const tanks = await resp.json();
            const nav = document.getElementById('tank-nav');
            if (!nav) return;
            nav.innerHTML = tanks.map(t =>
                `<button class="tank-btn ${t.id === 1 ? 'active' : ''}" data-tank-id="${t.id}">${t.tank_code}</button>`
            ).join('');
            nav.querySelectorAll('.tank-btn').forEach(btn => {
                btn.addEventListener('click', () => this.selectTank(parseInt(btn.dataset.tankId)));
            });
        } catch (e) {
            console.error('Failed to load tanks:', e);
        }
        await this.selectTank(1);
    },

    async _loadTankData(tankId) {
        try {
            const [snapshotResp, predResp] = await Promise.all([
                fetch(`/api/tank/${tankId}`),
                fetch(`/api/prediction/${tankId}`)
            ]);
            const snapshot = await snapshotResp.json();
            const predData = await predResp.json();

            Tank3DViewer.update(snapshot);

            this._drawDensityContour(snapshot);

            snapshot.prediction = predData;
            RiskDashboard.updateDataPanel(snapshot);
            RiskDashboard.updateRiskGauge(predData.risk_index || 0);
        } catch (e) {
            console.error('Failed to load tank data:', e);
        }
    },

    _drawDensityContour(data) {
        const canvas = document.getElementById('density-contour-canvas');
        if (!canvas) return;
        const ctx = canvas.getContext('2d');
        const w = canvas.width;
        const h = canvas.height;
        ctx.clearRect(0, 0, w, h);

        const densities = data.densities || [];
        if (densities.length === 0) return;

        const numLayers = 5;
        const numCols = 16;
        const grid = [];
        for (let i = 0; i < numLayers; i++) {
            grid[i] = new Float64Array(numCols);
            const layerD = densities.find(d => d.layer_index === i + 1);
            const baseVal = layerD ? layerD.value_kg_m3 : 450;
            for (let j = 0; j < numCols; j++) {
                const angle = (j / numCols) * Math.PI * 2;
                grid[i][j] = baseVal + 0.5 * Math.sin(angle + i * 0.5);
            }
        }

        const levels = [-165, -163, -161, -159];
        const layerH = h / numLayers;
        const colW = w / numCols;

        levels.forEach((level, li) => {
            const hue = 180 + li * 40;
            ctx.strokeStyle = `hsl(${hue}, 80%, 60%)`;
            ctx.lineWidth = 1.5;
            ctx.setLineDash([5, 3]);

            for (let i = 0; i < numLayers - 1; i++) {
                for (let j = 0; j < numCols; j++) {
                    const nj = (j + 1) % numCols;
                    const tl = grid[i][j], tr = grid[i][nj], bl = grid[i + 1][j], br = grid[i + 1][nj];
                    const x0 = j * colW, y0 = i * layerH, x1 = (j + 1) * colW, y1 = (i + 1) * layerH;
                    const config = this._marchingSquares(tl, tr, bl, br, level);
                    this._drawContourCell(ctx, config, x0, y0, x1, y1, tl, tr, bl, br, level);
                }
            }
        });
        ctx.setLineDash([]);
    },

    _marchingSquares(tl, tr, bl, br, level) {
        let config = 0;
        if (tl >= level) config |= 8;
        if (tr >= level) config |= 4;
        if (br >= level) config |= 2;
        if (bl >= level) config |= 1;
        return config;
    },

    _drawContourCell(ctx, config, x0, y0, x1, y1, tl, tr, bl, br, level) {
        const lerp = (a, b) => (level - a) / (b - a);
        const top = { x: x0 + lerp(tl, tr) * (x1 - x0), y: y0 };
        const right = { x: x1, y: y0 + lerp(tr, br) * (y1 - y0) };
        const bottom = { x: x0 + lerp(bl, br) * (x1 - x0), y: y1 };
        const left = { x: x0, y: y0 + lerp(tl, bl) * (y1 - y0) };

        const lines = [];
        switch (config) {
            case 1: case 14: lines.push([left, bottom]); break;
            case 2: case 13: lines.push([bottom, right]); break;
            case 3: case 12: lines.push([left, right]); break;
            case 4: case 11: lines.push([top, right]); break;
            case 5: lines.push([left, top], [bottom, right]); break;
            case 6: case 9: lines.push([top, bottom]); break;
            case 7: case 8: lines.push([left, top]); break;
            case 10: lines.push([top, left], [right, bottom]); break;
        }

        lines.forEach(([p1, p2]) => {
            ctx.beginPath();
            ctx.moveTo(p1.x, p1.y);
            ctx.lineTo(p2.x, p2.y);
            ctx.stroke();
        });
    },

    _onContourClick(e) {
        const canvas = e.target;
        const rect = canvas.getBoundingClientRect();
        const x = e.clientX - rect.left;
        const y = e.clientY - rect.top;
        const layerIndex = Math.floor(y / (canvas.height / 5)) + 1;
        RiskDashboard.showSensorPopup({
            sensorType: 'density',
            layerIndex,
            value: undefined,
            sensorId: null
        }, e.clientX, e.clientY);
    },

    _connectWebSocket() {
        const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${protocol}//${location.host}/ws`;
        this.ws = new WebSocket(url);

        this.ws.onopen = () => {
            console.log('WebSocket connected');
            if (this.wsReconnectTimer) { clearTimeout(this.wsReconnectTimer); this.wsReconnectTimer = null; }
        };

        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                if (msg.type === 'alarm' && msg.payload) {
                    RiskDashboard.showAlarm(msg.payload);
                } else if (msg.type === 'data_update') {
                    this._loadTankData(this.selectedTank);
                }
            } catch (e) { }
        };

        this.ws.onclose = () => {
            console.log('WebSocket closed, reconnecting in 5s');
            this.wsReconnectTimer = setTimeout(() => this._connectWebSocket(), 5000);
        };

        this.ws.onerror = () => {
            this.ws.close();
        };
    }
};
