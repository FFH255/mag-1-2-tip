package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	"prac_13/services/tasks/internal/client/authclient"
	"prac_13/services/tasks/internal/service"
	"prac_13/shared/httpx"
	"prac_13/shared/middleware"
)

type Handler struct {
	tasks *service.TaskService
	auth  *authclient.Client
	log   *logrus.Entry
}

func NewHandler(tasks *service.TaskService, auth *authclient.Client, log *logrus.Entry) *Handler {
	return &Handler{tasks: tasks, auth: auth, log: log.WithField("component", "handler")}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("POST /v1/tasks", h.CreateTask)
	mux.HandleFunc("GET /v1/tasks", h.ListTasks)
	mux.HandleFunc("GET /v1/tasks/search", h.SearchTasks)
	mux.HandleFunc("GET /v1/tasks/", h.GetTask)
	mux.HandleFunc("PATCH /v1/tasks/", h.UpdateTask)
	mux.HandleFunc("DELETE /v1/tasks/", h.DeleteTask)
}

// Health — liveness/readiness проба (ПЗ №10). Не требует авторизации и
// всегда отвечает 200 { "status": "ok" }. Используется балансировщиком и
// оркестратором для проверки готовности реплики.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	// Поддерживаем два способа аутентификации:
	//   1) cookie "session"  — основной способ для браузеров (нужен CSRF);
	//   2) Authorization: Bearer ... — для curl/интеграций (как в прошлых ПЗ).
	// Если пришла session cookie — превращаем её в Bearer-заголовок для Auth.
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		if c, err := r.Cookie("session"); err == nil && c.Value != "" {
			authHeader = "Bearer " + c.Value
		}
	}

	err := h.auth.Verify(
		r.Context(),
		authHeader,
		middleware.GetRequestID(r.Context()),
	)
	if err == nil {
		return true
	}

	if errors.Is(err, authclient.ErrUnauthorized) {
		log.Warn("authorization failed: unauthorized")
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if errors.Is(err, authclient.ErrAuthUnavailable) {
		log.WithError(err).Error("auth service unavailable")
		httpx.WriteError(w, http.StatusBadGateway, "auth service unavailable")
		return false
	}

	log.WithError(err).Error("authorization internal error")
	httpx.WriteError(w, http.StatusInternalServerError, "internal error")
	return false
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	var req service.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WithError(err).Warn("create task: invalid json")
		httpx.WriteError(w, http.StatusBadRequest, "invalid json")
		return
	}

	task, err := h.tasks.Create(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			log.Warn("create task: title is required")
			httpx.WriteError(w, http.StatusBadRequest, "title is required")
			return
		}
		log.WithError(err).Error("create task: internal error")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	log.WithField("task_id", task.ID).Info("task created")
	httpx.WriteJSON(w, http.StatusCreated, task)
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	tasks, err := h.tasks.List(r.Context())
	if err != nil {
		log.WithError(err).Error("list tasks: internal error")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tasks)
}

// SearchTasks — GET /v1/tasks/search?title=...
// Использует параметризованный SQL-запрос в репозитории. Внешний ввод
// никогда не склеивается со строкой SQL.
func (h *Handler) SearchTasks(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	title := r.URL.Query().Get("title")

	tasks, err := h.tasks.Search(r.Context(), title)
	if err != nil {
		log.WithError(err).Error("search tasks: internal error")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tasks)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	id := taskIDFromPath(r.URL.Path)
	if id == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	task, err := h.tasks.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			log.WithField("task_id", id).Warn("task not found")
			httpx.WriteError(w, http.StatusNotFound, "task not found")
			return
		}
		log.WithField("task_id", id).WithError(err).Error("get task: internal error")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, task)
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	id := taskIDFromPath(r.URL.Path)
	if id == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	var req service.UpdateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WithError(err).Warn("update task: invalid json")
		httpx.WriteError(w, http.StatusBadRequest, "invalid json")
		return
	}

	task, err := h.tasks.Update(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			log.WithField("task_id", id).Warn("update task: invalid data")
			httpx.WriteError(w, http.StatusBadRequest, "invalid task data")
			return
		}
		if errors.Is(err, service.ErrNotFound) {
			log.WithField("task_id", id).Warn("task not found")
			httpx.WriteError(w, http.StatusNotFound, "task not found")
			return
		}
		log.WithField("task_id", id).WithError(err).Error("update task: internal error")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	log.WithField("task_id", task.ID).Info("task updated")
	httpx.WriteJSON(w, http.StatusOK, task)
}

func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}

	log := h.log.WithField("request_id", middleware.GetRequestID(r.Context()))

	id := taskIDFromPath(r.URL.Path)
	if id == "" {
		httpx.WriteError(w, http.StatusNotFound, "not found")
		return
	}

	if err := h.tasks.Delete(r.Context(), id); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			log.WithField("task_id", id).Warn("task not found")
			httpx.WriteError(w, http.StatusNotFound, "task not found")
			return
		}
		log.WithField("task_id", id).WithError(err).Error("delete task: internal error")
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}

	log.WithField("task_id", id).Info("task deleted")
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
