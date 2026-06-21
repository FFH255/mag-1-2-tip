// Package events — адаптер публикации доменных событий для сервиса tasks.
//
// Он соединяет best-effort интерфейс service.EventPublisher с транспортом из
// shared/rabbitmq: берёт request_id из контекста запроса (для сквозной
// трассировки) и логирует ошибки публикации, не пробрасывая их в бизнес-логику.
package events

import (
	"context"

	"github.com/sirupsen/logrus"

	"prac_14/shared/middleware"
	"prac_14/shared/rabbitmq"
)

// Publisher — best-effort обёртка над rabbitmq.Publisher.
type Publisher struct {
	pub *rabbitmq.Publisher
	log *logrus.Entry
}

// New подключается к брокеру по url и объявляет очередь queue. Вызывающий код
// (main) сам решает, фатальна ли ошибка подключения; в best-effort режиме обычно
// логируют предупреждение и продолжают с Nop.
func New(url, queue string, log *logrus.Entry) (*Publisher, error) {
	pub, err := rabbitmq.NewPublisher(url, queue)
	if err != nil {
		return nil, err
	}
	return &Publisher{pub: pub, log: log.WithField("component", "events")}, nil
}

// PublishTaskCreated публикует событие task.created. Ошибка не возвращается:
// режим best-effort — задача уже в БД, поэтому сбой публикации только логируем.
func (p *Publisher) PublishTaskCreated(ctx context.Context, taskID string) {
	requestID := middleware.GetRequestID(ctx)
	if err := p.pub.PublishTaskCreated(ctx, taskID, requestID); err != nil {
		p.log.WithError(err).
			WithField("task_id", taskID).
			WithField("request_id", requestID).
			Error("failed to publish task.created event (best-effort, task is already created)")
		return
	}
	p.log.WithField("task_id", taskID).
		WithField("request_id", requestID).
		Info("published task.created event")
}

// Close закрывает соединение с брокером.
func (p *Publisher) Close() { p.pub.Close() }
