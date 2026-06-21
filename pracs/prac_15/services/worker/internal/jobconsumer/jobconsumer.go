// Package jobconsumer — потребитель очереди задач task_jobs для ПЗ №14.
//
// В отличие от consumer (ПЗ №13, лёгкое событие → лог → ack), здесь обработка
// «тяжёлая» (sleep) и может падать, поэтому добавлены три механизма:
//
//	retries  — ограниченное число повторных попыток через retry-очередь с TTL;
//	DLQ      — после исчерпания попыток задача уходит в task_jobs_dlq;
//	идемпотентность — дедупликация по message_id (защита от at-least-once дублей).
package jobconsumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"

	"prac_15/shared/rabbitmq"
)

// Config — параметры подключения и retry-политики.
type Config struct {
	URL          string        // AMQP URL брокера
	Prefetch     int           // сколько сообщений брокер отдаёт до ack
	MaxAttempts  int           // максимум попыток до DLQ (по умолчанию 3)
	RetryTTL     time.Duration // backoff: сколько задача «лежит» в retry-очереди
	WorkDuration time.Duration // имитация тяжёлой работы (sleep)
}

// Consumer держит соединение, канал (он же для publish в retry/DLQ) и хранилище
// обработанных message_id.
type Consumer struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	cfg       Config
	log       *logrus.Entry
	processed *processedStore
}

// New подключается к брокеру, объявляет полную топологию (task_jobs + retry +
// DLQ) и настраивает prefetch.
func New(cfg Config, log *logrus.Entry) (*Consumer, error) {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 3
	}

	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if err := rabbitmq.DeclareJobTopology(ch, cfg.RetryTTL); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare job topology: %w", err)
	}

	if err := ch.Qos(cfg.Prefetch, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}

	return &Consumer{conn: conn, ch: ch, cfg: cfg, log: log.WithField("consumer", "jobs"), processed: newProcessedStore()}, nil
}

// Run подписывается на task_jobs и обрабатывает задачи, пока не отменят ctx или
// брокер не закроет канал доставок.
func (c *Consumer) Run(ctx context.Context) error {
	deliveries, err := c.ch.Consume(
		rabbitmq.QueueJobs,
		"",    // consumer tag
		false, // autoAck=false → ручной ack
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	c.log.WithField("queue", rabbitmq.QueueJobs).
		WithField("prefetch", c.cfg.Prefetch).
		WithField("max_attempts", c.cfg.MaxAttempts).
		WithField("retry_ttl", c.cfg.RetryTTL.String()).
		Info("job worker subscribed, waiting for jobs")

	for {
		select {
		case <-ctx.Done():
			c.log.Info("shutdown signal received, stopping job consumer")
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("deliveries channel closed")
			}
			c.handle(ctx, d)
		}
	}
}

// handle обрабатывает одну задачу: проверяет дубль, выполняет работу, при ошибке
// решает retry vs DLQ. В конце исходное сообщение всегда ack — чтобы не оставить
// его unacked и не получить неконтролируемые повторы (см. методичку, шаг 4).
func (c *Consumer) handle(ctx context.Context, d amqp.Delivery) {
	var msg rabbitmq.JobMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		// «Ядовитое» сообщение: тело не парсится. Чтобы не потерять — кладём
		// сырое тело в DLQ (с пометкой в заголовке) и ack, иначе оно зациклит
		// или потеряется. Основная очередь без DLX, поэтому публикуем явно.
		c.log.WithError(err).WithField("body", string(d.Body)).
			Error("malformed job, routing raw body to DLQ")
		c.publishRawToDLQ(ctx, d.Body)
		_ = d.Ack(false)
		return
	}

	if msg.Attempt < 1 {
		msg.Attempt = 1
	}

	log := c.log.WithField("task_id", msg.TaskID).
		WithField("message_id", msg.MessageID).
		WithField("attempt", msg.Attempt).
		WithField("request_id", msg.RequestID)

	// Идемпотентность: если message_id уже успешно обработан — это дубль
	// (at-least-once мог доставить сообщение повторно). Не выполняем работу
	// второй раз, просто ack.
	if c.processed.isProcessed(msg.MessageID) {
		log.Warn("duplicate job (message_id already processed), skipping — idempotent ack")
		_ = d.Ack(false)
		return
	}

	log.Infof("processing job %s task_id=%s (attempt %d/%d)", msg.Job, msg.TaskID, msg.Attempt, c.cfg.MaxAttempts)

	// Имитация тяжёлой работы. Прерывается по shutdown, чтобы не задерживать
	// graceful-выход; в этом случае НЕ ack — сообщение вернётся в очередь.
	select {
	case <-ctx.Done():
		log.Info("shutdown during job work, leaving message unacked for redelivery")
		_ = d.Nack(false, true)
		return
	case <-time.After(c.cfg.WorkDuration):
	}

	if err := doWork(msg); err != nil {
		c.onFailure(ctx, d, msg, err, log)
		return
	}

	// Успех: помечаем message_id обработанным и подтверждаем.
	c.processed.mark(msg.MessageID)
	log.Info("job done successfully, ack")
	if err := d.Ack(false); err != nil {
		log.WithError(err).Error("failed to ack successful job")
	}
}

