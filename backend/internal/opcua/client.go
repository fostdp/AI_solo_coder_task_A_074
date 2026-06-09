package opcua

import (
	"context"
	"fmt"
	"log"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/models"
)

type Client struct {
	cfg      *config.Config
	endpoint string
	connected bool
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg:      cfg,
		endpoint: cfg.OPCUAEndpoint,
	}
}

func (c *Client) Connect(ctx context.Context) error {
	log.Printf("OPC UA: connecting to %s", c.endpoint)

	c.connected = true
	log.Println("OPC UA: connected (simulated)")
	return nil
}

func (c *Client) Close() {
	c.connected = false
	log.Println("OPC UA: disconnected")
}

func (c *Client) PushAlarm(ctx context.Context, alarm models.Alarm) error {
	if !c.connected {
		log.Println("OPC UA: not connected, alarm not pushed")
		return fmt.Errorf("not connected")
	}

	nodeID := fmt.Sprintf("ns=2;s=Tank%d.Alarm.Level%d.Type%s", alarm.TankID, alarm.AlarmLevel, alarm.AlarmType)

	log.Printf("OPC UA: pushing alarm node=%s level=%d type=%s tank=%d msg=%s",
		nodeID, alarm.AlarmLevel, alarm.AlarmType, alarm.TankID, alarm.Message)

	return nil
}

func (c *Client) ActivateBOGCompressor(ctx context.Context, tankID int) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}

	nodeID := fmt.Sprintf("ns=2;s=Tank%d.BOG.Command.Start", tankID)

	log.Printf("OPC UA: activating BOG compressor node=%s tank=%d", nodeID, tankID)

	return nil
}

func (c *Client) SetBOGCompressorSpeed(ctx context.Context, tankID int, speedRPM float64) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}

	nodeID := fmt.Sprintf("ns=2;s=Tank%d.BOG.Command.Speed", tankID)

	log.Printf("OPC UA: setting BOG speed node=%s speed=%.0f tank=%d", nodeID, speedRPM, tankID)

	return nil
}

func (c *Client) ReadDCSStatus(ctx context.Context, tankID int) (map[string]interface{}, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected")
	}

	status := map[string]interface{}{
		"tank_id":       tankID,
		"dcs_connected": true,
		"timestamp":     time.Now().UTC(),
	}

	return status, nil
}
