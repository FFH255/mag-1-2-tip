package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/sirupsen/logrus"

	"prac_08/shared/httpx"
)

const (
	CSRFCookieName = "csrf_token"
	CSRFHeaderName = "X-CSRF-Token"
)

// CSRF реализует Double Submit Cookie защиту от CSRF.
// На state-changing запросы (POST/PUT/PATCH/DELETE) проверяется, что значение
// cookie csrf_token совпадает с заголовком X-CSRF-Token. Если нет — 403.
//
// GET/HEAD/OPTIONS пропускаются: они не должны менять состояние сервера.
func CSRF(log *logrus.Entry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isStateChanging(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			entry := log.WithFields(logrus.Fields{
				"request_id": GetRequestID(r.Context()),
				"method":     r.Method,
				"path":       r.URL.Path,
			})

			cookie, err := r.Cookie(CSRFCookieName)
			if err != nil || cookie.Value == "" {
				entry.Warn("csrf: missing csrf_token cookie")
				httpx.WriteError(w, http.StatusForbidden, "csrf token missing")
				return
			}

			header := r.Header.Get(CSRFHeaderName)
			if header == "" {
				entry.Warn("csrf: missing X-CSRF-Token header")
				httpx.WriteError(w, http.StatusForbidden, "csrf token missing")
				return
			}

			// constant-time сравнение — не даём атакующему измерить время.
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
				entry.Warn("csrf: token mismatch")
				httpx.WriteError(w, http.StatusForbidden, "csrf token mismatch")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
