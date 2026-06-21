package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"prac_16/services/worker/internal/consumer"
	"prac_16/services/worker/internal/jobconsumer"
	"prac_16/shared/logger"
)

// runner — общий контракт обоих потребителей: блокирующий Run(ctx) и Close().
// И consumer (события task_events, ПЗ №13), и jobconsumer (задачи task_jobs,
// ПЗ №14) ему удовлетворяют, поэтому main работает с ними единообразно.
type runner interface {
	Run(context.Context) error
	Close()
}

func main() {
	log := logger.New("worker")

	url := os.Getenv("RABBIT_URL")
	if url == "" {
		url = "amqp://guest:guest@localhost:5672/"
	}

	// Очередь событий (ПЗ №13).
	eventsQueue := os.Getenv("QUEUE_NAME")
	if eventsQueue == "" {
		eventsQueue = "task_events"
	}

	prefetch := envInt("PREFETCH", 1)

	// Параметры job-конвейера (ПЗ №14).
	jobCfg := jobconsumer.Config{
		URL:          url,
		Prefetch:     prefetch,
		MaxAttempts:  envInt("MAX_ATTEMPTS", 3),
		RetryTTL:     time.Duration(envInt("RETRY_TTL_MS", 5000)) * time.Millisecond,
		WorkDuration: time.Duration(envInt("WORK_MS", 2000)) * time.Millisecond,
	}

	// Контекст, отменяемый по SIGINT/SIGTERM, — для graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Подключаем оба потребителя с ретраями (worker может стартовать раньше брокера).
	eventCons := connectWithRetry(ctx, log, "events", func() (runner, error) {
		c, err := consumer.New(consumer.Config{URL: url, Queue: eventsQueue, Prefetch: prefetch}, log)
		if err != nil {
			return nil, err
		}
		return c, nil
	})
	jobCons := connectWithRetry(ctx, log, "jobs", func() (runner, error) {
		c, err := jobconsumer.New(jobCfg, log)
		if err != nil {
			return nil, err
		}
		return c, nil
	})

	runners := map[string]runner{}
	if eventCons != nil {
		runners["events"] = eventCons
	}
	if jobCons != nil {
		runners["jobs"] = jobCons
	}
	if len(runners) == 0 {
		log.Fatal("could not connect to RabbitMQ")
	}
	for _, r := range runners {
		defer r.Close()
	}

	// Запускаем потребителей параллельно. Если любой упадёт с ошибкой —
	// отменяем общий контекст (stop), чтобы второй тоже корректно остановился.
	var wg sync.WaitGroup
	for name, r := range runners {
		wg.Add(1)
		go func(name string, r runner) {
			defer wg.Done()
			if err := r.Run(ctx); err != nil {
				log.WithField("consumer", name).WithError(err).Error("consumer stopped with error")
				stop()
			}
		}(name, r)
	}
	wg.Wait()
	log.Info("worker stopped gracefully")
}

// connectWithRetry повторяет попытки подключения с паузой, пока connect не
// вернёт потребителя или пока не отменят ctx. Возвращает nil при отмене.
func connectWithRetry(ctx context.Context, log *logrus.Entry, name string, connect func() (runner, error)) runner {
	const retryDelay = 2 * time.Second
	for attempt := 1; ; attempt++ {
		r, err := connect()
		if err == nil {
			log.WithField("consumer", name).Info("connected to RabbitMQ")
			return r
		}

		log.WithField("consumer", name).WithError(err).WithField("attempt", attempt).
			Warnf("RabbitMQ not ready, retrying in %s", retryDelay)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(retryDelay):
		}
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
