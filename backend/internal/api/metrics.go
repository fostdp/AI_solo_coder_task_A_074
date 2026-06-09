package api

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"sync/atomic"
	"time"
)

var (
	metricsRequests     atomic.Int64
	metricsWsConnects   atomic.Int64
	metricsAlarmsFired  atomic.Int64
	metricsPollerCycles atomic.Int64
	metricsPredictions  atomic.Int64
	metricsStartTime    time.Time
)

func init() {
	metricsStartTime = time.Now()
}

func IncRequests()     { metricsRequests.Add(1) }
func IncWsConnects()   { metricsWsConnects.Add(1) }
func IncAlarms()       { metricsAlarmsFired.Add(1) }
func IncPollerCycles() { metricsPollerCycles.Add(1) }
func IncPredictions()  { metricsPredictions.Add(1) }

func registerMetricsAndPprof(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/metrics", handlePrometheus)
}

func handlePrometheus(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(metricsStartTime).Seconds()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP lng_http_requests_total Total HTTP requests processed\n")
	fmt.Fprintf(w, "# TYPE lng_http_requests_total counter\n")
	fmt.Fprintf(w, "lng_http_requests_total %d\n", metricsRequests.Load())
	fmt.Fprintf(w, "# HELP lng_ws_connections_total Total WebSocket connections\n")
	fmt.Fprintf(w, "# TYPE lng_ws_connections_total counter\n")
	fmt.Fprintf(w, "lng_ws_connections_total %d\n", metricsWsConnects.Load())
	fmt.Fprintf(w, "# HELP lng_alarms_fired_total Total alarms fired\n")
	fmt.Fprintf(w, "# TYPE lng_alarms_fired_total counter\n")
	fmt.Fprintf(w, "lng_alarms_fired_total %d\n", metricsAlarmsFired.Load())
	fmt.Fprintf(w, "# HELP lng_poller_cycles_total Total Modbus poller cycles\n")
	fmt.Fprintf(w, "# TYPE lng_poller_cycles_total counter\n")
	fmt.Fprintf(w, "lng_poller_cycles_total %d\n", metricsPollerCycles.Load())
	fmt.Fprintf(w, "# HELP lng_predictions_total Total FVM predictions computed\n")
	fmt.Fprintf(w, "# TYPE lng_predictions_total counter\n")
	fmt.Fprintf(w, "lng_predictions_total %d\n", metricsPredictions.Load())
	fmt.Fprintf(w, "# HELP lng_uptime_seconds System uptime in seconds\n")
	fmt.Fprintf(w, "# TYPE lng_uptime_seconds gauge\n")
	fmt.Fprintf(w, "lng_uptime_seconds %.0f\n", uptime)
	fmt.Fprintf(w, "# HELP lng_build_info Build information\n")
	fmt.Fprintf(w, "# TYPE lng_build_info gauge\n")
	fmt.Fprintf(w, "lng_build_info{version=\"fvm-v1.2\",arch=\"amd64\"} 1\n")
}