// onFailure реализует retry-политику: пока есть попытки — отложенный ретрай
// через retry-очередь; когда исчерпаны — DLQ. Исходное сообщение в обоих случаях
// ack (мы уже сделали копию в retry/DLQ).
func (c *Consumer) onFailure(ctx context.Context, d amqp.Delivery, msg rabbitmq.JobMessage, workErr error, log *logrus.Entry) {
	log.WithError(workErr).Warn("job failed")

	if msg.Attempt < c.cfg.MaxAttempts {
		// Отложенный ретрай: публикуем в retry-очередь с увеличенным attempt.
		// retry-очередь подержит сообщение RetryTTL и через DLX вернёт его в
		// task_jobs (backoff). message_id сохраняем — это та же задача.
		next := msg
		next.Attempt = msg.Attempt + 1
		next.Error = workErr.Error()
		if err := rabbitmq.PublishJob(ctx, c.ch, rabbitmq.QueueJobsRetry, next); err != nil {
			// Не удалось запланировать ретрай — не теряем сообщение: возвращаем
			// в очередь (requeue=true), попробуем снова.
			log.WithError(err).Error("failed to schedule retry, requeueing original")
			_ = d.Nack(false, true)
			return
		}
		log.WithField("next_attempt", next.Attempt).
			Infof("scheduled retry: attempt %d → retry queue (backoff %s)", next.Attempt, c.cfg.RetryTTL)
		if err := d.Ack(false); err != nil {
			log.WithError(err).Error("failed to ack after scheduling retry")
		}
		return
	}

	// Попытки исчерпаны → DLQ.
	dead := msg
	dead.Error = workErr.Error()
	if err := rabbitmq.PublishJob(ctx, c.ch, rabbitmq.QueueJobsDLQ, dead); err != nil {
		log.WithError(err).Error("failed to publish to DLQ, requeueing original")
		_ = d.Nack(false, true)
		return
	}
	log.WithField("dlq", rabbitmq.QueueJobsDLQ).
		Errorf("max attempts (%d) exhausted → job sent to DLQ", c.cfg.MaxAttempts)
	if err := d.Ack(false); err != nil {
		log.WithError(err).Error("failed to ack after DLQ")
	}
}

// publishRawToDLQ кладёт нераспарсенное тело в DLQ как есть (best-effort).
func (c *Consumer) publishRawToDLQ(ctx context.Context, body []byte) {
	err := c.ch.PublishWithContext(ctx, "", rabbitmq.QueueJobsDLQ, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Headers:      amqp.Table{"x-dead-letter-reason": "malformed-json"},
		Body:         body,
	})
	if err != nil {
		c.log.WithError(err).Error("failed to route malformed body to DLQ")
	}
}

// doWork имитирует тяжёлую обработку задачи. Ошибку моделируем детерминированно
// по task_id — чтобы демонстрация ретраев и DLQ была воспроизводимой:
// задача «падает», если task_id содержит "fail" или заканчивается на "3".
// (Методичка допускает и случайную ошибку — здесь выбран детерминированный
// вариант ради повторяемого лога.)
func doWork(msg rabbitmq.JobMessage) error {
	id := strings.ToLower(msg.TaskID)
	if strings.Contains(id, "fail") || strings.HasSuffix(id, "3") {
		return fmt.Errorf("simulated processing error for task_id=%s", msg.TaskID)
	}
	return nil
}

// Close закрывает канал и соединение.
func (c *Consumer) Close() {
	if c.ch != nil {
		_ = c.ch.Close()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// processedStore — учебное «хранилище обработанных message_id» в памяти.
// Достаточно, чтобы продемонстрировать принцип идемпотентности. В проде это
// были бы Redis/БД (переживут рестарт и общие для нескольких worker'ов).
type processedStore struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newProcessedStore() *processedStore {
	return &processedStore{seen: make(map[string]struct{})}
}

func (s *processedStore) isProcessed(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[id]
	return ok
}

func (s *processedStore) mark(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[id] = struct{}{}
}
