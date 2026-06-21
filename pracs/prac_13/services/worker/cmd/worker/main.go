package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"prac_13/services/worker/internal/consumer"
	"prac_13/shared/logger"
)

func main() {
	log := logger.New("worker")

	url := os.Getenv("RABBIT_URL")
	if url == "" {
		url = "amqp://guest:guest@localhost:5672/"
	}

	queue := os.Getenv("QUEUE_NAME")
	if queue == "" {
		queue = "task_events"
	}

	prefetch := envInt("PREFETCH", 1)

	cfg := consumer.Config{URL: url, Queue: queue, Prefetch: prefetch}

	// Контекст, отменяемый по SIGINT/SIGTERM, — для graceful shutdown: текущее
	// сообщение успеет подтвердиться, после чего consumer корректно остановится.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Подключаемся с ретраями: в docker-compose worker может стартовать раньше,
	// чем брокер примет соединения, даже при depends_on/healthcheck.
	c := connectWithRetry(ctx, cfg, log)
	if c == nil {
		log.Fatal("could not connect to RabbitMQ")
	}
	defer c.Close()

	if err := c.Run(ctx); err != nil {
		log.WithError(err).Fatal("consumer stopped with error")
	}
	log.Info("worker stopped gracefully")
}

// connectWithRetry пытается подключиться к брокеру, повторяя попытки с паузой,
// пока не получится или пока не отменят ctx. Возвращает nil при отмене.
func connectWithRetry(ctx context.Context, cfg consumer.Config, log *logrus.Entry) *consumer.Consumer {
	const retryDelay = 2 * time.Second
	for attempt := 1; ; attempt++ {
		c, err := consumer.New(cfg, log)
		if err == nil {
			log.WithField("rabbit_url", cfg.URL).WithField("queue", cfg.Queue).
				Info("connected to RabbitMQ")
			return c
		}

		log.WithError(err).WithField("attempt", attempt).
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
