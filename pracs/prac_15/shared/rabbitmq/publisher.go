package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher держит соединение и канал к RabbitMQ и публикует события в одну
// очередь (task_events). Очередь объявляется при создании — так публикация не
// «уйдёт в никуда», если брокер чистый и worker ещё не стартовал.
type Publisher struct {
	conn  *amqp.Connection
	ch    *amqp.Channel
	queue string
}

// NewPublisher подключается к брокеру по AMQP URL вида
// amqp://guest:guest@localhost:5672/ и объявляет durable-очередь queue.
func NewPublisher(url, queue string) (*Publisher, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if _, err := DeclareTaskQueue(ch, queue); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare queue %q: %w", queue, err)
	}

	return &Publisher{conn: conn, ch: ch, queue: queue}, nil
}

// PublishTaskCreated публикует событие task.created в очередь.
//
// Сообщение persistent (DeliveryMode=2): вместе с durable-очередью это значит,
// что уже опубликованное событие не потеряется при рестарте брокера. Публикуем
// в exchange по умолчанию ("") с routing key = имя очереди — это прямая
// доставка в неё без отдельного exchange (для ПЗ №13 сложная маршрутизация не
// нужна).
func (p *Publisher) PublishTaskCreated(ctx context.Context, taskID, requestID string) error {
	evt := TaskEvent{
		Event:     EventTaskCreated,
		TaskID:    taskID,
		TS:        time.Now().UTC().Format(time.RFC3339),
		RequestID: requestID,
	}

	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	return p.ch.PublishWithContext(
		ctx,
		"",      // exchange по умолчанию
		p.queue, // routing key = имя очереди → прямая доставка в неё
		false,   // mandatory
		false,   // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent, // delivery mode 2 — сообщение на диске
			MessageId:    taskID,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
}

// Close закрывает канал и соединение. Безопасно вызывать через defer.
func (p *Publisher) Close() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
}
