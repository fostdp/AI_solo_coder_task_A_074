const AlarmHandler = {
    activeAlarms: [],
    bannerTimer: null,

    onAlarm(alarm) {
        this.activeAlarms.push(alarm);
        this.updateAlarmSummary();
        this.showBanner(alarm);
        this.playAlarmSound(alarm.alarm_level);
    },

    updateAlarmSummary() {
        const container = document.getElementById('alarm-summary');
        const level1 = this.activeAlarms.filter(a => a.alarm_level === 1).length;
        const level2 = this.activeAlarms.filter(a => a.alarm_level === 2).length;

        container.innerHTML = `
            <div class="summary-item">
                <span style="color:#f59e0b">一级预警</span>
                <span style="color:#f59e0b;font-weight:600">${level1}</span>
            </div>
            <div class="summary-item">
                <span style="color:#ef4444">二级告警</span>
                <span style="color:#ef4444;font-weight:600">${level2}</span>
            </div>
        `;
    },

    showBanner(alarm) {
        const banner = document.getElementById('alarm-banner');
        banner.className = `alarm-banner level${alarm.alarm_level}`;
        banner.textContent = alarm.message || `[Level ${alarm.alarm_level}] 告警`;

        if (this.bannerTimer) {
            clearTimeout(this.bannerTimer);
        }

        this.bannerTimer = setTimeout(() => {
            banner.classList.add('hidden');
        }, 10000);
    },

    playAlarmSound(level) {
        try {
            const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
            const oscillator = audioCtx.createOscillator();
            const gainNode = audioCtx.createGain();

            oscillator.connect(gainNode);
            gainNode.connect(audioCtx.destination);

            if (level === 2) {
                oscillator.frequency.setValueAtTime(880, audioCtx.currentTime);
                oscillator.frequency.setValueAtTime(440, audioCtx.currentTime + 0.2);
                oscillator.frequency.setValueAtTime(880, audioCtx.currentTime + 0.4);
            } else {
                oscillator.frequency.setValueAtTime(660, audioCtx.currentTime);
            }

            gainNode.gain.setValueAtTime(0.1, audioCtx.currentTime);
            gainNode.gain.exponentialRampToValueAtTime(0.01, audioCtx.currentTime + 0.8);

            oscillator.start(audioCtx.currentTime);
            oscillator.stop(audioCtx.currentTime + 0.8);
        } catch (e) {
            console.warn('音频告警失败:', e);
        }
    },

    async acknowledgeAlarm(alarmId, user) {
        try {
            await fetch(`${App.API_BASE}/api/alarm/${alarmId}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ user: user || 'operator' }),
            });
            this.activeAlarms = this.activeAlarms.filter(a => a.id !== alarmId);
            this.updateAlarmSummary();
        } catch (e) {
            console.error('确认告警失败:', e);
        }
    }
};
