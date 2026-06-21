package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"prac_02/services/tasks/internal/client/authclient"
	"prac_02/services/tasks/internal/service"
	"prac_02/shared/httpx"
	"prac_02/shared/middleware"
)

type Handler struct {
	tasks *service.TaskService
	auth  *authclient.Client
}

func NewHandler(tasks *service.TaskService, auth *authclient.Client) *Handler {
	return &Handler{tasks: tasks, auth: auth}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/tasks", h.CreateTask)
	mux.HandleFunc("GET /v1/tasks", h.ListTasks)
	mux.HandleFunc("GET /v1/tasks/", h.GetTask)
	mux.HandleFunc("PATCH /v1/tasks/", h.UpdateTask)
	mux.HandleFunc("DELETE /v1/tasks/", h.DeleteTask)
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	err := h.auth.Verify(
		r.Context(),
		r.Header.Get("Authorization"),
		middleware.GetRequestID(r.Context()),
	)
	if err == nil {
		return true
	}

	if errors.Is(err, authclient.ErrUnauthorized) {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if errors.Is(err, authclient.ErrAuthUnavailable) {
		httpx.WriteError(w, http.StatusBadGateway, "auth service unavailable")
		return false
	}

	httpx.WriteError(w, http.StatusInternalServerError, "internal error")
	return false
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	var req service.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json")
		return
	}

	task, err := h.tasks.Create(req)
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.WriteError(w, http.StatusBadRequest, "title is required")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, task)
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	httpx.WriteJSON(w, http.StatusOK, h.tasks.List())
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	id := taskIDFromPath(r.URL.Path)
	if id == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	task, err := h.tasks.Get(id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "task not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, task)
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	id := taskIDFromPath(r.URL.Path)
	if id == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	var req service.UpdateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json")
		return
	}

	task, err := h.tasks.Update(id, req)
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid task data")
			return
		}
		if errors.Is(err, service.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "task not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, task)
}

func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	id := taskIDFromPath(r.URL.Path)
	if id == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	if err := h.tasks.Delete(id); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "task not found")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func taskIDFromPath(path string) string {
	const prefix = "/v1/tasks/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}
