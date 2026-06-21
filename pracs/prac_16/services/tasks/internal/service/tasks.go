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

// TaskCache — best-effort кэш задач (cache-aside). Методы намеренно не
// возвращают ошибок: кэш — это "ускоритель", а не источник истины, поэтому при
// любом сбое Redis реализация логирует проблему и ведёт себя как промах/no-op,
// а сервис продолжает работать через БД. Реализации — в пакете cache
// (RedisCache и NopCache).
type TaskCache interface {
	// GetTask возвращает (task, true) при попадании; (zero, false) при промахе
	// или любой ошибке кэша.
	GetTask(ctx context.Context, id string) (Task, bool)
	// SetTask кладёт задачу в кэш с TTL (ошибки игнорируются).
	SetTask(ctx context.Context, t Task)
	// DelTask инвалидирует ключ задачи (ошибки игнорируются).
	DelTask(ctx context.Context, id string)
}

// EventPublisher — публикация доменных событий в брокер (ПЗ №13). Метод
// намеренно best-effort и не возвращает ошибку: режим публикации выбран
// "best effort" — задача уже создана в БД (источник истины), и если событие не
// ушло, мы не откатываем создание и не возвращаем клиенту 500, а только логируем
// (это делает реализация). Так сбой RabbitMQ не ломает основной сценарий.
// Реализации — в пакете events (Publisher поверх shared/rabbitmq и Nop).
type EventPublisher interface {
	// PublishTaskCreated публикует событие task.created для созданной задачи.
	PublishTaskCreated(ctx context.Context, taskID string)
}

type TaskService struct {
	repo   TaskRepository
	cache  TaskCache
	events EventPublisher
}

// NewTaskService собирает сервис. Аргумент events может быть nil (например, в
// юнит-тестах) — тогда подставляется no-op, и публикация событий выключена.
func NewTaskService(repo TaskRepository, cache TaskCache, events EventPublisher) *TaskService {
	if events == nil {
		events = nopEventPublisher{}
	}
	return &TaskService{repo: repo, cache: cache, events: events}
}

// nopEventPublisher — заглушка на случай, когда публикация не настроена.
type nopEventPublisher struct{}

func (nopEventPublisher) PublishTaskCreated(context.Context, string) {}

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

	// Событие публикуем ТОЛЬКО после успешной записи в БД (если задача не
	// создана — событие отправлять нельзя). Режим best-effort: возможный сбой
	// публикации логируется внутри events и не влияет на ответ клиенту (201).
	s.events.PublishTaskCreated(ctx, task.ID)

	return task, nil
}

func (s *TaskService) List(ctx context.Context) ([]Task, error) {
	return s.repo.List(ctx)
}

// Get реализует cache-aside для чтения задачи по id:
//  1. пробуем кэш — при попадании сразу возвращаем;
//  2. при промахе идём в репозиторий (источник истины);
//  3. найденную задачу кладём в кэш с TTL и возвращаем клиенту.
//
// Если Redis недоступен, шаги 1 и 3 деградируют (промах/no-op), и запрос
// обслуживается из БД без ошибки.
func (s *TaskService) Get(ctx context.Context, id string) (Task, error) {
	if t, ok := s.cache.GetTask(ctx, id); ok {
		return t, nil
	}

	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}

	s.cache.SetTask(ctx, t)
	return t, nil
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
	// Инвалидация: "изменил — сбросил". Удаляем устаревшую запись, чтобы
	// следующий GET перечитал актуальные данные из БД и заново прогрел кэш.
	s.cache.DelTask(ctx, id)
	return task, nil
}

func (s *TaskService) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	// Инвалидация удалённой задачи, чтобы кэш не отдавал её после удаления.
	s.cache.DelTask(ctx, id)
	return nil
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
