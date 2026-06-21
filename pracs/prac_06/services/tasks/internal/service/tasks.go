package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Done        bool   `json:"done"`
}

type CreateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	DueDate     string `json:"due_date"`
}

type UpdateTaskRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	DueDate     *string `json:"due_date,omitempty"`
	Done        *bool   `json:"done,omitempty"`
}

var (
	ErrNotFound   = errors.New("task not found")
	ErrValidation = errors.New("validation error")
)

// TaskRepository — интерфейс слоя хранения. Реализации (например, Postgres)
// находятся в пакете repository.
type TaskRepository interface {
	Create(ctx context.Context, t Task) error
	List(ctx context.Context) ([]Task, error)
	Get(ctx context.Context, id string) (Task, error)
	Update(ctx context.Context, t Task) error
	Delete(ctx context.Context, id string) error
	SearchByTitle(ctx context.Context, title string) ([]Task, error)
}

type TaskService struct {
	repo TaskRepository
}

func NewTaskService(repo TaskRepository) *TaskService {
	return &TaskService{repo: repo}
}

func (s *TaskService) Create(ctx context.Context, req CreateTaskRequest) (Task, error) {
	if req.Title == "" {
		return Task{}, ErrValidation
	}

	task := Task{
		ID:          newID(),
		Title:       sanitizeText(req.Title),
		Description: sanitizeText(req.Description),
		DueDate:     req.DueDate,
		Done:        false,
	}

	if err := s.repo.Create(ctx, task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *TaskService) List(ctx context.Context) ([]Task, error) {
	return s.repo.List(ctx)
}

func (s *TaskService) Get(ctx context.Context, id string) (Task, error) {
	return s.repo.Get(ctx, id)
}

func (s *TaskService) Update(ctx context.Context, id string, req UpdateTaskRequest) (Task, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}

	if req.Title != nil {
		if *req.Title == "" {
			return Task{}, ErrValidation
		}
		task.Title = sanitizeText(*req.Title)
	}
	if req.Description != nil {
		task.Description = sanitizeText(*req.Description)
	}
	if req.DueDate != nil {
		task.DueDate = *req.DueDate
	}
	if req.Done != nil {
		task.Done = *req.Done
	}

	if err := s.repo.Update(ctx, task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *TaskService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func (s *TaskService) Search(ctx context.Context, title string) ([]Task, error) {
	return s.repo.SearchByTitle(ctx, title)
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "t_" + hex.EncodeToString(b[:])
}

// sanitizeText — минимальная защита от XSS на backend: экранируем
// опасные HTML-символы, чтобы даже если фронтенд забудет экранировать
// поле при выводе, теги вроде <script> не исполнились.
// Кавычки заменяем на HTML-сущности по той же причине (защита от
// вставки в атрибут тега).
var textSanitizer = strings.NewReplacer(
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
	"'", "&#39;",
)

func sanitizeText(s string) string {
	return textSanitizer.Replace(s)
}
