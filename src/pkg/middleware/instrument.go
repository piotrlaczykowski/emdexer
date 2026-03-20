package middleware

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_http_requests_total",
	Help: "Total number of HTTP requests",
}, []string{"path", "code"})

// Instrument wraps an HTTP handler to track request counts by path and status code.
func Instrument(path string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		httpRequestsTotal.WithLabelValues(path, fmt.Sprintf("%d", rw.status)).Inc()
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
