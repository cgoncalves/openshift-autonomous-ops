package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
		[]string{"handler", "method"},
	)
	concurrentRequests atomic.Int64
)

func init() {
	prometheus.MustRegister(requestDuration)
}

func main() {
	http.HandleFunc("/api/data", instrumentHandler("data", dataHandler))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	http.Handle("/metrics", promhttp.Handler())

	fmt.Println("Sample app listening on :8080")
	http.ListenAndServe(":8080", nil)
}

func instrumentHandler(name string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler(w, r)
		requestDuration.WithLabelValues(name, r.Method).Observe(time.Since(start).Seconds())
	}
}

func dataHandler(w http.ResponseWriter, r *http.Request) {
	active := concurrentRequests.Add(1)
	defer concurrentRequests.Add(-1)

	base := 10 * time.Millisecond
	contention := time.Duration(active*3) * time.Millisecond
	jitter := time.Duration(rand.Intn(20)) * time.Millisecond
	time.Sleep(base + contention + jitter)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","active_connections":%d}`, active)
}
