package service

import (
	"context"
	"testing"
)

// fakeRepo — минимальный репозиторий с подсчётом обращений к Get, чтобы
// проверить, что при попадании в кэш в БД мы не ходим.
type fakeRepo struct {
	task     Task
	getCalls int
	getErr   error
}

func (r *fakeRepo) Create(context.Context, Task) error { return nil }
func (r *fakeRepo) List(context.Context) ([]Task, error) {
	return nil, nil
}
func (r *fakeRepo) Get(_ context.Context, _ string) (Task, error) {
	r.getCalls++
	if r.getErr != nil {
		return Task{}, r.getErr
	}
	return r.task, nil
}
func (r *fakeRepo) Update(context.Context, Task) error   { return nil }
func (r *fakeRepo) Delete(context.Context, string) error { return nil }
func (r *fakeRepo) SearchByTitle(context.Context, string) ([]Task, error) {
	return nil, nil
}

// fakeCache — in-memory кэш с подсчётом set/del и режимом "всегда сбой"
// (alwaysMiss), имитирующим недоступность Redis.
type fakeCache struct {
	store      map[string]Task
	setCalls   int
	delCalls   int
	alwaysMiss bool
}

func newFakeCache() *fakeCache { return &fakeCache{store: map[string]Task{}} }

func (c *fakeCache) GetTask(_ context.Context, id string) (Task, bool) {
	if c.alwaysMiss {
		return Task{}, false
	}
	t, ok := c.store[id]
	return t, ok
}
func (c *fakeCache) SetTask(_ context.Context, t Task) {
	c.setCalls++
	if !c.alwaysMiss {
		c.store[t.ID] = t
	}
}
func (c *fakeCache) DelTask(_ context.Context, id string) {
	c.delCalls++
	delete(c.store, id)
}

// TestGet_CacheAside проверяет канонический сценарий cache-aside:
// первый запрос — miss + поход в БД + запись в кэш; второй — hit без БД.
func TestGet_CacheAside(t *testing.T) {
	repo := &fakeRepo{task: Task{ID: "t_1", Title: "A"}}
	c := newFakeCache()
	svc := NewTaskService(repo, c)
	ctx := context.Background()

	// 1-й вызов: промах → БД → запись в кэш.
	got, err := svc.Get(ctx, "t_1")
	if err != nil || got.ID != "t_1" {
		t.Fatalf("first Get = (%+v, %v), want task t_1", got, err)
	}
	if repo.getCalls != 1 {
		t.Fatalf("repo.Get calls = %d, want 1 on miss", repo.getCalls)
	}
	if c.setCalls != 1 {
		t.Fatalf("cache.Set calls = %d, want 1 after miss", c.setCalls)
	}

	// 2-й вызов: попадание → БД не дёргаем.
	got, err = svc.Get(ctx, "t_1")
	if err != nil || got.ID != "t_1" {
		t.Fatalf("second Get = (%+v, %v), want task t_1", got, err)
	}
	if repo.getCalls != 1 {
		t.Fatalf("repo.Get calls = %d, want still 1 on cache hit", repo.getCalls)
	}
}

// TestUpdateDelete_InvalidateCache проверяет политику "изменил — сбросил".
func TestUpdateDelete_InvalidateCache(t *testing.T) {
	repo := &fakeRepo{task: Task{ID: "t_1", Title: "A"}}
	c := newFakeCache()
	svc := NewTaskService(repo, c)
	ctx := context.Background()

	if _, err := svc.Update(ctx, "t_1", UpdateTaskRequest{}); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if c.delCalls != 1 {
		t.Fatalf("cache.Del after Update = %d, want 1", c.delCalls)
	}

	if err := svc.Delete(ctx, "t_1"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if c.delCalls != 2 {
		t.Fatalf("cache.Del after Delete = %d, want 2 total", c.delCalls)
	}
}

// TestGet_DegradesWhenCacheUnavailable: если кэш всегда промахивается
// (имитация недоступного Redis), сервис всё равно отдаёт данные из БД и
// не возвращает ошибку.
func TestGet_DegradesWhenCacheUnavailable(t *testing.T) {
	repo := &fakeRepo{task: Task{ID: "t_1", Title: "A"}}
	c := newFakeCache()
	c.alwaysMiss = true
	svc := NewTaskService(repo, c)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		got, err := svc.Get(ctx, "t_1")
		if err != nil || got.ID != "t_1" {
			t.Fatalf("Get #%d = (%+v, %v), want task from DB", i, got, err)
		}
	}
	if repo.getCalls != 3 {
		t.Fatalf("repo.Get calls = %d, want 3 (every request hits DB when cache is down)", repo.getCalls)
	}
}
