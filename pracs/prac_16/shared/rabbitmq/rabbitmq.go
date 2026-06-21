// Package rabbitmq — тонкая обёртка над AMQP-клиентом для ПЗ №13.
//
// Здесь собрано всё, что общего у producer'а (сервис tasks) и consumer'а
// (services/worker): формат события task.created и единое объявление очереди.
// Очередь объявляют обе стороны одними и теми же параметрами — это безопасно
// (идемпотентно), и не важно, кто стартанул первым: брокер создаст очередь по
// первому объявлению, а повторные с теми же параметрами просто пройдут.
package rabbitmq

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

// EventTaskCreated — тип события «задача создана».
const EventTaskCreated = "task.created"

// TaskEvent — формат сообщения в очереди (JSON). Минимальный набор полей из
// ПЗ №13: что произошло, с какой задачей и когда. Поле RequestID не обязательно,
// но удобно для сквозной трассировки (X-Request-ID из HTTP → лог worker'а).
type TaskEvent struct {
	Event     string `json:"event"`
	TaskID    string `json:"task_id"`
	TS        string `json:"ts"`
	RequestID string `json:"request_id,omitempty"`
}

// DeclareTaskQueue объявляет durable-очередь задач. durable=true означает, что
// сама очередь переживёт рестарт брокера (вместе с persistent-сообщениями,
// которые публикует producer с DeliveryMode=2). Остальные флаги — дефолтные:
// очередь не автоудаляемая и не эксклюзивная, чтобы её разделяли producer и
// несколько worker'ов.
func DeclareTaskQueue(ch *amqp.Channel, name string) (amqp.Queue, error) {
	return ch.QueueDeclare(
		name,  // имя очереди (task_events)
		true,  // durable — переживает рестарт брокера
		false, // autoDelete — не удалять, когда отписался последний consumer
		false, // exclusive — доступна не только этому соединению
		false, // noWait — ждём подтверждения от брокера
		nil,   // дополнительные аргументы не нужны
	)
}
