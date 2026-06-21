package graph

import (
	"context"
	"testing"

	"prac_15/services/graphql/graph/model"
	"prac_15/services/graphql/internal/repository"
	"prac_15/services/graphql/internal/service"
)

// newResolver собирает резолверы поверх in-memory репозитория — тот же стек, что
// и в проде, но без БД.
func newResolver(seed ...service.Task) *Resolver {
	repo := repository.NewMemoryRepository()
	repo.Seed(seed...)
	return &Resolver{TaskSvc: service.NewTaskService(repo)}
}

func ptr[T any](v T) *T { return &v }

func TestQueryTasksAndTask(t *testing.T) {
	r := newResolver(service.Task{ID: "t_001", Title: "seed", Done: false})
	q := r.Query()
	ctx := context.Background()

	tasks, err := q.Tasks(ctx)
	if err != nil {
		t.Fatalf("tasks: unexpected error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t_001" {
		t.Fatalf("tasks: got %+v", tasks)
	}

	got, err := q.Task(ctx, "t_001")
	if err != nil || got == nil || got.Title != "seed" {
		t.Fatalf("task: got=%+v err=%v", got, err)
	}

	// Не найдена → null без ошибки (методичка 5.2).
	missing, err := q.Task(ctx, "absent")
	if err != nil {
		t.Fatalf("task(absent): unexpected error: %v", err)
	}
	if missing != nil {
		t.Fatalf("task(absent): want nil, got %+v", missing)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	m := newResolver().Mutation()
	ctx := context.Background()

	if _, err := m.CreateTask(ctx, model.CreateTaskInput{Title: ""}); err == nil {
		t.Fatal("createTask(empty title): expected validation error")
	}

	created, err := m.CreateTask(ctx, model.CreateTaskInput{Title: "hello", Description: ptr("d")})
	if err != nil {
		t.Fatalf("createTask: %v", err)
	}
	if created.Title != "hello" || created.Done {
		t.Fatalf("createTask: got %+v", created)
	}
}

func TestUpdateTaskPartial(t *testing.T) {
	r := newResolver(service.Task{ID: "t_001", Title: "orig", Description: "desc", Done: false})
	m := r.Mutation()
	ctx := context.Background()

	// Передаём только done — title/description не должны измениться.
	updated, err := m.UpdateTask(ctx, "t_001", model.UpdateTaskInput{Done: ptr(true)})
	if err != nil {
		t.Fatalf("updateTask: %v", err)
	}
	if !updated.Done || updated.Title != "orig" || updated.Description != "desc" {
		t.Fatalf("updateTask partial: got %+v", updated)
	}

	if _, err := m.UpdateTask(ctx, "absent", model.UpdateTaskInput{Done: ptr(true)}); err == nil {
		t.Fatal("updateTask(absent): expected not-found error")
	}
}

func TestDeleteTask(t *testing.T) {
	r := newResolver(service.Task{ID: "t_001", Title: "x"})
	m := r.Mutation()
	ctx := context.Background()

	ok, err := m.DeleteTask(ctx, "t_001")
	if err != nil || !ok {
		t.Fatalf("deleteTask(existing): ok=%v err=%v", ok, err)
	}

	// Удаление несуществующей → false без ошибки (методичка 5.5).
	ok, err = m.DeleteTask(ctx, "t_001")
	if err != nil || ok {
		t.Fatalf("deleteTask(missing): want false,nil; got ok=%v err=%v", ok, err)
	}
}
