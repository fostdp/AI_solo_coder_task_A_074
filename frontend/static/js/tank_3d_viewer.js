const Tank3DViewer = {
    scene: null,
    camera: null,
    renderer: null,
    tankGroup: null,
    sensorMarkers: [],
    raycaster: null,
    mouse: null,
    animFrame: null,
    isMobile: false,
    qualityLevel: 'high',
    onSensorClick: null,

    detectDeviceCapability() {
        const ua = navigator.userAgent.toLowerCase();
        const isMobileUA = /android|iphone|ipad|ipod|mobile/.test(ua);
        const isLowMem = navigator.deviceMemory !== undefined && navigator.deviceMemory <= 4;
        const isLowCore = navigator.hardwareConcurrency !== undefined && navigator.hardwareConcurrency <= 4;
        const isTouchDevice = 'ontouchstart' in window;
        this.isMobile = isMobileUA || isTouchDevice || isLowMem;
        if (isLowMem || (this.isMobile && isLowCore)) {
            this.qualityLevel = 'low';
        } else if (this.isMobile) {
            this.qualityLevel = 'medium';
        } else {
            this.qualityLevel = 'high';
        }
    },

    getGeometryParams() {
        switch (this.qualityLevel) {
            case 'low':
                return { cylinderSegs: 8, sphereSegs: 4, coneSegs: 4, pixelRatioCap: 1.0, enableEdges: false, enableShell: false, enableRoof: false };
            case 'medium':
                return { cylinderSegs: 16, sphereSegs: 6, coneSegs: 6, pixelRatioCap: 1.5, enableEdges: false, enableShell: true, enableRoof: false };
            default:
                return { cylinderSegs: 32, sphereSegs: 10, coneSegs: 8, pixelRatioCap: 2.0, enableEdges: true, enableShell: true, enableRoof: true };
        }
    },

    init(options) {
        this.onSensorClick = options.onSensorClick || null;
        this.detectDeviceCapability();
        const gp = this.getGeometryParams();

        const container = document.getElementById('three-container');
        const canvas = document.getElementById('tank-3d');

        this.scene = new THREE.Scene();
        this.scene.background = new THREE.Color(0x0a0e1a);

        this.camera = new THREE.PerspectiveCamera(45, container.clientWidth / container.clientHeight, 0.1, 1000);
        this.camera.position.set(8, 6, 12);
        this.camera.lookAt(0, 3, 0);

        const pixelRatio = Math.min(window.devicePixelRatio, gp.pixelRatioCap);
        this.renderer = new THREE.WebGLRenderer({
            canvas,
            antialias: !this.isMobile,
            powerPreference: this.isMobile ? 'low-power' : 'high-performance',
        });
        this.renderer.setSize(container.clientWidth, container.clientHeight);
        this.renderer.setPixelRatio(pixelRatio);

        this.scene.add(new THREE.AmbientLight(0x404060, 0.6));
        const dirLight = new THREE.DirectionalLight(0xffffff, 0.8);
        dirLight.position.set(10, 15, 10);
        this.scene.add(dirLight);

        if (!this.isMobile) {
            const pointLight = new THREE.PointLight(0x06b6d4, 0.5, 30);
            pointLight.position.set(0, 8, 0);
            this.scene.add(pointLight);
        }

        this.raycaster = new THREE.Raycaster();
        this.mouse = new THREE.Vector2();

        const gridHelper = new THREE.GridHelper(this.isMobile ? 10 : 20, this.isMobile ? 10 : 20, 0x2a3a52, 0x1a2332);
        gridHelper.position.y = -0.01;
        this.scene.add(gridHelper);

        this._bindEvents(container, canvas);

        if (this.isMobile) {
            document.addEventListener('visibilitychange', () => {
                if (document.hidden) {
                    if (this.animFrame) { cancelAnimationFrame(this.animFrame); this.animFrame = null; }
                } else {
                    if (!this.animFrame) this.animate();
                }
            });
        }

        this.animate();
    },

    _bindEvents(container, canvas) {
        canvas.addEventListener('click', (e) => this._onClick(e, container));
        canvas.addEventListener('mousemove', (e) => this._onMouseMove(e, container));

        let isDragging = false, prevX = 0;
        canvas.addEventListener('mousedown', (e) => { isDragging = true; prevX = e.clientX; });
        canvas.addEventListener('mouseup', () => isDragging = false);
        canvas.addEventListener('mouseleave', () => isDragging = false);
        canvas.addEventListener('mousemove', (e) => {
            if (!isDragging || !this.tankGroup) return;
            this.tankGroup.rotation.y += (e.clientX - prevX) * 0.005;
            prevX = e.clientX;
        });

        let touchStartX = 0;
        canvas.addEventListener('touchstart', (e) => { if (e.touches.length === 1) touchStartX = e.touches[0].clientX; }, { passive: true });
        canvas.addEventListener('touchmove', (e) => {
            if (e.touches.length === 1 && this.tankGroup) {
                this.tankGroup.rotation.y += (e.touches[0].clientX - touchStartX) * 0.005;
                touchStartX = e.touches[0].clientX;
            }
        }, { passive: true });
        canvas.addEventListener('touchend', (e) => {
            if (e.changedTouches.length === 1) this._handlePick(e.changedTouches[0].clientX, e.changedTouches[0].clientY, container);
        });

        window.addEventListener('resize', () => {
            this.camera.aspect = container.clientWidth / container.clientHeight;
            this.camera.updateProjectionMatrix();
            this.renderer.setSize(container.clientWidth, container.clientHeight);
        });
    },

    _onClick(e, container) {
        this._handlePick(e.clientX, e.clientY, container);
    },

    _onMouseMove(e, container) {
        const rect = container.getBoundingClientRect();
        this.mouse.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
        this.mouse.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
        this.raycaster.setFromCamera(this.mouse, this.camera);
        const intersects = this.raycaster.intersectObjects(this.sensorMarkers);
        container.style.cursor = intersects.length > 0 ? 'pointer' : 'default';
    },

    _handlePick(clientX, clientY, container) {
        const rect = container.getBoundingClientRect();
        this.mouse.x = ((clientX - rect.left) / rect.width) * 2 - 1;
        this.mouse.y = -((clientY - rect.top) / rect.height) * 2 + 1;
        this.raycaster.setFromCamera(this.mouse, this.camera);
        const intersects = this.raycaster.intersectObjects(this.sensorMarkers);
        if (intersects.length > 0 && this.onSensorClick) {
            this.onSensorClick(intersects[0].object.userData, clientX, clientY);
        }
    },

    update(data) {
        if (this.tankGroup) {
            this.tankGroup.traverse((obj) => {
                if (obj.geometry) obj.geometry.dispose();
                if (obj.material) { Array.isArray(obj.material) ? obj.material.forEach(m => m.dispose()) : obj.material.dispose(); }
            });
            this.scene.remove(this.tankGroup);
        }
        this.tankGroup = new THREE.Group();
        this.sensorMarkers = [];

        const gp = this.getGeometryParams();
        const tankHeight = 7.6, tankRadius = 3.0, numLayers = 5;
        const layerH = tankHeight / numLayers;
        const temperatures = data.temperatures || [];
        const densities = data.densities || [];

        const sharedCylGeom = new THREE.CylinderGeometry(tankRadius, tankRadius, layerH, gp.cylinderSegs, 1, false);
        const sharedSphereGeom = new THREE.SphereGeometry(0.08, gp.sphereSegs, gp.sphereSegs);
        const sharedOctGeom = new THREE.OctahedronGeometry(0.12, 0);

        for (let i = 0; i < numLayers; i++) {
            const layerData = temperatures.find(l => l.layer_index === i + 1);
            const avgTemp = layerData ? layerData.avg_temp : -162;
            const color = new THREE.Color(this._tempToHex(avgTemp));

            const layerMesh = new THREE.Mesh(sharedCylGeom, new THREE.MeshPhongMaterial({
                color, transparent: true, opacity: 0.7, shininess: this.isMobile ? 20 : 60,
            }));
            layerMesh.position.y = i * layerH + layerH / 2;
            this.tankGroup.add(layerMesh);

            if (gp.enableEdges) {
                const edges = new THREE.LineSegments(
                    new THREE.EdgesGeometry(sharedCylGeom),
                    new THREE.LineBasicMaterial({ color: 0x3b82f6, transparent: true, opacity: 0.4 })
                );
                edges.position.copy(layerMesh.position);
                this.tankGroup.add(edges);
            }

            if (layerData && layerData.sensors) {
                layerData.sensors.forEach((sensor, si) => {
                    const angle = (si / 8) * Math.PI * 2;
                    const marker = new THREE.Mesh(sharedSphereGeom, new THREE.MeshPhongMaterial({
                        color: 0xffffff, emissive: color, emissiveIntensity: 0.6,
                    }));
                    marker.position.set(
                        Math.cos(angle) * (tankRadius * 0.85),
                        i * layerH + layerH / 2,
                        Math.sin(angle) * (tankRadius * 0.85)
                    );
                    marker.userData = { sensorId: sensor.sensor_id, sensorType: 'temperature', layerIndex: i + 1, positionIndex: sensor.position_index, value: sensor.value };
                    this.tankGroup.add(marker);
                    this.sensorMarkers.push(marker);
                });
            }
        }

        const densityPositions = [{ layer: 1, angle: 0 }, { layer: 3, angle: Math.PI * 2 / 3 }, { layer: 5, angle: Math.PI * 4 / 3 }];
        densities.forEach((d, di) => {
            if (di >= densityPositions.length) return;
            const pos = densityPositions[di];
            const layerIdx = d.layer_index - 1;
            const densColor = new THREE.Color(this._densityToHex(d.value_kg_m3));
            const marker = new THREE.Mesh(sharedOctGeom, new THREE.MeshPhongMaterial({
                color: 0xffffff, emissive: densColor, emissiveIntensity: 0.8,
            }));
            marker.position.set(Math.cos(pos.angle) * (tankRadius * 0.5), layerIdx * layerH + layerH / 2, Math.sin(pos.angle) * (tankRadius * 0.5));
            marker.userData = { sensorId: null, sensorType: 'density', layerIndex: d.layer_index, value: d.value_kg_m3 };
            this.tankGroup.add(marker);
            this.sensorMarkers.push(marker);
        });

        if (gp.enableShell) {
            const shell = new THREE.Mesh(
                new THREE.CylinderGeometry(tankRadius + 0.05, tankRadius + 0.05, tankHeight + 0.1, gp.cylinderSegs, 1, true),
                new THREE.MeshPhongMaterial({ color: 0x4488aa, transparent: true, opacity: 0.12, side: THREE.DoubleSide })
            );
            shell.position.y = tankHeight / 2;
            this.tankGroup.add(shell);
        }

        if (gp.enableRoof) {
            const roof = new THREE.Mesh(
                new THREE.SphereGeometry(tankRadius + 0.05, gp.cylinderSegs, Math.floor(gp.cylinderSegs / 2), 0, Math.PI * 2, 0, Math.PI / 2),
                new THREE.MeshPhongMaterial({ color: 0x5599bb, transparent: true, opacity: 0.15, side: THREE.DoubleSide })
            );
            roof.position.y = tankHeight;
            this.tankGroup.add(roof);
        }

        const pressureMarker = new THREE.Mesh(
            new THREE.ConeGeometry(0.15, 0.3, gp.coneSegs),
            new THREE.MeshPhongMaterial({ color: 0x3b82f6, emissive: 0x1e40af, emissiveIntensity: 0.4 })
        );
        pressureMarker.position.set(0, tankHeight + 0.3, 0);
        pressureMarker.userData = { sensorType: 'pressure', value: data.pressure_kpa || 0 };
        this.tankGroup.add(pressureMarker);
        this.sensorMarkers.push(pressureMarker);

        this.scene.add(this.tankGroup);
    },

    animate() {
        this.animFrame = requestAnimationFrame(() => this.animate());
        this.renderer.render(this.scene, this.camera);
    },

    _tempToHex(temp) {
        const t = Math.max(0, Math.min(1, (temp + 170) / 20));
        let r, g, b;
        if (t < 0.25) { const s = t / 0.25; r = 30 + (6 - 30) * s; g = 64 + (182 - 64) * s; b = 175 + (212 - 175) * s; }
        else if (t < 0.5) { const s = (t - 0.25) / 0.25; r = 6 + 10 * s; g = 182 + 3 * s; b = 212 - 83 * s; }
        else if (t < 0.75) { const s = (t - 0.5) / 0.25; r = 16 + 229 * s; g = 185 - 27 * s; b = 129 - 118 * s; }
        else { const s = (t - 0.75) / 0.25; r = 245 - 6 * s; g = 158 - 90 * s; b = 11 + 57 * s; }
        return (Math.round(r) << 16) | (Math.round(g) << 8) | Math.round(b);
    },

    _densityToHex(density) {
        const t = Math.max(0, Math.min(1, (density - 440) / 30));
        let r, g, b;
        if (t < 0.5) { const s = t / 0.5; r = 6 + 133 * s; g = 182 - 90 * s; b = 212 + 34 * s; }
        else { const s = (t - 0.5) / 0.5; r = 139 + 100 * s; g = 92 - 24 * s; b = 246 - 178 * s; }
        return (Math.round(r) << 16) | (Math.round(g) << 8) | Math.round(b);
    }
};
