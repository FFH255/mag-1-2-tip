package middleware

import (
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// AccessLog returns middleware that logs every completed HTTP request
// with the standard set of fields: request_id, method, path, status, duration_ms.
func AccessLog(log *logrus.Entry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			duration := time.Since(start)

			entry := log.WithFields(logrus.Fields{
				"request_id":  GetRequestID(r.Context()),
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      rec.status,
				"duration_ms": duration.Milliseconds(),
			})

			if rec.status >= 500 {
				entry.Error("request completed")
			} else if rec.status >= 400 {
				entry.Warn("request completed")
			} else {
				entry.Info("request completed")
			}
		})
	}
}
