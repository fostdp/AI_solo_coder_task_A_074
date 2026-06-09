const Tank3D = {
    scene: null,
    camera: null,
    renderer: null,
    tankGroup: null,
    sensorMarkers: [],
    raycaster: null,
    mouse: null,
    animFrame: null,

    init() {
        const container = document.getElementById('three-container');
        const canvas = document.getElementById('tank-3d');

        this.scene = new THREE.Scene();
        this.scene.background = new THREE.Color(0x0a0e1a);

        this.camera = new THREE.PerspectiveCamera(45, container.clientWidth / container.clientHeight, 0.1, 1000);
        this.camera.position.set(8, 6, 12);
        this.camera.lookAt(0, 3, 0);

        this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
        this.renderer.setSize(container.clientWidth, container.clientHeight);
        this.renderer.setPixelRatio(window.devicePixelRatio);

        this.scene.add(new THREE.AmbientLight(0x404060, 0.6));
        const dirLight = new THREE.DirectionalLight(0xffffff, 0.8);
        dirLight.position.set(10, 15, 10);
        this.scene.add(dirLight);
        const pointLight = new THREE.PointLight(0x06b6d4, 0.5, 30);
        pointLight.position.set(0, 8, 0);
        this.scene.add(pointLight);

        this.raycaster = new THREE.Raycaster();
        this.mouse = new THREE.Vector2();

        this.createBaseEnvironment();

        canvas.addEventListener('click', (e) => this.onClick(e));
        canvas.addEventListener('mousemove', (e) => this.onMouseMove(e));

        let isDragging = false;
        let prevX = 0, prevY = 0;

        canvas.addEventListener('mousedown', (e) => {
            isDragging = true;
            prevX = e.clientX;
            prevY = e.clientY;
        });

        canvas.addEventListener('mouseup', () => isDragging = false);
        canvas.addEventListener('mouseleave', () => isDragging = false);

        canvas.addEventListener('mousemove', (e) => {
            if (!isDragging || !this.tankGroup) return;
            const dx = e.clientX - prevX;
            const dy = e.clientY - prevY;
            this.tankGroup.rotation.y += dx * 0.005;
            prevX = e.clientX;
            prevY = e.clientY;
        });

        window.addEventListener('resize', () => {
            this.camera.aspect = container.clientWidth / container.clientHeight;
            this.camera.updateProjectionMatrix();
            this.renderer.setSize(container.clientWidth, container.clientHeight);
        });

        this.animate();
    },

    createBaseEnvironment() {
        const gridHelper = new THREE.GridHelper(20, 20, 0x2a3a52, 0x1a2332);
        gridHelper.position.y = -0.01;
        this.scene.add(gridHelper);
    },

    updateTank(data) {
        if (this.tankGroup) {
            this.scene.remove(this.tankGroup);
        }
        this.tankGroup = new THREE.Group();
        this.sensorMarkers = [];

        const tankHeight = 7.6;
        const tankRadius = 3.0;
        const numLayers = 5;
        const layerH = tankHeight / numLayers;

        const temperatures = data.temperatures || [];
        const densities = data.densities || [];

        for (let i = 0; i < numLayers; i++) {
            const layerData = temperatures.find(l => l.layer_index === i + 1);
            const avgTemp = layerData ? layerData.avg_temp : -162;
            const color = new THREE.Color(this.tempToHex(avgTemp));

            const layerGeom = new THREE.CylinderGeometry(tankRadius, tankRadius, layerH, 32, 1, false);
            const layerMat = new THREE.MeshPhongMaterial({
                color: color,
                transparent: true,
                opacity: 0.7,
                shininess: 60,
            });
            const layerMesh = new THREE.Mesh(layerGeom, layerMat);
            layerMesh.position.y = i * layerH + layerH / 2;
            this.tankGroup.add(layerMesh);

            const edgeGeom = new THREE.EdgesGeometry(layerGeom);
            const edgeMat = new THREE.LineBasicMaterial({ color: 0x3b82f6, transparent: true, opacity: 0.4 });
            const edges = new THREE.LineSegments(edgeGeom, edgeMat);
            edges.position.copy(layerMesh.position);
            this.tankGroup.add(edges);

            if (layerData && layerData.sensors) {
                layerData.sensors.forEach((sensor, si) => {
                    const angle = (si / 8) * Math.PI * 2;
                    const sx = Math.cos(angle) * (tankRadius * 0.85);
                    const sz = Math.sin(angle) * (tankRadius * 0.85);
                    const sy = i * layerH + layerH / 2;

                    const markerGeom = new THREE.SphereGeometry(0.08, 12, 12);
                    const markerMat = new THREE.MeshPhongMaterial({
                        color: 0xffffff,
                        emissive: color,
                        emissiveIntensity: 0.6,
                    });
                    const marker = new THREE.Mesh(markerGeom, markerMat);
                    marker.position.set(sx, sy, sz);
                    marker.userData = {
                        sensorId: sensor.sensor_id,
                        sensorType: 'temperature',
                        layerIndex: i + 1,
                        positionIndex: sensor.position_index,
                        value: sensor.value,
                    };
                    this.tankGroup.add(marker);
                    this.sensorMarkers.push(marker);
                });
            }
        }

        const densityPositions = [
            { layer: 1, angle: 0 },
            { layer: 3, angle: Math.PI * 2 / 3 },
            { layer: 5, angle: Math.PI * 4 / 3 },
        ];

        densities.forEach((d, di) => {
            if (di >= densityPositions.length) return;
            const pos = densityPositions[di];
            const layerIdx = d.layer_index - 1;
            const sy = layerIdx * layerH + layerH / 2;
            const dx = Math.cos(pos.angle) * (tankRadius * 0.5);
            const dz = Math.sin(pos.angle) * (tankRadius * 0.5);

            const markerGeom = new THREE.OctahedronGeometry(0.12, 0);
            const densColor = new THREE.Color(this.densityToHex(d.value_kg_m3));
            const markerMat = new THREE.MeshPhongMaterial({
                color: 0xffffff,
                emissive: densColor,
                emissiveIntensity: 0.8,
            });
            const marker = new THREE.Mesh(markerGeom, markerMat);
            marker.position.set(dx, sy, dz);
            marker.userData = {
                sensorId: null,
                sensorType: 'density',
                layerIndex: d.layer_index,
                value: d.value_kg_m3,
            };
            this.tankGroup.add(marker);
            this.sensorMarkers.push(marker);
        });

        const shellGeom = new THREE.CylinderGeometry(tankRadius + 0.05, tankRadius + 0.05, tankHeight + 0.1, 32, 1, true);
        const shellMat = new THREE.MeshPhongMaterial({
            color: 0x4488aa,
            transparent: true,
            opacity: 0.12,
            side: THREE.DoubleSide,
            wireframe: false,
        });
        const shell = new THREE.Mesh(shellGeom, shellMat);
        shell.position.y = tankHeight / 2;
        this.tankGroup.add(shell);

        const roofGeom = new THREE.SphereGeometry(tankRadius + 0.05, 32, 16, 0, Math.PI * 2, 0, Math.PI / 2);
        const roofMat = new THREE.MeshPhongMaterial({
            color: 0x5599bb,
            transparent: true,
            opacity: 0.15,
            side: THREE.DoubleSide,
        });
        const roof = new THREE.Mesh(roofGeom, roofMat);
        roof.position.y = tankHeight;
        this.tankGroup.add(roof);

        const pressureMarkerGeom = new THREE.ConeGeometry(0.15, 0.3, 8);
        const pressureMarkerMat = new THREE.MeshPhongMaterial({
            color: 0x3b82f6,
            emissive: 0x1e40af,
            emissiveIntensity: 0.4,
        });
        const pressureMarker = new THREE.Mesh(pressureMarkerGeom, pressureMarkerMat);
        pressureMarker.position.set(0, tankHeight + 0.3, 0);
        pressureMarker.userData = {
            sensorType: 'pressure',
            value: data.pressure_kpa || 0,
        };
        this.tankGroup.add(pressureMarker);
        this.sensorMarkers.push(pressureMarker);

        this.tankGroup.position.set(0, 0, 0);
        this.scene.add(this.tankGroup);
    },

    onClick(event) {
        const container = document.getElementById('three-container');
        const rect = container.getBoundingClientRect();
        this.mouse.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
        this.mouse.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;

        this.raycaster.setFromCamera(this.mouse, this.camera);
        const intersects = this.raycaster.intersectObjects(this.sensorMarkers);

        if (intersects.length > 0) {
            const obj = intersects[0].object;
            const data = obj.userData;
            if (data.sensorId) {
                showSensorPopup(data.sensorId, data.sensorType, event.clientX, event.clientY);
            } else if (data.sensorType === 'density') {
                showDensityPopup(data.layerIndex, data.value, event.clientX, event.clientY);
            } else if (data.sensorType === 'pressure') {
                showPressurePopup(data.value, event.clientX, event.clientY);
            }
        }
    },

    onMouseMove(event) {
        const container = document.getElementById('three-container');
        const rect = container.getBoundingClientRect();
        this.mouse.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
        this.mouse.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;

        this.raycaster.setFromCamera(this.mouse, this.camera);
        const intersects = this.raycaster.intersectObjects(this.sensorMarkers);

        container.style.cursor = intersects.length > 0 ? 'pointer' : 'default';
    },

    animate() {
        this.animFrame = requestAnimationFrame(() => this.animate());
        this.renderer.render(this.scene, this.camera);
    },

    tempToHex(temp) {
        const t = Math.max(0, Math.min(1, (temp - (-170)) / ((-150) - (-170))));
        let r, g, b;
        if (t < 0.25) {
            const s = t / 0.25;
            r = Math.round(30 + (6 - 30) * s);
            g = Math.round(64 + (182 - 64) * s);
            b = Math.round(175 + (212 - 175) * s);
        } else if (t < 0.5) {
            const s = (t - 0.25) / 0.25;
            r = Math.round(6 + (16 - 6) * s);
            g = Math.round(182 + (185 - 182) * s);
            b = Math.round(212 + (129 - 212) * s);
        } else if (t < 0.75) {
            const s = (t - 0.5) / 0.25;
            r = Math.round(16 + (245 - 16) * s);
            g = Math.round(185 + (158 - 185) * s);
            b = Math.round(129 + (11 - 129) * s);
        } else {
            const s = (t - 0.75) / 0.25;
            r = Math.round(245 + (239 - 245) * s);
            g = Math.round(158 + (68 - 158) * s);
            b = Math.round(11 + (68 - 11) * s);
        }
        return (r << 16) | (g << 8) | b;
    },

    densityToHex(density) {
        const t = Math.max(0, Math.min(1, (density - 440) / 30));
        let r, g, b;
        if (t < 0.5) {
            const s = t / 0.5;
            r = Math.round(6 + (139 - 6) * s);
            g = Math.round(182 + (92 - 182) * s);
            b = Math.round(212 + (246 - 212) * s);
        } else {
            const s = (t - 0.5) / 0.5;
            r = Math.round(139 + (239 - 139) * s);
            g = Math.round(92 + (68 - 92) * s);
            b = Math.round(246 + (68 - 246) * s);
        }
        return (r << 16) | (g << 8) | b;
    }
};
