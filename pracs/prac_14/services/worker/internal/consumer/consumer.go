// Package consumer — потребитель очереди task_events для ПЗ №13.
//
// Worker подключается к RabbitMQ, объявляет ту же durable-очередь, что и
// producer, выставляет prefetch и читает сообщения с ручным подтверждением:
// ack — после успешной обработки, nack — при ошибке.
package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"

	"prac_14/shared/rabbitmq"
)

// Config — параметры подключения и потребления.
type Config struct {
	URL      string // AMQP URL брокера
	Queue    string // имя очереди (task_events)
	Prefetch int    // сколько сообщений брокер отдаёт до получения ack
}

// Consumer держит соединение, канал и канал доставок.
type Consumer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	cfg  Config
	log  *logrus.Entry
}

// New подключается к брокеру, объявляет очередь и настраивает prefetch.
func New(cfg Config, log *logrus.Entry) (*Consumer, error) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if _, err := rabbitmq.DeclareTaskQueue(ch, cfg.Queue); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare queue %q: %w", cfg.Queue, err)
	}

	// Prefetch (QoS): брокер не отдаёт worker'у больше cfg.Prefetch
	// неподтверждённых сообщений сразу. Это ограничивает нагрузку и при
	// нескольких worker'ах честно распределяет сообщения (round-robin по тем,
	// кто готов принять). Особенно важно, когда обработка тяжёлая.
	if err := ch.Qos(cfg.Prefetch, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}

	return &Consumer{conn: conn, ch: ch, cfg: cfg, log: log}, nil
}

// Run подписывается на очередь и обрабатывает сообщения, пока не отменят ctx
// (например, по SIGINT/SIGTERM) или пока брокер не закроет канал доставок.
//
// autoAck=false: подтверждаем вручную. После успешной обработки — d.Ack; при
// «битом» сообщении (не парсится JSON) — d.Nack без requeue, чтобы не зациклить
// доставку «ядовитого» сообщения. В реальной системе такие уходили бы в DLQ.
func (c *Consumer) Run(ctx context.Context) error {
	deliveries, err := c.ch.Consume(
		c.cfg.Queue, // очередь
		"",          // consumer tag — пусть назначит брокер
		false,       // autoAck = false → ручной ack
		false,       // exclusive
		false,       // noLocal
		false,       // noWait
		nil,         // args
	)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}

	c.log.WithField("queue", c.cfg.Queue).
		WithField("prefetch", c.cfg.Prefetch).
		Info("worker subscribed, waiting for messages")

	for {
		select {
		case <-ctx.Done():
			c.log.Info("shutdown signal received, stopping consumer")
			return nil
		case d, ok := <-deliveries:
			if !ok {
				// Канал доставок закрыт (соединение/канал разорвано брокером).
				return fmt.Errorf("deliveries channel closed")
			}
			c.handle(d)
		}
	}
}

// handle обрабатывает одно сообщение: разбирает JSON, логирует событие и
// подтверждает обработку.
func (c *Consumer) handle(d amqp.Delivery) {
	var evt rabbitmq.TaskEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		c.log.WithError(err).
			WithField("body", string(d.Body)).
			Error("failed to parse message, dropping (nack without requeue)")
		// requeue=false: битое сообщение не вернётся в очередь и не зациклит обработку.
		_ = d.Nack(false, false)
		return
	}

	// Полезная работа в ПЗ №13 — просто лог факта получения события.
	// event_ts, а не ts: ts уже занят logrus под собственную метку времени.
	c.log.WithField("event", evt.Event).
		WithField("task_id", evt.TaskID).
		WithField("event_ts", evt.TS).
		WithField("request_id", evt.RequestID).
		Infof("получено событие %s id=%s", evt.Event, evt.TaskID)

	// ack после успешной обработки — брокер удаляет сообщение из очереди.
	if err := d.Ack(false); err != nil {
		c.log.WithError(err).WithField("task_id", evt.TaskID).Error("failed to ack message")
	}
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
