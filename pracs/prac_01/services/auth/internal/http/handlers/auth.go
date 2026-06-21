package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"prac_01/services/auth/internal/core"
	"prac_01/services/auth/internal/repo"
)

type AuthHandler struct {
	Users      *repo.UserRepo
	BcryptCost int
}

type registerReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResp struct {
	Status string      `json:"status"`
	User   interface{} `json:"user,omitempty"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var in registerReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json")
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "email_required_and_password_min_8")
		return
	}

	// bcrypt hash
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), h.BcryptCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash_failed")
		return
	}

	u := core.User{Email: in.Email, PasswordHash: string(hash)}
	if err := h.Users.Create(r.Context(), &u); err != nil {
		if err == repo.ErrEmailTaken {
			writeErr(w, http.StatusConflict, "email_taken")
			return
		}
		writeErr(w, http.StatusInternalServerError, "db_error")
		return
	}

	writeJSON(w, http.StatusCreated, authResp{
		Status: "ok",
		User:   map[string]any{"id": u.ID, "email": u.Email},
	})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var in loginReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json")
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || in.Password == "" {
		writeErr(w, http.StatusBadRequest, "email_and_password_required")
		return
	}

	u, err := h.Users.ByEmail(context.Background(), in.Email)
	if err != nil {
		// не раскрываем, что именно не так
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}

	// В ПЗ10 здесь будет генерация JWT; пока просто ok
	writeJSON(w, http.StatusOK, authResp{
		Status: "ok",
		User:   map[string]any{"id": u.ID, "email": u.Email},
	})
}

// helpers
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
