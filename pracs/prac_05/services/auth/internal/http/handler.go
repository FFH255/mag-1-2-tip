package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"

	"prac_05/services/auth/internal/service"
	"prac_05/shared/httpx"
	"prac_05/shared/middleware"
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
