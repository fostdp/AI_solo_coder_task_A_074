package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lng-monitor/internal/config"
	"lng-monitor/internal/database"
	"lng-monitor/internal/models"

	"github.com/gorilla/websocket"
)

type Server struct {
	cfg        *config.Config
	db         *database.DB
	wsHub      *WSHub
	staticDir  string
}

type WSHub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients: make(map[*websocket.Conn]bool),
	}
}

func (h *WSHub) Add(conn *websocket.Conn) {
	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()
}

func (h *WSHub) Remove(conn *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, conn)
	h.mu.Unlock()
}

func (h *WSHub) Broadcast(msg models.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			go func(c *websocket.Conn) {
				h.Remove(c)
				c.Close()
			}(conn)
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func NewServer(cfg *config.Config, db *database.DB) *Server {
	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		exePath, _ := os.Executable()
		staticDir = filepath.Join(filepath.Dir(exePath), "..", "frontend", "static")
	}

	return &Server{
		cfg:       cfg,
		db:        db,
		wsHub:     NewWSHub(),
		staticDir: staticDir,
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/tanks", s.wrapWithMetrics(s.handleGetTanks))
	mux.HandleFunc("/api/tank/", s.wrapWithMetrics(s.handleTankData))
	mux.HandleFunc("/api/sensor/", s.wrapWithMetrics(s.handleSensorTrend))
	mux.HandleFunc("/api/alarm/", s.wrapWithMetrics(s.handleAlarmAction))
	mux.HandleFunc("/api/alarms/", s.wrapWithMetrics(s.handleTankAlarms))
	mux.HandleFunc("/api/prediction/", s.wrapWithMetrics(s.handlePrediction))
	mux.HandleFunc("/ws", s.handleWebSocket)

	registerMetricsAndPprof(mux)

	mux.HandleFunc("/", s.serveFrontend)

	handler := gzipMiddleware(mux)

	addr := fmt.Sprintf(":%d", s.cfg.HTTPPort)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	pprofAddr := ":6060"
	pprofmux := http.NewServeMux()
	pprofmux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><h1>pprof</h1><a href="/debug/pprof/goroutine">goroutine</a><br><a href="/debug/pprof/heap">heap</a><br><a href="/debug/pprof/profile?seconds=30">profile (30s)</a><br><a href="/debug/pprof/trace">trace</a></body></html>`)
	})
	pprofSrv := &http.Server{Addr: pprofAddr, Handler: pprofmux}
	go pprofSrv.ListenAndServe()
	log.Printf("pprof server on %s", pprofAddr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		pprofSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("API server starting on %s (gzip enabled)", addr)
	return srv.ListenAndServe()
}

func (s *Server) wrapWithMetrics(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		IncRequests()
		fn(w, r)
	}
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gw *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.gw.Write(b)
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(statusCode)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		if isGzipExcluded(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()

		gzw := &gzipResponseWriter{ResponseWriter: w, gw: gz}
		next.ServeHTTP(gzw, r)
	})
}

func isGzipExcluded(path string) bool {
	return strings.HasPrefix(path, "/ws") ||
		strings.HasPrefix(path, "/debug/") ||
		strings.HasPrefix(path, "/metrics")
}

var _ io.Writer = (*gzipResponseWriter)(nil)

func (s *Server) BroadcastMessage(msg models.WSMessage) {
	s.wsHub.Broadcast(msg)
}

func (s *Server) handleGetTanks(w http.ResponseWriter, r *http.Request) {
	tanks, err := s.db.GetTanks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, tanks)
}

func (s *Server) handleTankData(w http.ResponseWriter, r *http.Request) {
	tankID, err := strconv.Atoi(pathParam(r, 0))
	if err != nil {
		http.Error(w, "invalid tank_id", 400)
		return
	}

	tanks, _ := s.db.GetTanks(r.Context())
	var tank models.Tank
	for _, t := range tanks {
		if t.ID == tankID {
			tank = t
			break
		}
	}

	temps, _ := s.db.GetLatestTemperatures(r.Context(), tankID)
	densities, _ := s.db.GetLatestDensities(r.Context(), tankID)
	pressure, _ := s.db.GetLatestPressure(r.Context(), tankID)
	bogStatus, _ := s.db.GetLatestBOGStatus(r.Context(), tankID)
	prediction, _ := s.db.GetLatestPrediction(r.Context(), tankID)
	alarms, _ := s.db.GetRecentAlarms(r.Context(), tankID, 10)

	layerTemps := buildLayerTemps(temps)
	layerDens := buildLayerDens(densities)

	snapshot := models.TankSnapshot{
		TankID:       tankID,
		TankCode:     tank.TankCode,
		Temperatures: layerTemps,
		Densities:    layerDens,
		Pressure:     pressure,
		BOGRunning:   bogStatus.Running,
		RiskIndex:    prediction.RiskIndex,
		Alarms:       alarms,
	}

	writeJSON(w, snapshot)
}

func (s *Server) handleSensorTrend(w http.ResponseWriter, r *http.Request) {
	sensorID, err := strconv.Atoi(pathParam(r, 0))
	if err != nil {
		http.Error(w, "invalid sensor_id", 400)
		return
	}

	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil {
			hours = v
		}
	}

	sensors, _ := s.db.GetSensorsByTank(r.Context(), 1)
	var sensor models.Sensor
	found := false
	for _, sn := range sensors {
		if sn.ID == sensorID {
			sensor = sn
			found = true
			break
		}
	}
	if !found {
		for tid := 2; tid <= 4; tid++ {
			sensors, _ = s.db.GetSensorsByTank(r.Context(), tid)
			for _, sn := range sensors {
				if sn.ID == sensorID {
					sensor = sn
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}

	var points []models.TrendPoint
	if found && sensor.SensorType == "temperature" {
		points, _ = s.db.GetTemperatureTrend(r.Context(), sensorID, hours)
	} else if found && sensor.SensorType == "density" {
		points, _ = s.db.GetDensityTrend(r.Context(), sensorID, hours)
	}

	trend := models.TrendData{
		SensorID:   sensorID,
		SensorType: sensor.SensorType,
		TankID:     sensor.TankID,
		Points:     points,
	}
	writeJSON(w, trend)
}

func (s *Server) handleAlarmAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	alarmID, err := strconv.Atoi(pathParam(r, 0))
	if err != nil {
		http.Error(w, "invalid alarm_id", 400)
		return
	}

	var body struct {
		User string `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}

	if err := s.db.AcknowledgeAlarm(r.Context(), alarmID, body.User); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"status": "acknowledged"})
}

