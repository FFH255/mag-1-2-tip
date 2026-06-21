package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"prac_08/services/auth/internal/service"
	"prac_08/shared/httpx"
	"prac_08/shared/middleware"
)

const (
	sessionCookieName = "session"
	csrfCookieName    = "csrf_token"
	cookieMaxAge      = 3600
)

type Handler struct {
	auth *service.AuthService
	log  *logrus.Entry
}

func NewHandler(auth *service.AuthService, log *logrus.Entry) *Handler {
	return &Handler{auth: auth, log: log.WithField("component", "handler")}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/login", h.Login)
	mux.HandleFunc("GET /v1/auth/verify", h.Verify)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	var req service.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WithError(err).Warn("login: invalid json")
		httpx.WriteError(w, http.StatusBadRequest, "invalid json")
		return
	}

	resp, ok := h.auth.Login(req)
	if !ok {
		log.WithField("username", req.Username).Warn("login: invalid credentials")
		httpx.WriteError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	csrfToken, err := newCSRFToken()
	if err != nil {
		log.WithError(err).Error("login: csrf token generation failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	expires := time.Now().Add(time.Duration(cookieMaxAge) * time.Second)

	// HttpOnly: JS cookie from session can't be read from JS (mitigates XSS token theft).
	// Secure: sent only over HTTPS. SameSite=Lax: mitigates cross-site cookie attach.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    resp.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge,
		Expires:  expires,
	})

	// CSRF cookie is NOT HttpOnly so the frontend JS can read it
	// and send it back in the X-CSRF-Token header (double-submit pattern).
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge,
		Expires:  expires,
	})

	log.WithField("username", req.Username).Info("login successful")
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	hasAuth := r.Header.Get("Authorization") != ""
	resp := h.auth.Verify(r.Header.Get("Authorization"))

	if !resp.Valid {
		log.WithField("has_auth", hasAuth).Warn("verify: unauthorized")
		httpx.WriteJSON(w, http.StatusUnauthorized, resp)
		return
	}

	log.WithField("has_auth", hasAuth).Info("verify successful")
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func newCSRFToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
