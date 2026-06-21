package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTPMetrics bundles the three основных метрики HTTP-сервиса:
// счётчик запросов, гистограмма длительности и gauge активных запросов.
type HTTPMetrics struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	InFlight        prometheus.Gauge
}

// NewHTTPMetrics регистрирует метрики в переданном registry.
// Если registry == nil, используется prometheus.DefaultRegisterer.
func NewHTTPMetrics(service string, reg prometheus.Registerer) *HTTPMetrics {
	factory := promauto.With(reg)

	return &HTTPMetrics{
		RequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name:        "http_requests_total",
				Help:        "Total number of HTTP requests processed by the service.",
				ConstLabels: prometheus.Labels{"service": service},
			},
			[]string{"method", "route", "status"},
		),
		RequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        "http_request_duration_seconds",
				Help:        "HTTP request duration in seconds.",
				Buckets:     []float64{0.01, 0.05, 0.1, 0.3, 1, 3},
				ConstLabels: prometheus.Labels{"service": service},
			},
			[]string{"method", "route"},
		),
		InFlight: factory.NewGauge(
			prometheus.GaugeOpts{
				Name:        "http_in_flight_requests",
				Help:        "Current number of in-flight HTTP requests.",
				ConstLabels: prometheus.Labels{"service": service},
			},
		),
	}
}
