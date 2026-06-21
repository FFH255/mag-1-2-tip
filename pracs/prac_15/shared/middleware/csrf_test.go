package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func noopNext() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func testLogger() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l.WithField("service", "test")
}

func TestCSRF_SafeMethodPassesThrough(t *testing.T) {
	mw := CSRF(testLogger())(noopNext())

	req := httptest.NewRequest(http.MethodGet, "/v1/tasks", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for GET, got %d", rec.Code)
	}
}

func TestCSRF_PostWithoutCookieIs403(t *testing.T) {
	mw := CSRF(testLogger())(noopNext())

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without cookie, got %d", rec.Code)
	}
}

func TestCSRF_PostWithoutHeaderIs403(t *testing.T) {
	mw := CSRF(testLogger())(noopNext())

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 without header, got %d", rec.Code)
	}
}

func TestCSRF_PostWithMismatchIs403(t *testing.T) {
	mw := CSRF(testLogger())(noopNext())

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "xyz")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 on mismatch, got %d", rec.Code)
	}
}

func TestCSRF_PostWithMatchingTokenPasses(t *testing.T) {
	mw := CSRF(testLogger())(noopNext())

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader("{}"))
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "abc")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 when tokens match, got %d", rec.Code)
	}
}

func TestSecurityHeaders_SetsExpectedHeaders(t *testing.T) {
	mw := SecurityHeaders(noopNext())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	cases := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
	}
	for h, want := range cases {
		if got := rec.Header().Get(h); got != want {
			t.Errorf("%s: want %q, got %q", h, want, got)
		}
	}
}
