package handlers

import (
	"encoding/json"
	"net/http"

	"prac_01/services/auth/internal/service"
	"prac_01/shared/httpx"
)

type Handler struct {
	auth *service.AuthService
}

func NewHandler(auth *service.AuthService) *Handler {
	return &Handler{auth: auth}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/login", h.Login)
	mux.HandleFunc("GET /v1/auth/verify", h.Verify)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req service.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json")
		return
	}

	resp, ok := h.auth.Login(req)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	resp := h.auth.Verify(r.Header.Get("Authorization"))
	if !resp.Valid {
		httpx.WriteJSON(w, http.StatusUnauthorized, resp)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}
