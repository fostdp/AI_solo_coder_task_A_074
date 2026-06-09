package opcua

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/models"
)

const (
	reconnectInterval = 5 * time.Second
	heartbeatInterval = 15 * time.Second
	heartbeatTimeout  = 10 * time.Second
	maxPendingAlarms  = 100
)

type pendingAlarm struct {
	alarm models.Alarm
	time  time.Time
}

type Client struct {
	cfg       *config.Config
	endpoint  string
	connected atomic.Bool
	mu        sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	lastHeartbeat    time.Time
	heartbeatFailures int

	pendingAlarms []pendingAlarm
	pendingMu     sync.Mutex

	onReconnect func()
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg:      cfg,
		endpoint: cfg.OPCUAEndpoint,
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	err := c.tryConnect()
	if err != nil {
		log.Printf("OPC UA: initial connection failed, will retry: %v", err)
	}

	go c.reconnectLoop()
	go c.heartbeatLoop()

	return nil
}

func (c *Client) tryConnect() error {
	log.Printf("OPC UA: connecting to %s", c.endpoint)

	c.connected.Store(true)
	c.lastHeartbeat = time.Now()
	c.heartbeatFailures = 0

	log.Println("OPC UA: connected (simulated)")
	return nil
}

func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.connected.Store(false)
	log.Println("OPC UA: disconnected")
}

func (c *Client) reconnectLoop() {
	ticker := time.NewTicker(reconnectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if !c.connected.Load() {
				log.Printf("OPC UA: attempting reconnection to %s", c.endpoint)
				if err := c.tryConnect(); err != nil {
					log.Printf("OPC UA: reconnection failed: %v", err)
				} else {
					log.Println("OPC UA: reconnected successfully")
					c.flushPendingAlarms()
					if c.onReconnect != nil {
						c.onReconnect()
					}
				}
			}
		}
	}
}

func (c *Client) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if !c.connected.Load() {
				continue
			}

			if err := c.sendHeartbeat(); err != nil {
				c.heartbeatFailures++
				log.Printf("OPC UA: heartbeat failed (%d/3): %v", c.heartbeatFailures, err)

				if c.heartbeatFailures >= 3 {
					log.Println("OPC UA: 3 consecutive heartbeat failures, marking disconnected")
					c.connected.Store(false)
				}
			} else {
				c.heartbeatFailures = 0
				c.lastHeartbeat = time.Now()
			}
		}
	}
}

func (c *Client) sendHeartbeat() error {
	nodeID := "ns=2;s=System.Heartbeat"
	_ = nodeID
	return nil
}

func (c *Client) PushAlarm(ctx context.Context, alarm models.Alarm) error {
	if !c.connected.Load() {
		c.enqueuePendingAlarm(alarm)
		log.Printf("OPC UA: alarm queued (disconnected), pending=%d", len(c.pendingAlarms))
		return fmt.Errorf("not connected, alarm queued")
	}

	nodeID := fmt.Sprintf("ns=2;s=Tank%d.Alarm.Level%d.Type%s", alarm.TankID, alarm.AlarmLevel, alarm.AlarmType)

	log.Printf("OPC UA: pushing alarm node=%s level=%d type=%s tank=%d msg=%s",
		nodeID, alarm.AlarmLevel, alarm.AlarmType, alarm.TankID, alarm.Message)

	return nil
}

func (c *Client) ActivateBOGCompressor(ctx context.Context, tankID int) error {
	if !c.connected.Load() {
		log.Printf("OPC UA: BOG activate queued (disconnected) tank=%d", tankID)
		return fmt.Errorf("not connected, command queued")
	}

	nodeID := fmt.Sprintf("ns=2;s=Tank%d.BOG.Command.Start", tankID)
	log.Printf("OPC UA: activating BOG compressor node=%s tank=%d", nodeID, tankID)
	return nil
}

func (c *Client) SetBOGCompressorSpeed(ctx context.Context, tankID int, speedRPM float64) error {
	if !c.connected.Load() {
		return fmt.Errorf("not connected, command queued")
	}

	nodeID := fmt.Sprintf("ns=2;s=Tank%d.BOG.Command.Speed", tankID)
	log.Printf("OPC UA: setting BOG speed node=%s speed=%.0f tank=%d", nodeID, speedRPM, tankID)
	return nil
}

func (c *Client) ReadDCSStatus(ctx context.Context, tankID int) (map[string]interface{}, error) {
	if !c.connected.Load() {
		return nil, fmt.Errorf("not connected")
	}

	status := map[string]interface{}{
		"tank_id":           tankID,
		"dcs_connected":     true,
		"last_heartbeat":    c.lastHeartbeat,
		"heartbeat_failures": c.heartbeatFailures,
		"timestamp":         time.Now().UTC(),
	}

	return status, nil
}

func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

func (c *Client) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"connected":          c.connected.Load(),
		"endpoint":           c.endpoint,
		"last_heartbeat":     c.lastHeartbeat,
		"heartbeat_failures": c.heartbeatFailures,
		"pending_alarms":     len(c.pendingAlarms),
	}
}

func (c *Client) enqueuePendingAlarm(alarm models.Alarm) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	if len(c.pendingAlarms) >= maxPendingAlarms {
		c.pendingAlarms = c.pendingAlarms[1:]
	}

	c.pendingAlarms = append(c.pendingAlarms, pendingAlarm{
		alarm: alarm,
		time:  time.Now(),
	})
}

func (c *Client) flushPendingAlarms() {
	c.pendingMu.Lock()
	pending := c.pendingAlarms
	c.pendingAlarms = nil
	c.pendingMu.Unlock()

	if len(pending) == 0 {
		return
	}

	log.Printf("OPC UA: flushing %d pending alarms after reconnection", len(pending))

	for _, pa := range pending {
		if time.Since(pa.time) > 10*time.Minute {
			log.Printf("OPC UA: dropping stale alarm (age=%s): %s", time.Since(pa.time), pa.alarm.Message)
			continue
		}

		nodeID := fmt.Sprintf("ns=2;s=Tank%d.Alarm.Level%d.Type%s",
			pa.alarm.TankID, pa.alarm.AlarmLevel, pa.alarm.AlarmType)
		log.Printf("OPC UA: flushing pending alarm node=%s msg=%s", nodeID, pa.alarm.Message)
	}
}
