// Package jobs — адаптер постановки задач в очередь task_jobs для сервиса tasks
// (producer ПЗ №14).
//
// В отличие от events (ПЗ №13, публикация события — best-effort, ошибка
// глотается), здесь постановка job'а — это и есть смысл запроса POST
// /v1/jobs/process-task. Поэтому ошибка публикации возвращается наверх: handler
// должен ответить клиенту, что задачу не удалось поставить в очередь.
package jobs

import (
	"context"

	"github.com/sirupsen/logrus"

	"prac_15/shared/middleware"
	"prac_15/shared/rabbitmq"
)

// Publisher — обёртка над rabbitmq.JobPublisher с логированием.
type Publisher struct {
	pub *rabbitmq.JobPublisher
	log *logrus.Entry
}

// New подключается к брокеру по url и объявляет очередь task_jobs.
func New(url string, log *logrus.Entry) (*Publisher, error) {
	pub, err := rabbitmq.NewJobPublisher(url)
	if err != nil {
		return nil, err
	}
	return &Publisher{pub: pub, log: log.WithField("component", "jobs")}, nil
}

// EnqueueProcessTask ставит задачу «process_task» в очередь task_jobs. Берёт
// request_id из контекста (сквозная трассировка HTTP → лог worker'а) и
// возвращает ошибку публикации вызывающему коду.
func (p *Publisher) EnqueueProcessTask(ctx context.Context, taskID, messageID string) error {
	requestID := middleware.GetRequestID(ctx)
	if err := p.pub.PublishProcessTask(ctx, taskID, messageID, requestID); err != nil {
		p.log.WithError(err).
			WithField("task_id", taskID).
			WithField("message_id", messageID).
			WithField("request_id", requestID).
			Error("failed to enqueue process_task job")
		return err
	}
	p.log.WithField("task_id", taskID).
		WithField("message_id", messageID).
		WithField("request_id", requestID).
		Info("enqueued process_task job")
	return nil
}

// Close закрывает соединение с брокером.
func (p *Publisher) Close() { p.pub.Close() }
