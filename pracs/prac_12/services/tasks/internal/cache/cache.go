// Package cache реализует распределённый кэш задач поверх Redis по стратегии
// cache-aside (см. docs/pz25_cache.md).
//
// Ключевой принцип ПЗ №9: Redis — это "ускоритель", а не источник истины.
// Поэтому все операции кэша спроектированы как best-effort: любая ошибка Redis
// (недоступен, таймаут, битый JSON) логируется как WARN и трактуется как
// "промах" (cache miss). Сервис при этом продолжает работать через БД —
// это и есть требуемая деградация при недоступности кэша.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"prac_12/services/tasks/internal/service"
)

// Убеждаемся на этапе компиляции, что обе реализации удовлетворяют интерфейсу
// кэша, который ожидает сервисный слой.
var (
	_ service.TaskCache = (*RedisCache)(nil)
	_ service.TaskCache = (*NopCache)(nil)
)

// cacheOpTimeout ограничивает длительность каждой операции с Redis. Если Redis
// недоступен или тормозит, операция отвалится по таймауту и сработает
// деградация — запрос обслужится из БД, а не зависнет.
const cacheOpTimeout = 300 * time.Millisecond

// taskKey формирует ключ кэша для задачи: tasks:task:<id>.
func taskKey(id string) string {
	return "tasks:task:" + id
}

// Config — параметры подключения и кэширования.
type Config struct {
	// Addrs — адреса Redis. Один адрес → standalone-узел (Уровень A),
	// несколько → кластер. Клиент одинаково работает в обоих режимах.
	Addrs []string
	// Password — пароль Redis (пусто, если не задан).
	Password string
	// DB — номер БД для standalone (для кластера игнорируется).
	DB int
	// TTL — базовое время жизни записи кэша.
	TTL time.Duration
	// Jitter — максимальный случайный разброс, прибавляемый к TTL, чтобы
	// записи не истекали одновременно (защита от cache avalanche).
	Jitter time.Duration
}

// RedisCache — реализация кэша задач поверх go-redis.
type RedisCache struct {
	client redis.UniversalClient
	ttl    time.Duration
	jitter time.Duration
	log    *logrus.Entry
}

// NewRedis создаёт клиент Redis. NewUniversalClient сам выбирает режим:
// один адрес → standalone (*redis.Client), несколько → cluster. Соединение
// ленивое — даже если Redis сейчас недоступен, конструктор не падает, а первые
// операции просто деградируют. Таймауты заданы явно, чтобы не зависать.
func NewRedis(cfg Config, log *logrus.Entry) *RedisCache {
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        cfg.Addrs,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  time.Second,
		ReadTimeout:  cacheOpTimeout,
		WriteTimeout: cacheOpTimeout,
		// Не повторяем операции: для кэша лучше быстро промахнуться и пойти
		// в БД, чем копить задержку на ретраях.
		MaxRetries: -1,
	})
	return &RedisCache{
		client: client,
		ttl:    cfg.TTL,
		jitter: cfg.Jitter,
		log:    log.WithField("component", "cache"),
	}
}

// Ping выполняет однократную best-effort проверку доступности Redis на старте.
// Возвращает ошибку для логирования, но вызывающий код НЕ должен падать при
// ошибке — кэш необязателен.
func (c *RedisCache) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cacheOpTimeout)
	defer cancel()
	return c.client.Ping(ctx).Err()
}

// Close закрывает пул соединений Redis.
func (c *RedisCache) Close() error {
	return c.client.Close()
}

// GetTask пытается прочитать задачу из кэша.
//   - hit  → (task, true)
//   - miss, ошибка Redis или битый JSON → (zero, false) + лог.
func (c *RedisCache) GetTask(ctx context.Context, id string) (service.Task, bool) {
	ctx, cancel := context.WithTimeout(ctx, cacheOpTimeout)
	defer cancel()

	key := taskKey(id)
	data, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		c.log.WithField("task_id", id).Debug("cache miss")
		return service.Task{}, false
	}
	if err != nil {
		// Деградация: Redis недоступен/таймаут — трактуем как miss.
		c.log.WithField("task_id", id).WithError(err).Warn("cache read failed, falling back to DB")
		return service.Task{}, false
	}

	var t service.Task
	if err := json.Unmarshal(data, &t); err != nil {
		// Битое значение в кэше не должно ломать ответ — идём в БД как при miss.
		c.log.WithField("task_id", id).WithError(err).Warn("cache decode failed, treating as miss")
		return service.Task{}, false
	}

	c.log.WithField("task_id", id).Debug("cache hit")
	return t, true
}

// SetTask кладёт задачу в кэш с TTL и случайным jitter. Ошибки записи
// логируются, но не пробрасываются — кэш необязателен.
func (c *RedisCache) SetTask(ctx context.Context, t service.Task) {
	data, err := json.Marshal(t)
	if err != nil {
		c.log.WithField("task_id", t.ID).WithError(err).Warn("cache encode failed, skip set")
		return
	}

	ctx, cancel := context.WithTimeout(ctx, cacheOpTimeout)
	defer cancel()

	if err := c.client.Set(ctx, taskKey(t.ID), data, c.ttlWithJitter()).Err(); err != nil {
		c.log.WithField("task_id", t.ID).WithError(err).Warn("cache write failed")
	}
}

// DelTask удаляет ключ задачи из кэша (инвалидация при update/delete).
// Ошибка удаления логируется, но не пробрасывается.
func (c *RedisCache) DelTask(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, cacheOpTimeout)
	defer cancel()

	if err := c.client.Del(ctx, taskKey(id)).Err(); err != nil {
		c.log.WithField("task_id", id).WithError(err).Warn("cache invalidation failed")
	}
}

// ttlWithJitter возвращает TTL + случайную добавку из [0, jitter].
// Без jitter все записи, созданные в один момент (например, прогрев кэша),
// истекли бы одновременно и дали всплеск запросов в БД (cache avalanche).
func (c *RedisCache) ttlWithJitter() time.Duration {
	if c.jitter <= 0 {
		return c.ttl
	}
	return c.ttl + time.Duration(rand.Int64N(int64(c.jitter)+1))
}

// ParseAddrs разбирает строку REDIS_ADDR в список адресов. Поддерживает как
// один узел ("localhost:6379"), так и список через запятую для кластера.
func ParseAddrs(s string) []string {
	parts := strings.Split(s, ",")
	addrs := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			addrs = append(addrs, p)
		}
	}
	return addrs
}

// NopCache — "пустой" кэш: используется, когда REDIS_ADDR не задан. Всегда
// промах, запись/удаление — no-op. Благодаря ему сервисный слой не содержит
// проверок "включён ли кэш" — он всегда работает с интерфейсом.
type NopCache struct{}

// NewNop возвращает кэш-заглушку.
func NewNop() *NopCache { return &NopCache{} }

func (NopCache) GetTask(context.Context, string) (service.Task, bool) {
	return service.Task{}, false
}
func (NopCache) SetTask(context.Context, service.Task) {}
func (NopCache) DelTask(context.Context, string)       {}
