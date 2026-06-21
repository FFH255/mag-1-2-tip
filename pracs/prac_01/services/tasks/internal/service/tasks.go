package service

import (
	"errors"
	"fmt"
	"sync"
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

var ErrNotFound = errors.New("task not found")
var ErrValidation = errors.New("validation error")

type TaskService struct {
	mu     sync.RWMutex
	tasks  map[string]Task
	nextID int
}

func NewTaskService() *TaskService {
	return &TaskService{
		tasks:  make(map[string]Task),
		nextID: 1,
	}
}

func (s *TaskService) Create(req CreateTaskRequest) (Task, error) {
	if req.Title == "" {
		return Task{}, ErrValidation
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("t_%03d", s.nextID)
	s.nextID++

	task := Task{
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		DueDate:     req.DueDate,
		Done:        false,
	}

	s.tasks[id] = task
	return task, nil
}

func (s *TaskService) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		result = append(result, task)
	}
	return result
}

func (s *TaskService) Get(id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return task, nil
}

func (s *TaskService) Update(id string, req UpdateTaskRequest) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}

	if req.Title != nil {
		if *req.Title == "" {
			return Task{}, ErrValidation
		}
		task.Title = *req.Title
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.DueDate != nil {
		task.DueDate = *req.DueDate
	}
	if req.Done != nil {
		task.Done = *req.Done
	}

	s.tasks[id] = task
	return task, nil
}

func (s *TaskService) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[id]; !ok {
		return ErrNotFound
	}

	delete(s.tasks, id)
	return nil
}
