// Package service — доменный слой GraphQL-сервиса: модель Task, бизнес-логика
// (валидация, санитизация, генерация id) и интерфейс хранилища.
//
// Это самостоятельный сервис в монорепозитории, поэтому домен описан здесь, а
// не импортируется из сервиса tasks (Go запрещает импорт чужих internal-
// пакетов). Единый с REST источник истины достигается на уровне ДАННЫХ:
// postgres-репозиторий этого сервиса ходит в ту же таблицу tasks.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

// Task — доменная модель. На неё в gqlgen.yml замаплен GraphQL-тип Task
// (поля id/title/description/done). Поле DueDate в схеме не используется, но
// присутствует, чтобы модель совпадала с таблицей tasks (колонка due_date).
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"`
	Done        bool   `json:"done"`
}

type CreateTaskRequest struct {
	Title       string
	Description string
}

// UpdateTaskRequest — частичное обновление: nil-поле не меняется. Указатели
// позволяют отличить "поле не передали" от "передали пустую строку/false".
type UpdateTaskRequest struct {
	Title       *string
	Description *string
	Done        *bool
}

var (
	ErrNotFound   = errors.New("task not found")
	ErrValidation = errors.New("validation error")
)

// TaskRepository — слой хранения. Реализации: MemoryRepository (для запуска без
// БД) и PostgresRepository (та же таблица tasks, что и у REST-сервиса).
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
	if strings.TrimSpace(req.Title) == "" {
		return Task{}, ErrValidation
	}
	task := Task{
		ID:          newID(),
		Title:       sanitizeText(req.Title),
		Description: sanitizeText(req.Description),
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

// Update применяет только переданные (не nil) поля. Если поле nil — оставляем
// текущее значение, как требует методичка для Mutation.updateTask.
func (s *TaskService) Update(ctx context.Context, id string, req UpdateTaskRequest) (Task, error) {
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}

	if req.Title != nil {
		if strings.TrimSpace(*req.Title) == "" {
			return Task{}, ErrValidation
		}
		task.Title = sanitizeText(*req.Title)
	}
	if req.Description != nil {
		task.Description = sanitizeText(*req.Description)
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

// sanitizeText — минимальная защита от XSS: экранируем опасные HTML-символы,
// чтобы значения, пришедшие через мутации, нельзя было превратить в активный
// HTML при выводе на клиенте.
var textSanitizer = strings.NewReplacer(
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
	"'", "&#39;",
)

func sanitizeText(s string) string {
	return textSanitizer.Replace(s)
}
