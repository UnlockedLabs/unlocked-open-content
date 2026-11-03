package opencontent

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Open Content's request metrics, served on /api/metrics. They have core's
// names, labels and buckets, so the queries that read core's also read these.
// The path label is the route's pattern, not the request's path: every page a
// library proxies would otherwise be a new series.
var (
	requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Histogram of response latency (seconds) for handler.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		},
		[]string{"path"},
	)
	requestSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_size_bytes",
			Help:    "Histogram of request size (bytes).",
			Buckets: prometheus.ExponentialBuckets(100, 10, 5),
		},
		[]string{"path"},
	)
	responseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "Histogram of response size (bytes).",
			Buckets: prometheus.ExponentialBuckets(100, 10, 5),
		},
		[]string{"path"},
	)
	requestCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Number of HTTP requests processed",
		},
		[]string{"path"},
	)
	responseStatus = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_response_status_total",
			Help: "Count of HTTP responses by status code, method, and path",
		},
		[]string{"path", "method", "status_code"},
	)
	errorCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_errors_total",
			Help: "Number of HTTP errors",
		},
		[]string{"route", "method", "status_code"},
	)
)

// observe records each request to the route with pattern, as core's
// prometheusMiddleware does, including the ones its checks refuse.
func observe(pattern string, next http.Handler) http.Handler {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		path = pattern
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := prometheus.NewTimer(requestDuration.WithLabelValues(path))
		defer timer.ObserveDuration()
		requestSize.WithLabelValues(path).Observe(float64(r.ContentLength))

		rw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		responseSize.WithLabelValues(path).Observe(float64(rw.size))
		requestCount.WithLabelValues(path).Inc()
		statusCode := strconv.Itoa(rw.statusCode)
		if rw.statusCode >= 400 {
			errorCount.WithLabelValues(path, r.Method, statusCode).Inc()
		}
		responseStatus.WithLabelValues(path, r.Method, statusCode).Inc()
	})
}

// statusWriter records the status and size of a response.
type statusWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (w *statusWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.size += n
	return n, err
}

// Unwrap lets http.ResponseController reach the real writer, so the library
// proxy can still flush.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
