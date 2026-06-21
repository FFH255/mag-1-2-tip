// Очередь задач (job queue) для ПЗ №14: producer–consumer с повторными
// попытками, DLQ и идемпотентностью.
//
// В отличие от ПЗ №13 (очередь событий task_events — лёгкое уведомление),
// здесь сообщение — это «задача» (job): её надо реально выполнить, обработка
// тяжёлая (sleep) и может падать. Поэтому добавлены три вещи:
//
//	task_jobs       — основная рабочая очередь;
//	task_jobs_retry — очередь отложенного ретрая (TTL + DLX обратно в task_jobs);
//	task_jobs_dlq   — «кладбище» для сообщений, исчерпавших попытки (dead-letter).
//
// Выбран вариант B из методички (задержка через TTL + DLX): он даёт backoff
// между попытками и ближе к реальной эксплуатации, чем мгновенные ретраи.
package rabbitmq

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Тип job'а. Пока единственный — «обработать задачу».
const JobProcessTask = "process_task"

// Имена очередей job-конвейера.
const (
	QueueJobs      = "task_jobs"       // основная очередь задач
	QueueJobsRetry = "task_jobs_retry" // отложенный ретрай (TTL → DLX → task_jobs)
	QueueJobsDLQ   = "task_jobs_dlq"   // DLQ: «плохие» сообщения после max попыток
)

// JobMessage — формат сообщения-задачи (JSON). Поля Attempt и MessageID несут
// нагрузку retry-политики и идемпотентности:
//
//	Attempt   — номер текущей попытки (с 1). Растёт при каждом ретрае.
//	MessageID — UUID задачи; стабилен между ретраями одной и той же задачи,
//	            поэтому годится как ключ дедупликации (идемпотентность).
//	Error     — текст последней ошибки; заполняется при отправке в retry/DLQ,
//	            чтобы при разборе DLQ было видно, почему задача «упала».
type JobMessage struct {
	Job       string `json:"job"`
	TaskID    string `json:"task_id"`
	Attempt   int    `json:"attempt"`
	MessageID string `json:"message_id"`
	Error     string `json:"error,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// DeclareJobsQueue объявляет только основную очередь task_jobs (durable, без
// аргументов). Её достаточно producer'у: ему нужно лишь, чтобы очередь
// существовала, и он мог опубликовать в неё задачу. Очередь без особых
// аргументов — поэтому объявление безопасно повторить с обеих сторон.
func DeclareJobsQueue(ch *amqp.Channel) (amqp.Queue, error) {
	return ch.QueueDeclare(
		QueueJobs,
		true,  // durable — переживает рестарт брокера
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,   // без аргументов
	)
}

// DeclareJobTopology объявляет полную топологию job-конвейера (сторона worker'а):
// основную очередь, retry-очередь и DLQ.
//
// Ключевой трюк — retry-очередь. У неё два аргумента:
//
//	x-message-ttl              — сколько сообщение «лежит» в retry-очереди (backoff);
//	x-dead-letter-exchange ""  — exchange по умолчанию, куда уйдёт сообщение по TTL;
//	x-dead-letter-routing-key  — routing key = task_jobs, т.е. обратно в основную.
//
// Итог: worker при retryable-ошибке кладёт задачу в task_jobs_retry; та держит
// её retryTTL, после чего брокер сам (через DLX) возвращает задачу в task_jobs.
// Так получается отложенная повторная попытка без отдельного планировщика.
//
// Аргументы очереди в RabbitMQ неизменяемы: чтобы повторные объявления не
// конфликтовали, retry-очередь (со специальными аргументами) и DLQ объявляет
// только worker. Producer трогает лишь task_jobs (без аргументов).
func DeclareJobTopology(ch *amqp.Channel, retryTTL time.Duration) error {
	if _, err := DeclareJobsQueue(ch); err != nil {
		return fmt.Errorf("declare %s: %w", QueueJobs, err)
	}

	retryArgs := amqp.Table{
		"x-message-ttl":             int32(retryTTL.Milliseconds()),
		"x-dead-letter-exchange":    "",        // exchange по умолчанию
		"x-dead-letter-routing-key": QueueJobs, // по TTL → обратно в основную очередь
	}
	if _, err := ch.QueueDeclare(QueueJobsRetry, true, false, false, false, retryArgs); err != nil {
		return fmt.Errorf("declare %s: %w", QueueJobsRetry, err)
	}

	if _, err := ch.QueueDeclare(QueueJobsDLQ, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare %s: %w", QueueJobsDLQ, err)
	}
	return nil
}

// PublishJob публикует job в exchange по умолчанию с routing key = имя очереди
// (прямая доставка). Сообщение persistent (DeliveryMode=2) и несёт MessageId —
// тот же, что в payload, чтобы идентификатор был виден и в свойствах AMQP, и в
// management UI. Используется и producer'ом (в task_jobs), и worker'ом (в
// task_jobs_retry / task_jobs_dlq).
func PublishJob(ctx context.Context, ch *amqp.Channel, routingKey string, msg JobMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	return ch.PublishWithContext(
		ctx,
		"",         // exchange по умолчанию
		routingKey, // routing key = имя очереди → прямая доставка
		false,      // mandatory
		false,      // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    msg.MessageID,
			Timestamp:    time.Now().UTC(),
			Body:         body,
		},
	)
}

// NewMessageID генерирует UUID v4 без внешних зависимостей (тем же приёмом, что
// newID в сервисе tasks). Используется producer'ом, когда клиент не передал
// свой message_id в запросе.
func NewMessageID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // версия 4
	b[8] = (b[8] & 0x3f) | 0x80 // вариант 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// JobPublisher держит соединение и канал к брокеру и публикует job'ы в основную
// очередь task_jobs. Используется producer'ом (сервис tasks).
type JobPublisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// NewJobPublisher подключается к брокеру и объявляет основную очередь task_jobs
// (без аргументов), чтобы публикация не «ушла в никуда» на чистом брокере.
func NewJobPublisher(url string) (*JobPublisher, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if _, err := DeclareJobsQueue(ch); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare queue %q: %w", QueueJobs, err)
	}

	return &JobPublisher{conn: conn, ch: ch}, nil
}

// PublishProcessTask публикует задачу «process_task» в очередь task_jobs с
// первой попыткой (attempt=1).
func (p *JobPublisher) PublishProcessTask(ctx context.Context, taskID, messageID, requestID string) error {
	return PublishJob(ctx, p.ch, QueueJobs, JobMessage{
		Job:       JobProcessTask,
		TaskID:    taskID,
		Attempt:   1,
		MessageID: messageID,
		RequestID: requestID,
	})
}

// Close закрывает канал и соединение. Безопасно вызывать через defer.
func (p *JobPublisher) Close() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
}
