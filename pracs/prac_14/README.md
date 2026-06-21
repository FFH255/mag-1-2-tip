## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №14
**Тема:** Очередь задач producer–consumer с повторными попытками, DLQ и
идемпотентностью.
**Технологии:** RabbitMQ, DLX/DLQ, TTL, retry policy, `message_id`, обработка
дублей, Go-клиент [`rabbitmq/amqp091-go`](https://github.com/rabbitmq/amqp091-go).
**Объект практики:** сервис `tasks` (producer, endpoint
[`POST /v1/jobs/process-task`](services/tasks/internal/http/handler.go#L138)) +
сервис `worker` (consumer задач,
[services/worker/internal/jobconsumer](services/worker/internal/jobconsumer/jobconsumer.go))
с «тяжёлой» обработкой.

**Цель.** Построить рабочую очередь задач, которая устойчиво обрабатывает ошибки:
временные ошибки **ретраятся** с задержкой, «плохие» сообщения уходят в **DLQ**, а
обработчик устойчив к дублям (**идемпотентен**).

---

## 0. Как поднят стенд (воспроизводимость)

Весь стек поднимается одной командой из корня репозитория
([deploy/docker-compose.yml](deploy/docker-compose.yml) — в сервис `worker`
добавлены переменные retry-политики `MAX_ATTEMPTS` / `RETRY_TTL_MS` / `WORK_MS`):

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

Реальный статус контейнеров (`docker compose ps`):

```text
NAME           SERVICE    STATUS
pz13_rabbit    rabbitmq   Up (healthy)
pz13_worker    worker     Up
pz7_tasks      tasks      Up
pz7_auth       auth       Up
pz7_db         db         Up (healthy)
...
```

> Всё ниже — **реальный вывод запущенного стека**. Producer — REST-сервис `tasks`
> (`http://localhost:8082`), брокер RabbitMQ (AMQP `:5672`, management UI `:15672`,
> `guest`/`guest`), consumer — сервис `worker`. Очереди и тела сообщений проверены
> через `rabbitmqctl list_queues` и management HTTP API.

При старте worker объявляет всю топологию и подписывается на обе очереди:

```json
{"consumer":"jobs","msg":"job worker subscribed, waiting for jobs","queue":"task_jobs","prefetch":1,"max_attempts":3,"retry_ttl":"5s","level":"info"}
{"msg":"worker subscribed, waiting for messages","queue":"task_events","prefetch":1,"level":"info"}
```

---

## 1. Топология очередей и маршрутизация

Объявляется в [shared/rabbitmq/jobs.go](shared/rabbitmq/jobs.go) — общий источник
истины о схеме для producer'а и consumer'а. Три очереди (все `durable`):

| Очередь | Роль | Особые аргументы |
|---------|------|------------------|
| [`task_jobs`](shared/rabbitmq/jobs.go#L62) | основная рабочая очередь | — |
| [`task_jobs_retry`](shared/rabbitmq/jobs.go#L84) | отложенный ретрай (backoff) | `x-message-ttl=5000`, `x-dead-letter-exchange=""`, `x-dead-letter-routing-key=task_jobs` |
| [`task_jobs_dlq`](shared/rabbitmq/jobs.go#L84) | DLQ — «плохие» сообщения после исчерпания попыток | — |

```text
              POST /v1/jobs/process-task
                        │
                        ▼
   producer ──publish──▶ [ task_jobs ] ──consume──▶ worker (sleep 2s)
                              ▲                         │
                  TTL 5s,     │                    success│        fail
                  DLX ────────┘                         │           │
                  back to                          ack + mark   attempt<max ? │ : attempt>=max
                  task_jobs                          (готово)        │           │
                              ┌──────────────────────────────────────┘           │
                              ▼ publish (attempt+1)                               ▼ publish
                       [ task_jobs_retry ] ──(по TTL, через DLX)──▶ task_jobs   [ task_jobs_dlq ]
```

**Как работает DLX (dead-letter exchange).** `task_jobs_retry` сама ничего не
обрабатывает — это «таймер». Сообщение лежит в ней `x-message-ttl` (5 c), затем
**истекает** и брокер по правилу dead-letter (`x-dead-letter-routing-key=task_jobs`)
**сам** перекладывает его обратно в основную очередь. Так получается отложенная
повторная попытка без отдельного планировщика. Фактические аргументы очереди из
management HTTP API:

```bash
curl -s -u guest:guest http://localhost:15672/api/queues/%2F/task_jobs_retry
```
```json
{"name":"task_jobs_retry","durable":true,"arguments":{"x-message-ttl":5000,"x-dead-letter-exchange":"","x-dead-letter-routing-key":"task_jobs"}}
```

> **Почему аргументы объявляет только worker.** Аргументы очереди в RabbitMQ
> неизменяемы: повторное объявление с другими параметрами падает с
> `PRECONDITION_FAILED`. Поэтому retry-очередь и DLQ (со спец-аргументами)
> объявляет только worker ([DeclareJobTopology](shared/rabbitmq/jobs.go#L84)), а
> producer трогает лишь `task_jobs` (без аргументов,
> [DeclareJobsQueue](shared/rabbitmq/jobs.go#L62)) — её объявление идемпотентно с
> обеих сторон.

В DLQ сообщения попадают **двумя** путями: (1) явной публикацией worker'а после
исчерпания попыток (основной сценарий), и (2) «ядовитые» нераспарсиваемые тела —
worker кладёт сырое тело в DLQ с заголовком `x-dead-letter-reason: malformed-json`
([jobconsumer.go:117](services/worker/internal/jobconsumer/jobconsumer.go#L116)),
чтобы не потерять их и не зациклить.

---

## 2. Формат сообщения job

Тип [`JobMessage`](shared/rabbitmq/jobs.go#L44) (JSON). Реально опубликованное тело:

```json
{"job":"process_task","task_id":"t_fail","attempt":3,"message_id":"1e3ee765-1274-4f66-b87c-9f280250f8f2","error":"simulated processing error for task_id=t_fail","request_id":"pz14-fail"}
```

| Поле | Зачем |
|------|-------|
| `job` | тип задачи (`process_task`); по нему consumer ветвит обработку |
| `task_id` | идентификатор задачи — над ней выполняется «работа» |
| `attempt` | **номер попытки** (с 1). Растёт при каждом ретрае — основа retry-политики (хранится в payload) |
| `message_id` | **UUID задачи** — ключ идемпотентности. Стабилен между ретраями (та же задача), дублируется и в AMQP-свойстве `message_id` |
| `error` | текст последней ошибки; заполняется при уходе в retry/DLQ — чтобы при разборе DLQ было видно, **почему** упало |
| `request_id` | сквозная трассировка `X-Request-ID`: HTTP → producer → очередь → consumer |

Счётчик попыток выбран **в payload** (а не в headers) — он виден в management UI и
естественно переживает republish. Сообщения публикуются `persistent`
(`DeliveryMode=2`) — переживают рестарт брокера.

---

## 3. Producer: постановка задачи в очередь

Endpoint [`POST /v1/jobs/process-task`](services/tasks/internal/http/handler.go#L138)
(отдельный от создания сущности — чтобы демонстрировать именно **очередь задач**).
Адаптер публикации — [services/tasks/internal/jobs/publisher.go](services/tasks/internal/jobs/publisher.go).

```bash
curl -i -X POST http://localhost:8082/v1/jobs/process-task \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer demo-token" \
  -H "X-CSRF-Token: demo-csrf" -b "csrf_token=demo-csrf" \
  -d '{"task_id":"t_001"}'
```
```text
HTTP/1.1 202 Accepted

{"attempt":1,"job":"process_task","message_id":"f99e38a6-3b1c-4a81-8d45-73e284a5b008","queue":"task_jobs","status":"queued","task_id":"t_001"}
```

Отвечаем **`202 Accepted`** («принято к обработке»), а не `201` — задача лишь
поставлена в очередь, результат будет позже у worker'а. `message_id` можно передать
в теле (`{"task_id":"...","message_id":"..."}`) — это позволяет повторно послать ту
же задачу и показать дедупликацию (см. 14.7); если не передан — генерируется UUID
v4 ([NewMessageID](shared/rabbitmq/jobs.go#L134)).


Лог producer'а (`tasks`):
```json
{"component":"jobs","msg":"enqueued process_task job","task_id":"t_fail","message_id":"1e3ee765-...","request_id":"pz14-fail","level":"info"}
```

---

## 4. Consumer: логика обработки и где `ack`

Логика в [jobconsumer.go](services/worker/internal/jobconsumer/jobconsumer.go),
метод [`handle`](services/worker/internal/jobconsumer/jobconsumer.go#L116):

1. разобрать JSON (битое тело → DLQ + `ack`);
2. **проверить дубль** по `message_id` ([:141](services/worker/internal/jobconsumer/jobconsumer.go#L141)) — если уже обработан, `ack` без работы;
3. выполнить «работу» — `sleep 2s` (прерывается по shutdown);
4. **успех** → пометить `message_id` обработанным + [`ack`](services/worker/internal/jobconsumer/jobconsumer.go#L167);
5. **ошибка** → retry или DLQ (см. 14.5), исходное сообщение **в любом случае `ack`**.

**Где `ack`.** При успехе — [jobconsumer.go:167](services/worker/internal/jobconsumer/jobconsumer.go#L167);
после планирования ретрая — [:194](services/worker/internal/jobconsumer/jobconsumer.go#L194);
после отправки в DLQ — [:210](services/worker/internal/jobconsumer/jobconsumer.go#L210).
Это ключевое требование методички: **в retry-ветке нельзя оставлять исходное
сообщение `unacked`** — иначе при разрыве/redelivery начнутся неконтролируемые
повторы. Мы сначала публикуем копию (в retry/DLQ), затем `ack` оригинала. `prefetch=1`
(`Qos`) — worker не «расхватывает» очередь и при тяжёлой обработке берёт по одному.

---

## 5. Retry-политика

| Параметр | Значение | Где |
|----------|----------|-----|
| Что считается «ошибкой» | детерминированно: `task_id` содержит `fail` **или** оканчивается на `3` | [`doWork`](services/worker/internal/jobconsumer/jobconsumer.go#L234) |
| Максимум попыток | `MAX_ATTEMPTS=3` | env worker'а |
| Задержка (backoff) | `RETRY_TTL_MS=5000` (5 c) через TTL-очередь | [DeclareJobTopology](shared/rabbitmq/jobs.go#L84) |

Ошибку моделируем **детерминированно** (а не случайно, хотя методичка допускает оба)
— ради повторяемого лога: `t_001` всегда успешен, `t_fail` всегда падает.

**Алгоритм** ([onFailure](services/worker/internal/jobconsumer/jobconsumer.go#L175)):
обрабатываем сообщение с `attempt = N`. Если упало и `N < max_attempts` →
republish в `task_jobs_retry` с `attempt = N+1` (через 5 c вернётся в `task_jobs`);
если `N >= max_attempts` → republish в `task_jobs_dlq`. При `max=3` это даёт ровно
**3 попытки, затем DLQ**:

```text
attempt 1 (N=1<3) → fail → retry (→2)
attempt 2 (N=2<3) → fail → retry (→3)
attempt 3 (N=3≥3) → fail → DLQ
```

`message_id` при ретрае **сохраняется** (та же логическая задача) — поэтому
идемпотентность совместима с ретраями: повтор ещё-не-успешной задачи не считается
дублем. Если сам publish в retry/DLQ не удался — делаем `Nack(requeue=true)`, чтобы
не потерять задачу.

---

## 6. DLQ: зачем и как увидеть

DLQ (`task_jobs_dlq`) нужна, чтобы: **не терять** необрабатываемые сообщения, **не
мешать** основной очереди (они не крутятся в ней бесконечно) и дать возможность
**проанализировать/переобработать** их вручную.

Увидеть попадание в DLQ можно тремя способами — **(1) лог worker'а:**
```json
{"consumer":"jobs","task_id":"t_fail","attempt":3,"dlq":"task_jobs_dlq","msg":"max attempts (3) exhausted → job sent to DLQ","level":"error"}
```
**(2) счётчик очереди** (`rabbitmqctl list_queues`):
```text
name            messages  consumers
task_jobs       0         1
task_jobs_dlq   1         0
task_jobs_retry 0         0
```
**(3) чтение тела** из DLQ (management HTTP API) — видно `attempt:3`, тот же
`message_id`, причину `error` и `request_id` для трассировки:
```json
{"payload":"{\"job\":\"process_task\",\"task_id\":\"t_fail\",\"attempt\":3,\"message_id\":\"1e3ee765-1274-4f66-b87c-9f280250f8f2\",\"error\":\"simulated processing error for task_id=t_fail\",\"request_id\":\"pz14-fail\"}","properties":{"message_id":"1e3ee765-...","delivery_mode":2,"content_type":"application/json"}}
```

---

## 7. Идемпотентность

Из-за `ack`/retry система — **at-least-once**: одно и то же сообщение может
обработаться повторно (например, worker упал после выполнения работы, но **до**
`ack` — брокер передоставит). Защита от дублей:

| Вопрос | Ответ |
|--------|-------|
| Что является ключом | `message_id` (UUID), стабилен между ретраями одной задачи |
| Где хранится | in-memory `map[string]struct{}` под мьютексом — [`processedStore`](services/worker/internal/jobconsumer/jobconsumer.go#L249) (учебно достаточно; в проде — Redis/БД, чтобы пережить рестарт и быть общим для нескольких worker'ов) |
| Как проверяется | перед работой — [`isProcessed`](services/worker/internal/jobconsumer/jobconsumer.go#L141); после успеха — `mark`; дубль → `ack` без повторного выполнения |

---

## 8. Демонстрация логами (реальный вывод)

**Сценарий 1 — успешная обработка** (`t_001`, ~2 c работы):
```json
{"consumer":"jobs","task_id":"t_001","attempt":1,"msg":"processing job process_task task_id=t_001 (attempt 1/3)","ts":"2026-05-29T17:13:30.019Z","level":"info"}
{"consumer":"jobs","task_id":"t_001","attempt":1,"msg":"job done successfully, ack","ts":"2026-05-29T17:13:32.021Z","level":"info"}
```

**Сценарий 2 — несколько ретраев → DLQ** (`t_fail`, backoff 5 c между попытками):
```json
{"task_id":"t_fail","attempt":1,"msg":"processing job process_task task_id=t_fail (attempt 1/3)","ts":"2026-05-29T17:13:51.597Z","level":"info"}
{"task_id":"t_fail","attempt":1,"error":"simulated processing error for task_id=t_fail","msg":"job failed","ts":"2026-05-29T17:13:53.598Z","level":"warning"}
{"task_id":"t_fail","attempt":1,"next_attempt":2,"msg":"scheduled retry: attempt 2 → retry queue (backoff 5s)","ts":"2026-05-29T17:13:53.599Z","level":"info"}
{"task_id":"t_fail","attempt":2,"msg":"processing job process_task task_id=t_fail (attempt 2/3)","ts":"2026-05-29T17:13:58.602Z","level":"info"}
{"task_id":"t_fail","attempt":2,"next_attempt":3,"msg":"scheduled retry: attempt 3 → retry queue (backoff 5s)","ts":"2026-05-29T17:14:00.604Z","level":"info"}
{"task_id":"t_fail","attempt":3,"msg":"processing job process_task task_id=t_fail (attempt 3/3)","ts":"2026-05-29T17:14:05.607Z","level":"info"}
{"task_id":"t_fail","attempt":3,"error":"simulated processing error for task_id=t_fail","msg":"job failed","ts":"2026-05-29T17:14:07.605Z","level":"warning"}
{"task_id":"t_fail","attempt":3,"dlq":"task_jobs_dlq","msg":"max attempts (3) exhausted → job sent to DLQ","ts":"2026-05-29T17:14:07.606Z","level":"error"}
```
Видно ровно по методичке: попытка 1 → retry, попытка 2 → retry, попытка 3 → DLQ.
Между попытками — **~5 c** (17:13:53 → 17:13:58 → … ), это и есть backoff из TTL.

**Сценарий 3 — идемпотентность** (один и тот же `message_id=idem-demo-42` дважды):
```json
{"message_id":"idem-demo-42","task_id":"t_777","attempt":1,"request_id":"pz14-dup-1","msg":"processing job process_task task_id=t_777 (attempt 1/3)","level":"info"}
{"message_id":"idem-demo-42","task_id":"t_777","attempt":1,"request_id":"pz14-dup-1","msg":"job done successfully, ack","level":"info"}
{"message_id":"idem-demo-42","task_id":"t_777","attempt":1,"request_id":"pz14-dup-2","msg":"duplicate job (message_id already processed), skipping — idempotent ack","level":"warning"}
```
Первый запрос (`pz14-dup-1`) выполнил работу и пометил `message_id`; второй
(`pz14-dup-2`) с тем же `message_id` — **пропущен** без повторного выполнения.

---

## 9. Контрольные вопросы

1. **Чем «job queue» отличается от «event queue»?** *Event* — уведомление о
   свершившемся факте («задача создана»); обработка лёгкая, потеря/повтор часто
   терпимы. *Job* — поручение выполнить работу; обработка тяжёлая и может падать,
   поэтому нужны гарантии выполнения: ретраи, DLQ, идемпотентность, контроль
   результата. В нашем коде событие `task.created` → просто лог (ПЗ №13), а job
   `process_task` → `sleep 2s` + ретраи/DLQ (ПЗ №14).

2. **Почему система очередей часто работает как at-least-once?** Потому что
   подтверждение (`ack`) и собственно работа — не атомарны. Если worker выполнил
   задачу, но упал/потерял соединение **до** `ack`, брокер не знает, что работа
   сделана, и **передоставит** сообщение. Гарантировать «ровно один раз»
   (exactly-once) в общем случае нельзя без распределённых транзакций, поэтому
   практичный компромисс — at-least-once + идемпотентный обработчик.

3. **Как DLQ помогает эксплуатации?** Изолирует «ядовитые» сообщения: они не
   крутятся бесконечно в основной очереди и не блокируют нормальные задачи. Их не
   теряют — можно разобрать причину (в нашем `error`-поле прямо записана), починить
   код/данные и **переотправить**. DLQ — это «карантин» + точка наблюдаемости
   (всплеск размера DLQ = сигнал об инциденте).

4. **Почему ретраи нельзя делать бесконечно?** Постоянно падающая задача
   («ядовитое» сообщение или системный сбой) при бесконечных ретраях заберёт все
   ресурсы worker'а, заблокирует обработку нормальных задач и будет вечно
   гонять трафик. Лимит (`max_attempts`) + backoff (TTL) ограничивают ущерб, а DLQ
   принимает то, что так и не удалось обработать, — деградация контролируемая.

5. **Что такое идемпотентность и как реализовать минимально?** Идемпотентность —
   свойство операции давать **один и тот же эффект** при повторном выполнении
   (повтор не «удваивает» результат). Минимально: дать сообщению уникальный
   `message_id`, хранить множество уже обработанных id и при повторе **пропускать**
   работу (как в [`processedStore`](services/worker/internal/jobconsumer/jobconsumer.go#L249)).
   На учебном уровне хватает `map` в памяти; в проде — общее хранилище (Redis/БД)
   или естественная идемпотентность через `UPSERT`/уникальный ключ в БД.

---
