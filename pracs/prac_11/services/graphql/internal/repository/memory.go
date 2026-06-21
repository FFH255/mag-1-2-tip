// Package repository содержит реализации service.TaskRepository для
// GraphQL-сервиса.
//
// MemoryRepository — потокобезопасное хранилище в памяти. Оно нужно, чтобы
// сервис graphql можно было запустить и проверить в Playground без поднятия
// PostgreSQL. Для "боевого" единого источника истины тот же service.TaskService
// можно собрать с repository.NewPostgresRepository из сервиса tasks — интерфейс
// один и тот же.
package repository

import (
	"context"
	"sync"

	"prac_11/services/graphql/internal/service"
)

// Проверка на этапе компиляции, что MemoryRepository удовлетворяет интерфейсу
// слоя хранения, который ожидает service.TaskService.
var _ service.TaskRepository = (*MemoryRepository)(nil)

type MemoryRepository struct {
	mu    sync.RWMutex
	tasks map[string]service.Task
	// order хранит порядок добавления, чтобы List/Search отдавали стабильный,
	// предсказуемый порядок (как ORDER BY created_at в Postgres-репозитории).
	order []string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{tasks: make(map[string]service.Task)}
}

// Seed наполняет хранилище стартовыми данными (удобно для демонстрации в
// Playground; например, id "t_001" из примеров методички).
func (r *MemoryRepository) Seed(tasks ...service.Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tasks {
		if _, exists := r.tasks[t.ID]; !exists {
			r.order = append(r.order, t.ID)
		}
		r.tasks[t.ID] = t
	}
}

func (r *MemoryRepository) Create(_ context.Context, t service.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tasks[t.ID]; !exists {
		r.order = append(r.order, t.ID)
	}
	r.tasks[t.ID] = t
	return nil
}

func (r *MemoryRepository) List(_ context.Context) ([]service.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot(), nil
}

func (r *MemoryRepository) Get(_ context.Context, id string) (service.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	if !ok {
		return service.Task{}, service.ErrNotFound
	}
	return t, nil
}

func (r *MemoryRepository) Update(_ context.Context, t service.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[t.ID]; !ok {
		return service.ErrNotFound
	}
	r.tasks[t.ID] = t
	return nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tasks[id]; !ok {
		return service.ErrNotFound
	}
	delete(r.tasks, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

func (r *MemoryRepository) SearchByTitle(_ context.Context, title string) ([]service.Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]service.Task, 0)
	for _, t := range r.snapshot() {
		if t.Title == title {
			result = append(result, t)
		}
	}
	return result, nil
}

// snapshot возвращает задачи в порядке добавления. Вызывается под удержанной
// блокировкой.
func (r *MemoryRepository) snapshot() []service.Task {
	result := make([]service.Task, 0, len(r.order))
	for _, id := range r.order {
		if t, ok := r.tasks[id]; ok {
			result = append(result, t)
		}
	}
	return result
}
