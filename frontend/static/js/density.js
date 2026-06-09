const DensityContour = {
    canvas: null,
    ctx: null,

    init() {
        this.canvas = document.getElementById('density-contour');
        this.ctx = this.canvas.getContext('2d');
        this.resize();
        window.addEventListener('resize', () => this.resize());
    },

    resize() {
        const container = document.getElementById('density-contour-container');
        this.canvas.width = container.clientWidth;
        this.canvas.height = container.clientHeight;
    },

    update(data) {
        if (!this.ctx) return;
        const ctx = this.ctx;
        const w = this.canvas.width;
        const h = this.canvas.height;
        ctx.clearRect(0, 0, w, h);

        ctx.fillStyle = '#0a0e1a';
        ctx.fillRect(0, 0, w, h);

        const temperatures = data.temperatures || [];
        const densities = data.densities || [];
        const numLayers = 5;
        const numPositions = 8;

        const marginLeft = 50;
        const marginRight = 20;
        const marginTop = 20;
        const marginBottom = 30;
        const plotW = w - marginLeft - marginRight;
        const plotH = h - marginTop - marginBottom;

        ctx.strokeStyle = '#2a3a52';
        ctx.lineWidth = 1;
        ctx.strokeRect(marginLeft, marginTop, plotW, plotH);

        ctx.fillStyle = '#8899aa';
        ctx.font = '11px sans-serif';
        ctx.textAlign = 'right';
        for (let i = 0; i <= numLayers; i++) {
            const y = marginTop + (i / numLayers) * plotH;
            ctx.beginPath();
            ctx.moveTo(marginLeft, y);
            ctx.lineTo(marginLeft + plotW, y);
            ctx.strokeStyle = 'rgba(42,58,82,0.5)';
            ctx.stroke();

            ctx.fillStyle = '#8899aa';
            ctx.fillText(`L${numLayers - i}`, marginLeft - 6, y + 4);
        }

        ctx.textAlign = 'center';
        for (let i = 0; i <= 4; i++) {
            const x = marginLeft + (i / 4) * plotW;
            ctx.fillStyle = '#8899aa';
            ctx.fillText(`P${i * 2 + 1}`, x, marginTop + plotH + 16);
        }

        const grid = [];
        for (let layer = 0; layer < numLayers; layer++) {
            grid[layer] = [];
            for (let pos = 0; pos < numPositions; pos++) {
                const layerData = temperatures.find(l => l.layer_index === layer + 1);
                if (layerData && layerData.sensors && layerData.sensors[pos]) {
                    grid[layer][pos] = layerData.sensors[pos].value;
                } else if (layerData) {
                    grid[layer][pos] = layerData.avg_temp;
                } else {
                    grid[layer][pos] = -162;
                }
            }
        }

        const cellW = plotW / (numPositions - 1);
        const cellH = plotH / (numLayers - 1);

        for (let layer = 0; layer < numLayers; layer++) {
            for (let pos = 0; pos < numPositions; pos++) {
                const x = marginLeft + pos * cellW;
                const y = marginTop + (numLayers - 1 - layer) * cellH;
                const temp = grid[layer][pos];
                const color = App.tempToColor(temp);

                ctx.fillStyle = color;
                ctx.globalAlpha = 0.3;
                ctx.fillRect(x - cellW / 2, y - cellH / 2, cellW, cellH);
                ctx.globalAlpha = 1.0;

                ctx.fillStyle = '#e0e7ef';
                ctx.font = '10px sans-serif';
                ctx.textAlign = 'center';
                ctx.fillText(temp.toFixed(1), x, y + 3);
            }
        }

        this.drawContourLines(ctx, grid, marginLeft, marginTop, plotW, plotH, numLayers, numPositions, cellW, cellH);

        densities.forEach(d => {
            const layerIdx = d.layer_index - 1;
            const y = marginTop + (numLayers - 1 - layerIdx) * cellH;
            const x = marginLeft + plotW / 2;

            ctx.beginPath();
            ctx.arc(x, y, 6, 0, Math.PI * 2);
            ctx.fillStyle = App.densityToColor(d.value_kg_m3);
            ctx.fill();
            ctx.strokeStyle = '#fff';
            ctx.lineWidth = 1.5;
            ctx.stroke();

            ctx.fillStyle = '#fff';
            ctx.font = 'bold 9px sans-serif';
            ctx.textAlign = 'center';
            ctx.fillText(d.value_kg_m3.toFixed(1), x, y - 10);
        });

        ctx.fillStyle = '#06b6d4';
        ctx.font = 'bold 12px sans-serif';
        ctx.textAlign = 'left';
        ctx.fillText('温度分层剖面 + 密度等值线', marginLeft, 14);

        const legendX = w - marginRight - 100;
        const legendY = marginTop + 10;
        const grad = ctx.createLinearGradient(legendX, 0, legendX + 80, 0);
        grad.addColorStop(0, '#1e40af');
        grad.addColorStop(0.25, '#06b6d4');
        grad.addColorStop(0.5, '#10b981');
        grad.addColorStop(0.75, '#f59e0b');
        grad.addColorStop(1, '#ef4444');
        ctx.fillStyle = grad;
        ctx.fillRect(legendX, legendY, 80, 8);
        ctx.fillStyle = '#8899aa';
        ctx.font = '9px sans-serif';
        ctx.textAlign = 'left';
        ctx.fillText('-170°C', legendX, legendY + 18);
        ctx.textAlign = 'right';
        ctx.fillText('-150°C', legendX + 80, legendY + 18);
    },

    drawContourLines(ctx, grid, mx, my, pw, ph, nl, np, cw, ch) {
        const levels = [-165, -163, -161, -159];
        levels.forEach(level => {
            const points = [];

            for (let layer = 0; layer < nl - 1; layer++) {
                for (let pos = 0; pos < np - 1; pos++) {
                    const v00 = grid[layer][pos];
                    const v10 = grid[layer][pos + 1];
                    const v01 = grid[layer + 1][pos];
                    const v11 = grid[layer + 1][pos + 1];

                    const edges = this.marchingSquares(v00, v10, v01, v11, level);
                    const x0 = mx + pos * cw;
                    const y0 = my + (nl - 1 - layer) * ch;
                    const x1 = mx + (pos + 1) * cw;
                    const y1 = my + (nl - 1 - (layer + 1)) * ch;

                    edges.forEach(edge => {
                        const p1 = this.interpolateEdge(edge[0], x0, y0, x1, y1, v00, v10, v01, v11, level);
                        const p2 = this.interpolateEdge(edge[1], x0, y0, x1, y1, v00, v10, v01, v11, level);
                        if (p1 && p2) {
                            points.push([p1, p2]);
                        }
                    });
                }
            }

            if (points.length > 0) {
                ctx.beginPath();
                ctx.strokeStyle = 'rgba(255,255,255,0.4)';
                ctx.lineWidth = 1;
                ctx.setLineDash([4, 4]);
                points.forEach(([p1, p2]) => {
                    ctx.moveTo(p1[0], p1[1]);
                    ctx.lineTo(p2[0], p2[1]);
                });
                ctx.stroke();
                ctx.setLineDash([]);

                const lastPt = points[points.length - 1][1];
                ctx.fillStyle = 'rgba(255,255,255,0.6)';
                ctx.font = '9px sans-serif';
                ctx.textAlign = 'left';
                ctx.fillText(level.toFixed(0) + '°C', lastPt[0] + 4, lastPt[1] - 2);
            }
        });
    },

    marchingSquares(a, b, c, d, level) {
        let code = 0;
        if (a > level) code |= 1;
        if (b > level) code |= 2;
        if (d > level) code |= 4;
        if (c > level) code |= 8;

        const table = {
            0: [], 1: [['left', 'bottom']], 2: [['bottom', 'right']],
            3: [['left', 'right']], 4: [['top', 'right']],
            5: [['left', 'top'], ['bottom', 'right']], 6: [['bottom', 'top']],
            7: [['left', 'top']], 8: [['left', 'top']], 9: [['top', 'bottom']],
            10: [['left', 'bottom'], ['top', 'right']], 11: [['top', 'right']],
            12: [['left', 'right']], 13: [['bottom', 'right']],
            14: [['left', 'bottom']], 15: []
        };

        return table[code] || [];
    },

    interpolateEdge(edge, x0, y0, x1, y1, v00, v10, v01, v11, level) {
        const lerp = (a, b, va, vb) => {
            if (Math.abs(vb - va) < 0.001) return (a + b) / 2;
            const t = (level - va) / (vb - va);
            return a + (b - a) * t;
        };

        switch (edge) {
            case 'left':
                return [x0, lerp(y0, y1, v00, v01)];
            case 'right':
                return [x1, lerp(y0, y1, v10, v11)];
            case 'top':
                return [lerp(x0, x1, v00, v10), y0];
            case 'bottom':
                return [lerp(x0, x1, v01, v11), y1];
        }
        return null;
    }
};
