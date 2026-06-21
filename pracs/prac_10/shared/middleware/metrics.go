package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"prac_10/shared/metrics"
)

// RouteClassifier превращает конкретный URL-путь (например /v1/tasks/123)
// в шаблон маршрута (/v1/tasks/:id), чтобы избежать взрыва кардинальности
// меток Prometheus.
type RouteClassifier func(method, path string) string

// Metrics возвращает middleware, которое на каждый HTTP-запрос увеличивает
// InFlight, после завершения инкрементирует RequestsTotal и наблюдает
// длительность в гистограмме.
func Metrics(m *metrics.HTTPMetrics, classify RouteClassifier) func(http.Handler) http.Handler {
	if classify == nil {
		classify = func(_, path string) string { return path }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := classify(r.Method, r.URL.Path)

			m.InFlight.Inc()
			defer m.InFlight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			start := time.Now()
			next.ServeHTTP(rec, r)
			duration := time.Since(start).Seconds()

			m.RequestDuration.WithLabelValues(r.Method, route).Observe(duration)
			m.RequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		})
	}
}

// TasksRouteClassifier нормализует пути сервиса tasks:
//
//	/v1/tasks            -> /v1/tasks
//	/v1/tasks/search     -> /v1/tasks/search
//	/v1/tasks/123        -> /v1/tasks/:id
//	/metrics             -> /metrics
//	/health              -> /health
//	всё остальное        -> other
func TasksRouteClassifier(_, path string) string {
	switch {
	case path == "/metrics":
		return "/metrics"
	case path == "/health":
		return "/health"
	case path == "/v1/tasks":
		return "/v1/tasks"
	case path == "/v1/tasks/search":
		return "/v1/tasks/search"
	case strings.HasPrefix(path, "/v1/tasks/"):
		return "/v1/tasks/:id"
	default:
		return "other"
	}
}
