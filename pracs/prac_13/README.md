## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №13
**Тема:** RabbitMQ — базовая работа с очередями: публикация и потребление
сообщений.
**Технологии:** RabbitMQ (AMQP), docker-compose, Go-клиент
[`rabbitmq/amqp091-go`](https://github.com/rabbitmq/amqp091-go), `ack`, `prefetch`,
durable-очередь, persistent-сообщения.
**Объект практики:** сервис `tasks` (producer, [services/tasks](services/tasks/))
и отдельный сервис `worker` (consumer, [services/worker](services/worker/)).

**Цель.** Поднять RabbitMQ, публиковать события в очередь и обрабатывать их
потребителем с подтверждением (`ack`), понимая основы надёжности доставки.

**Реализованный сценарий «событие о создании задачи»:**

```text
POST /v1/tasks ──▶ tasks создаёт задачу в БД ──▶ публикует task.created
                                                  в очередь task_events
                                                          │
                                                          ▼
                                       worker читает очередь и логирует:
                                       "получено событие task.created id=…",
                                       затем ack
```

Сложной маршрутизации exchange'ами здесь нет — одна durable-очередь
`task_events` и прямая публикация в неё.

> Всё ниже — **реальный вывод запущенного стека** (`docker compose`, раздел 0).
> Producer — REST-сервис `tasks` (`http://localhost:8082`), брокер RabbitMQ
> (AMQP `:5672`, management UI `:15672`), consumer — сервис `worker`. Логи в
> JSON (logrus), очередь и счётчики сообщений проверены через
> `rabbitmqctl list_queues` и management HTTP API.

---

## 0. Как поднят стенд (воспроизводимость)

Весь стек собран и поднят одной командой из корня репозитория
([deploy/docker-compose.yml](deploy/docker-compose.yml) — туда добавлены сервисы
`rabbitmq` и `worker`):

```bash
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml down   # остановить и удалить
```

Только брокер (как в задании, изолированный стенд) поднимается из
[deploy/rabbit/docker-compose.yml](deploy/rabbit/docker-compose.yml):

```bash
cd deploy/rabbit && docker compose up -d && docker compose ps
```

Реальный статус контейнеров ПЗ №13 (`docker compose ps`):

```text
NAME           SERVICE    STATUS                    PORTS
pz13_rabbit    rabbitmq   Up 9 minutes (healthy)    0.0.0.0:5672->5672/tcp, 0.0.0.0:15672->15672/tcp
pz13_worker    worker     Up 9 seconds
pz7_tasks      tasks      Up 8 minutes              0.0.0.0:8082->8082/tcp
```

| Компонент | Контейнер | Порт(ы) | Назначение |
|-----------|-----------|---------|------------|
| RabbitMQ (AMQP) | `pz13_rabbit` | **5672** | протокол AMQP — сюда подключаются producer/consumer |
| RabbitMQ (UI/API) | `pz13_rabbit` | **15672** | management UI и HTTP API (`guest`/`guest`) |
| Producer | `pz7_tasks` | 8082 | REST `tasks`, публикует событие при создании задачи |
| Consumer | `pz13_worker` | — | читает `task_events`, портов не слушает |

**Образ.** `rabbitmq:3.13-management-alpine` — версия с management-плагином
(UI + HTTP API на 15672). Креды учебные: `guest`/`guest`, URL подключения —
`amqp://guest:guest@rabbitmq:5672/` (внутри docker-сети) или
`amqp://guest:guest@localhost:5672/` (с хоста).

**Healthcheck.** Использован `rabbitmq-diagnostics -q check_port_connectivity`, а
не `ping`: `ping` помечает узел `healthy` раньше, чем слушатель AMQP начинает
принимать соединения, и зависимые сервисы ловят `connection refused`.
`check_port_connectivity` дожидается готовности именно слушателя 5672 — поэтому
`tasks`/`worker` (через `depends_on: condition: service_healthy`) стартуют, когда
брокер реально готов.

---

## 1. Формат сообщения (JSON) и почему такой

Формат события задаётся типом `TaskEvent` в
[shared/rabbitmq/rabbitmq.go](shared/rabbitmq/rabbitmq.go) и общий для producer и
consumer (один источник истины о схеме). Реально опубликованное тело сообщения:

```json
{"event":"task.created","task_id":"t_77dfc1f6d7f55efe","ts":"2026-05-29T16:48:50Z","request_id":"pz13-002"}
```

| Поле | Зачем |
|------|-------|
| `event` | **тип** события (`task.created`). По нему consumer ветвит обработку, когда событий станет несколько |
| `task_id` | **идентификатор** задачи — по нему consumer найдёт сущность; основной полезный контент события |
| `ts` | **время** события (RFC3339, UTC) — для логов, отладки, упорядочивания |
| `request_id` | сквозная трассировка: `X-Request-ID` из HTTP-запроса проносится до лога worker'а (`request_id` опционален, `omitempty`) |

**Почему JSON.** Человекочитаемо (видно в management UI), не требует генерации
кода/схемы (в отличие от protobuf/Avro), легко расширяется — для учебного
сценария это оптимально. Минимума (`event` + `task_id` + `ts`) достаточно;
`request_id` добавлен ради трассировки и пригодится в следующих ПЗ.

**Тип содержимого и надёжность.** Сообщение публикуется с
`ContentType: application/json` и `DeliveryMode: 2` (persistent) — см. раздел 4.

---

## 2. Producer: где и как публикуется событие

**Где именно (на каком шаге).** Событие публикуется **строго после успешной
записи задачи в БД** — в методе `Create` сервиса,
[services/tasks/internal/service/tasks.go:114](services/tasks/internal/service/tasks.go#L114):

```go
if err := s.repo.Create(ctx, task); err != nil {
    return Task{}, err           // задача не создана → событие НЕ публикуем
}
// Событие публикуем ТОЛЬКО после успешной записи в БД.
s.events.PublishTaskCreated(ctx, task.ID)
return task, nil
```

Это соблюдает требование задания: *«если задача не создана — событие
публиковать нельзя»*. Если валидация/репозиторий вернули ошибку — до публикации
дело не доходит.

**Выбранный режим — `best effort`.** Публикация устроена как **best-effort**:
интерфейс `EventPublisher` намеренно не возвращает ошибку
([tasks.go:71](services/tasks/internal/service/tasks.go#L71)), а адаптер
[services/tasks/internal/events/publisher.go](services/tasks/internal/events/publisher.go)
лишь логирует сбой. Логика выбора:

- задача — уже в БД (источник истины), клиент получает **`201 Created`**
  независимо от того, ушло ли событие;
- если RabbitMQ недоступен — пишем `ERROR`-лог, но **не** возвращаем `500` и не
  откатываем создание задачи;
- если `RABBIT_URL` не задан или брокер не поднялся за отведённое время —
  сервис стартует с no-op публикатором (`nopEventPublisher`) и продолжает
  работать без событий (как и кэш Redis из ПЗ №9 — необязательная зависимость).

Альтернатива «строго» (вернуть `500`, если событие не ушло) тоже допустима по
заданию, но best-effort честнее для сценария «событие — это побочный эффект, а не
часть контракта создания задачи».

**Транспорт.** Подключение и публикация — в
[shared/rabbitmq/publisher.go](shared/rabbitmq/publisher.go): один раз при старте
открываем соединение и канал, объявляем очередь, далее публикуем в exchange по
умолчанию (`""`) с `routing key = task_events` — это прямая доставка в очередь
без отдельного exchange. Подключение на старте — с ограниченными ретраями
([services/tasks/cmd/tasks/main.go](services/tasks/cmd/tasks/main.go), функция
`buildPublisher`), чтобы переждать запуск брокера.

---

## 3. Consumer (worker): логика и где делается `ack`

Worker — отдельный сервис [services/worker](services/worker/). Точка входа
[cmd/worker/main.go](services/worker/cmd/worker/main.go), логика потребления —
[internal/consumer/consumer.go](services/worker/internal/consumer/consumer.go).

**Общая логика:**
1. подключается к RabbitMQ (с ретраями — может стартовать раньше брокера);
2. **объявляет ту же очередь** теми же параметрами через общий
   `rabbitmq.DeclareTaskQueue` (объявление идемпотентно — не важно, кто первый);
3. выставляет **prefetch** (`Qos`);
4. подписывается `Consume(..., autoAck=false, ...)` — подтверждаем вручную;
5. на каждое сообщение: разбирает JSON, **логирует** `получено событие
   task.created id=…`, затем `ack`.

**Где делается `ack`** —
[consumer.go:127](services/worker/internal/consumer/consumer.go#L127), сразу
после успешной обработки (в ПЗ №13 «обработка» = лог):

```go
c.log.Infof("получено событие %s id=%s", evt.Event, evt.TaskID)
// ack после успешной обработки — брокер удаляет сообщение из очереди.
if err := d.Ack(false); err != nil { ... }
```

**`nack` на ошибку.** Если тело не парсится как JSON («ядовитое» сообщение) —
[consumer.go:114](services/worker/internal/consumer/consumer.go#L114) делает
`d.Nack(false, requeue=false)`: сообщение **не** возвращается в очередь, чтобы не
зациклить бесконечную переобработку битого сообщения (в проде такие уходят в
dead-letter queue).

**Prefetch.** `ch.Qos(prefetch, 0, false)` —
[consumer.go:57](services/worker/internal/consumer/consumer.go#L57). В стенде
`PREFETCH=1`: брокер не отдаёт worker'у следующее сообщение, пока не получит
`ack` на текущее. Это ограничивает нагрузку (worker не «расхватывает» сразу всю
очередь) и при нескольких worker'ах честно распределяет сообщения между теми, кто
готов принять. Особенно важно, когда обработка тяжёлая.

**Graceful shutdown.** По `SIGINT`/`SIGTERM` контекст отменяется, цикл выходит,
канал и соединение закрываются — текущее сообщение успевает подтвердиться.

---

## 4. Надёжность: durable-очередь + persistent-сообщения

| Параметр | Значение | Где | Что даёт |
|----------|----------|-----|----------|
| Очередь `durable` | `true` | [rabbitmq.go:35](shared/rabbitmq/rabbitmq.go#L35) | сама очередь переживает рестарт брокера |
| Сообщения `persistent` | `DeliveryMode=2` | [publisher.go:72](shared/rabbitmq/publisher.go#L72) | уже опубликованные сообщения пишутся на диск и не теряются при рестарте брокера |

`durable` без `persistent` (или наоборот) недостаточно: очередь должна пережить
рестарт **и** сами сообщения в ней должны быть на диске. Проверка фактических
параметров очереди через management API:

```bash
curl -s -u guest:guest http://localhost:15672/api/queues/%2F/task_events
```
```json
{"name":"task_events","durable":true,"messages":0,"messages_ready":0,"consumers":1,"state":"running"}
```

`durable: true`, `consumers: 1` — очередь долговечна, к ней подключён один worker.

---

## 5. Демонстрация: POST → лог worker'а

**5.1. Запрос** (стек защищён CSRF — нужны парные cookie+заголовок, как в ПЗ №8):

```bash
curl -i -X POST http://localhost:8082/v1/tasks \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer demo-token" \
  -H "X-Request-ID: pz13-002" \
  -H "X-CSRF-Token: demo-csrf" -b "csrf_token=demo-csrf" \
  -d '{"title":"Rabbit","description":"publish event"}'
```
```text
HTTP/1.1 201 Created
X-Request-Id: pz13-002
Content-Length: 88

{"id":"t_77dfc1f6d7f55efe","title":"Rabbit","description":"publish event","done":false}
```

**5.2. Лог producer'а** (`tasks`) — событие ушло после создания задачи:

```json
{"component":"events","level":"info","msg":"published task.created event","request_id":"pz13-002","task_id":"t_77dfc1f6d7f55efe","service":"tasks"}
```

**5.3. Лог consumer'а** (`worker`) — событие получено и обработано:

```json
{"event":"task.created","event_ts":"2026-05-29T16:48:50Z","level":"info","msg":"получено событие task.created id=t_77dfc1f6d7f55efe","request_id":"pz13-002","task_id":"t_77dfc1f6d7f55efe","service":"worker"}
```

Один и тот же `request_id=pz13-002` и `task_id` прошли весь путь HTTP → producer →
очередь → consumer — сквозная трассировка работает.

---

## 6. Демонстрация развязки (почему брокер, а не HTTP)

Главное свойство брокера — producer и consumer **развязаны во времени**:
producer не ждёт consumer'а, сообщение копится в очереди. Проверка:

**6.1. Останавливаем worker и публикуем задачу (consumer offline):**

```bash
docker stop pz13_worker
curl -s -o /dev/null -w "HTTP %{http_code}\n" -X POST http://localhost:8082/v1/tasks \
  -H "Authorization: Bearer demo-token" -H "X-Request-ID: pz13-003" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: demo-csrf" -b "csrf_token=demo-csrf" \
  -d '{"title":"buffered","description":"queued while worker down"}'
# → HTTP 201  (создание задачи не зависит от worker'а)
```

Сообщение **ждёт в durable-очереди**, потребителей нет (`rabbitmqctl list_queues`):

```text
name          durable  messages  messages_ready  consumers
task_events   true      1          1               0
```

**6.2. Запускаем worker — он сразу разгребает накопленное:**

```bash
docker start pz13_worker
```
```json
{"event":"task.created","event_ts":"2026-05-29T16:50:26Z","level":"info","msg":"получено событие task.created id=t_0c7cf5059c3848c0","request_id":"pz13-003","service":"worker"}
```

Сообщение опубликовано в `16:50:26` (worker был выключен), а обработано в
`16:50:49` — **через ~23 секунды**, сразу после запуска consumer'а. После `ack`
очередь снова пуста (`messages 0`). Это и есть развязка: при синхронном HTTP
запрос бы провалился, пока получатель лежит; с брокером событие дождалось своего
обработчика.

---

## 7. Контрольные вопросы

1. **Зачем нужен брокер сообщений, если есть HTTP?** HTTP — синхронный вызов
   «здесь и сейчас»: если получатель недоступен или медленный, вызывающий ждёт
   или падает, и они жёстко связаны. Брокер **развязывает** producer и consumer
   во времени и по нагрузке: producer кладёт сообщение в очередь и сразу
   свободен (раздел 6 — задача создаётся, даже когда worker выключен); consumer
   читает в своём темпе. Плюс буферизация всплесков, повторные попытки,
   несколько независимых потребителей, сглаживание пиков. HTTP остаётся для
   синхронных «запрос-ответ», брокер — для асинхронных событий.

2. **Что такое `ack` и зачем он нужен?** `ack` (acknowledgement) — подтверждение
   потребителя, что сообщение **успешно обработано**; только после `ack` брокер
   удаляет его из очереди. Если consumer упал/отключился **до** `ack`, брокер
   считает сообщение необработанным и доставит его снова (другому или тому же
   consumer'у). Так достигается гарантия **at-least-once**: сообщение не
   потеряется из-за падения обработчика. В worker'е `ack` стоит **после** лога
   (успешной обработки) — [consumer.go:127](services/worker/internal/consumer/consumer.go#L127);
   на ошибке — `nack`.

3. **Почему возможна повторная доставка сообщения?** Потому что гарантия —
   at-least-once. Сообщение придёт повторно, если: consumer обработал его, но
   упал **до** отправки `ack`; `ack` потерялся из-за обрыва соединения; consumer
   сделал `nack`/`reject` с `requeue=true`; истёк таймаут доставки. Брокер не
   знает, выполнилась ли работа, если не получил `ack`, поэтому переотправляет.
   **Как с этим жить:** делать обработчики **идемпотентными** — повторная
   обработка того же `task_id` не должна давать двойного эффекта (проверять, не
   обработано ли уже; UPSERT вместо INSERT; дедуп по идентификатору события).

4. **Что делает `prefetch`?** Ограничивает число **неподтверждённых** (ещё не
   `ack`-нутых) сообщений, которые брокер отдаёт одному consumer'у одновременно
   (`ch.Qos`). При `prefetch=1` брокер не пришлёт следующее, пока не получит
   `ack` на текущее. Это (а) ограничивает нагрузку — worker не забирает в память
   сразу всю очередь, и (б) обеспечивает честное распределение между несколькими
   worker'ами: быстрый возьмёт больше, медленный — меньше, без перекоса.
   В стенде `PREFETCH=1` — [consumer.go:57](services/worker/internal/consumer/consumer.go#L57).

5. **Чем очередь `durable` отличается от non-durable?** `durable`-очередь
   **переживает рестарт брокера** — её метаданные сохраняются на диск, и после
   перезапуска она существует снова. Non-durable (transient) очередь при
   перезапуске брокера **исчезает**. Важная оговорка: `durable` относится к самой
   очереди; чтобы не терялись и **сообщения** в ней, их нужно публиковать
   `persistent` (`DeliveryMode=2`). В стенде включены оба:
   очередь `durable=true` и сообщения persistent (раздел 4) — поэтому событие
   переживёт и рестарт брокера, и временную недоступность consumer'а.

---