func (s *Server) handleTankAlarms(w http.ResponseWriter, r *http.Request) {
	tankID, err := strconv.Atoi(pathParam(r, 0))
	if err != nil {
		http.Error(w, "invalid tank_id", 400)
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}

	alarms, err := s.db.GetRecentAlarms(r.Context(), tankID, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, alarms)
}

func (s *Server) handlePrediction(w http.ResponseWriter, r *http.Request) {
	tankID, err := strconv.Atoi(pathParam(r, 0))
	if err != nil {
		http.Error(w, "invalid tank_id", 400)
		return
	}

	prediction, err := s.db.GetLatestPrediction(r.Context(), tankID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, prediction)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	s.wsHub.Add(conn)

	go func() {
		defer func() {
			s.wsHub.Remove(conn)
			conn.Close()
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}

func (s *Server) serveFrontend(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	filePath := filepath.Join(s.staticDir, path)
	if strings.HasPrefix(filePath, s.staticDir) {
		if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
			http.ServeFile(w, r, filePath)
			return
		}
	}

	http.NotFound(w, r)
}

func pathParam(r *http.Request, index int) string {
	parts := splitPath(r.URL.Path)
	baseIdx := 0
	switch {
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "tank":
		baseIdx = 2
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "sensor":
		baseIdx = 2
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "alarm":
		baseIdx = 2
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "alarms":
		baseIdx = 2
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "prediction":
		baseIdx = 2
	}

	targetIdx := baseIdx + index
	if targetIdx < len(parts) {
		return parts[targetIdx]
	}
	return ""
}

func splitPath(p string) []string {
	var parts []string
	for _, s := range split(p, '/') {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

func split(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func buildLayerTemps(temps []models.TemperatureReading) []models.LayerTemperature {
	layerMap := make(map[int]*models.LayerTemperature)
	for _, t := range temps {
		li := t.LayerIndex
		if _, ok := layerMap[li]; !ok {
			layerMap[li] = &models.LayerTemperature{
				LayerIndex: li,
				Sensors:    []models.SensorValue{},
			}
		}
		lm := layerMap[li]
		lm.Sensors = append(lm.Sensors, models.SensorValue{
			SensorID:      t.SensorID,
			PositionIndex: t.PositionIndex,
			Value:         t.ValueCelsius,
		})
	}

	var result []models.LayerTemperature
	for i := 1; i <= 5; i++ {
		if lm, ok := layerMap[i]; ok {
			sum := 0.0
			minV := lm.Sensors[0].Value
			maxV := lm.Sensors[0].Value
			for _, sv := range lm.Sensors {
				sum += sv.Value
				if sv.Value < minV {
					minV = sv.Value
				}
				if sv.Value > maxV {
					maxV = sv.Value
				}
			}
			lm.AvgTemp = sum / float64(len(lm.Sensors))
			lm.MinTemp = minV
			lm.MaxTemp = maxV
			result = append(result, *lm)
		}
	}
	return result
}

func buildLayerDens(densities []models.DensityReading) []models.LayerDensity {
	var result []models.LayerDensity
	for _, d := range densities {
		result = append(result, models.LayerDensity{
			LayerIndex: d.LayerIndex,
			ValueKgM3:  d.ValueKgM3,
		})
	}
	return result
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
