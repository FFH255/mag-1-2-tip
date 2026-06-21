## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №12
**Тема:** Сравнение REST и GraphQL на одном домене (Tasks).
**Технологии:** REST (HTTP), GraphQL, анализ сценариев, измерение количества
запросов и объёма данных, обработка ошибок.
**Объект практики:** сервис `tasks` (REST, [services/tasks](services/tasks/)) и
сервис `graphql` (GraphQL, [services/graphql](services/graphql/)).

**Цель.** На практике понять сильные и слабые стороны REST и GraphQL: как
меняется клиентский сценарий, сколько нужно запросов, как устроены ошибки, что
легче кэшировать.

---

## 0. Как поднят стенд (воспроизводимость)

```bash
# из корня репозитория — собирает и поднимает db + auth + tasks + graphql
docker compose -f deploy/docker-compose.yml up -d --build
# остановить и удалить
docker compose -f deploy/docker-compose.yml down
```

Сопоставление сервисов и портов:

| Сервис | Контейнер | URL | Назначение |
|--------|-----------|-----|------------|
| REST | `pz7_tasks` | `http://localhost:8082/v1/tasks` | REST API задач |
| GraphQL | `pz11_graphql` | `http://localhost:8090/query` | GraphQL API задач |
| Auth (gRPC) | `pz7_auth` | `auth:50051` | проверка токена |
| PostgreSQL | `pz7_db` | общий | единый слой данных |

**Аутентификация.** Токен из Auth-сервиса — `demo-token`
(логин `student`/`student`). REST требует его на **всех** эндпоинтах
(`Authorization: Bearer demo-token`). У GraphQL в этом стенде авторизация
выключена (`GRAPHQL_REQUIRE_AUTH=false`) — фокус на механике API.

**CSRF.** REST защищён double-submit-cookie: на `POST/PATCH/DELETE` нужны
совпадающие cookie `csrf_token` и заголовок `X-CSRF-Token` (иначе `403 csrf
token missing`). В curl-примерах ниже это `-H "X-CSRF-Token: demo-csrf" -b
"csrf_token=demo-csrf"`. У GraphQL мутации идут тем же `POST /query`, отдельного
CSRF-обряда нет.

Таблица `tasks` на старте пустая (миграция не сидит данные), поэтому сценарий
начинается с создания двух задач через REST — после чего они видны **и через
REST, и через GraphQL** (общая БД):

```text
t_05badadd901246de  "Buy milk"          description="2 liters, lactose-free"
t_22ae59277fa7f29a  "Write PZ12 report" description="compare REST and GraphQL"
```

---

## 1. Зафиксированный UI-сценарий «Список + карточка детали»

| Экран / действие | Нужные поля |
|------------------|-------------|
| **Список** | `id`, `title`, `done` |
| **Деталь** (открытие карточки) | `id`, `title`, `description`, `done` |
| **Действие** | отметить задачу выполненной (`done = true`) |

Типовой путь пользователя: открыл список → открыл карточку → нажал «выполнено».

---

## 2. REST: запросы (curl) и ответы

### 2.1. Список — `GET /v1/tasks`

```bash
curl -s http://localhost:8082/v1/tasks -H "Authorization: Bearer demo-token"
```
```json
[{"id":"t_05badadd901246de","title":"Buy milk","description":"2 liters, lactose-free","done":false},
 {"id":"t_22ae59277fa7f29a","title":"Write PZ12 report","description":"compare REST and GraphQL","done":false}]
```
`HTTP 200` · **211 байт**. Ответ **фиксированный**: пришёл `description`, хотя
списку он не нужен (**over-fetching**). Поле `due_date` сейчас пустое и опущено
(`omitempty`), но если бы оно было задано — тоже приехало бы, и клиент не может
от него отказаться.

### 2.2. Деталь — `GET /v1/tasks/{id}`

```bash
curl -s http://localhost:8082/v1/tasks/t_05badadd901246de -H "Authorization: Bearer demo-token"
```
```json
{"id":"t_05badadd901246de","title":"Buy milk","description":"2 liters, lactose-free","done":false}
```
`HTTP 200` · **99 байт**. Здесь нужны все 4 поля — лишнего нет.

### 2.3. Действие — отметить done — `PATCH /v1/tasks/{id}`

```bash
curl -s -X PATCH http://localhost:8082/v1/tasks/t_05badadd901246de \
  -H "Content-Type: application/json" -H "Authorization: Bearer demo-token" \
  -H "X-CSRF-Token: demo-csrf" -b "csrf_token=demo-csrf" \
  -d '{"done":true}'
```
```json
{"id":"t_05badadd901246de","title":"Buy milk","description":"2 liters, lactose-free","done":true}
```
`HTTP 200` · **98 байт**. Частичное обновление: послали только `done`, остальные
поля сохранились. Ответ — снова **весь** объект (выбрать поля нельзя).

### 2.4. Создать — `POST /v1/tasks`

```bash
curl -s -X POST http://localhost:8082/v1/tasks \
  -H "Content-Type: application/json" -H "Authorization: Bearer demo-token" \
  -H "X-CSRF-Token: demo-csrf" -b "csrf_token=demo-csrf" \
  -d '{"title":"Buy milk","description":"2 liters, lactose-free"}'
```
```json
{"id":"t_05badadd901246de","title":"Buy milk","description":"2 liters, lactose-free","done":false}
```
`HTTP 201 Created`.

### 2.5. Ошибки REST (HTTP-статус + JSON)

| Ситуация | Запрос | Ответ |
|----------|--------|-------|
| Нет токена | `GET /v1/tasks` без `Authorization` | `HTTP 401` `{"error":"unauthorized"}` |
| Нет задачи | `GET /v1/tasks/does_not_exist` | `HTTP 404` `{"error":"task not found"}` |
| Пустой `title` | `POST /v1/tasks -d '{"title":""}'` | `HTTP 400` `{"error":"title is required"}` |

Семантика ошибки **выражена HTTP-статусом**; тело — единый формат `{"error": "..."}`.

---

## 3. GraphQL: запросы/мутации + variables + ответы

Endpoint: `POST http://localhost:8090/query`, тело — JSON `{ "query": ..., "variables": ... }`.

### 3.1. Список — выбираем ровно `id title done`

```graphql
query { tasks { id title done } }
```
```json
{"data":{"tasks":[
  {"id":"t_05badadd901246de","title":"Buy milk","done":false},
  {"id":"t_22ae59277fa7f29a","title":"Write PZ12 report","done":false}]}}
```
`HTTP 200` · **149 байт**. `description` **не пришёл** — мы его не просили
(нет over-fetching).

### 3.2. Деталь — `task(id)` с нужными полями (с variables)

```graphql
query GetTask($id: ID!) { task(id: $id) { id title description done } }
```
Variables: `{ "id": "t_05badadd901246de" }`
```json
{"data":{"task":{"id":"t_05badadd901246de","title":"Buy milk","description":"2 liters, lactose-free","done":false}}}
```
`HTTP 200` · **116 байт**. Полей ровно столько, сколько нужно, но ответ обёрнут
в `data → task` — отсюда +17 байт к REST-варианту (см. §4.2).

### 3.3. Действие — `updateTask` (с variables)

```graphql
mutation Update($id: ID!, $input: UpdateTaskInput!) {
  updateTask(id: $id, input: $input) { id title done }
}
```
Variables: `{ "id": "t_22ae59277fa7f29a", "input": { "done": true } }`
```json
{"data":{"updateTask":{"id":"t_22ae59277fa7f29a","title":"Write PZ12 report","done":true}}}
```
`HTTP 200` · **91 байт**. И в ответе мутации клиент тоже выбирает поля.

### 3.4. Список + деталь за **один** запрос (псевдонимы)

Сильная сторона GraphQL — собрать данные нескольких «экранов» в один round-trip:

```graphql
query Screen($id: ID!) {
  list:   tasks        { id title done }
  detail: task(id:$id) { id title description done }
}
```
Variables: `{ "id": "t_05badadd901246de" }`
```json
{"data":{"list":[{"id":"t_05badadd901246de","title":"Buy milk","done":true},
                 {"id":"t_22ae59277fa7f29a","title":"Write PZ12 report","done":true}],
         "detail":{"id":"t_05badadd901246de","title":"Buy milk","description":"2 liters, lactose-free","done":true}}}
```
`HTTP 200` · **253 байта в 1 запросе** против `211 + 99 = 310 байт в 2 запросах` у REST.

### 3.5. Ошибки GraphQL

| Ситуация | Ответ |
|----------|-------|
| Бизнес-ошибка (`createTask(input:{title:""})`) | `HTTP 200` `{"errors":[{"message":"title is required","path":["createTask"]}],"data":null}` |
| Нет задачи (`task(id:"does_not_exist")`) | `HTTP 200` `{"data":{"task":null}}` (не ошибка — просто `null`) |
| Ошибка схемы (запросили несуществующее поле) | `HTTP 422` `{"errors":[{"message":"Cannot query field \"nonExistentField\" on type \"Task\".",...,"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}` |

Ключевое: **бизнес-ошибки** едут с `HTTP 200` в поле `errors`, а статус
показывает только транспорт. Ошибки **валидации схемы** gqlgen всё же отдаёт с
`HTTP 422` — то есть статус у GraphQL «смешанный».

---

## 4. Сравнение по 4 критериям

### 4.1. Количество запросов на сценарий «список → деталь → done»

| | REST | GraphQL |
|---|------|---------|
| Запросов | **3** (`GET` список, `GET` деталь, `PATCH`) | **3** (query, query, mutation), либо **2**, если список и деталь слить в один query (§3.4) |
| Эндпоинтов | несколько URL (`/v1/tasks`, `/v1/tasks/{id}`) | один URL (`/query`) |

REST «жёстко» бьётся на ресурсы → под сложный экран легко получить *under-fetching*
(несколько запросов). GraphQL может собрать всё нужное за один round-trip.

### 4.2. Объём данных (реальные замеры `wc -c`)

| Шаг сценария | REST | GraphQL | Комментарий |
|--------------|------|---------|-------------|
| Список (`id,title,done`) | **211 B** | **149 B** | REST тащит лишний `description` → GraphQL на **62 B (~29 %) меньше** |
| Деталь (4 поля) | **99 B** | **116 B** | нужны все поля → REST компактнее: GraphQL добавляет обёртку `data/task` (+17 B) |
| Действие (mark done) | **98 B** | **91 B** | GraphQL вернул только `id/title/done` |
| Список + деталь | 310 B / **2 запроса** | 253 B / **1 запрос** | GraphQL и меньше байт, и меньше round-trip'ов |

Вывод по объёму: GraphQL выигрывает там, где у REST есть **лишние поля**
(over-fetching) или нужно **несколько запросов**. Но на экране «нужно всё» один
ресурс REST оказывается даже компактнее — за счёт отсутствия конвертной обёртки
`data`.

### 4.3. Ошибки и статусы

| | REST | GraphQL |
|---|------|---------|
| Где сигнал об ошибке | **HTTP-статус** (`401/404/400/...`) + `{"error":"..."}` | в основном `HTTP 200` + массив `errors[]`; ошибки схемы — `HTTP 422` |
| «Не найдено» | `404` | `data:null` либо `errors` |
| Удобство клиенту | привычно: статус → ветка обработки | надо парсить `errors` даже при `200`; статус не отражает бизнес-провал |
| Мониторинг | просто: считаем долю `4xx/5xx` по статусам | сложнее: `200` может скрывать ошибку → нужен разбор тела/метрики на уровне резолверов |

### 4.4. Кэширование (концептуально + наблюдение)

REST-ответы — это `GET` по конкретному URL, поэтому работает стандартный
HTTP-кэш (браузер, CDN, обратный прокси) по ключу = URL. Реальные заголовки
ответа REST на `GET /v1/tasks`:

```text
HTTP/1.1 200 OK
Content-Type: application/json
Content-Length: 209
X-Instance-Id: 740c0007d928
X-Request-Id: req-cb416a18c847b53e
```
(в нашем стенде явный `Cache-Control` не выставлен, но **сама модель** «GET + URL»
позволяет кэшировать без доработок).

GraphQL — это `POST` на **один** `/query`, тело запроса разное → URL-кэш не
работает «из коробки». Нужны доп. подходы: **persisted queries** (хешируем
запрос → можем кэшировать по хешу), кэш на уровне данных/полей (DataLoader,
Apollo cache), нормализованный клиентский кэш.

### Итоговая таблица сравнения

| Критерий | REST (`:8082`) | GraphQL (`:8090`) |
|----------|----------------|-------------------|
| Эндпоинты | много URL по ресурсам | один `POST /query` |
| Выбор полей | фиксированный ответ | клиент выбирает поля |
| Over-fetching | есть (`description` в списке) | нет |
| Under-fetching | вероятен (N запросов под экран) | решается одним query |
| Список (id,title,done) | 211 B | 149 B |
| Деталь (4 поля) | 99 B | 116 B |
| Запросов на сценарий | 3 | 3 (или 2) |
| Ошибки | HTTP-статус + `{"error"}` | `200` + `errors[]` (схема — `422`) |
| Кэширование | просто (HTTP-кэш по URL) | сложно (persisted queries, кэш данных) |
| Контракт | OpenAPI/Swagger отдельно | схема SDL + интроспекция встроены |

---

## 5. Итоговый вывод

1. **Число запросов** одинаково (3) для простого пути, но GraphQL умеет слить
   «список + деталь» в **1** запрос — преимущество на сложных экранах.
2. **Объём данных**: GraphQL устраняет over-fetching (список — 149 B против
   211 B, −29 %), зато добавляет обёртку `data`, поэтому на «нужно-всё» экране
   REST бывает компактнее.
3. **Ошибки**: у REST они выражены HTTP-статусом — клиенту и мониторингу проще;
   у GraphQL бизнес-ошибка приходит с `200` в `errors[]`, что требует разбора
   тела (хотя ошибки схемы gqlgen отдаёт с `422`).
4. **Кэширование**: REST кэшируется по URL почти бесплатно; GraphQL требует
   persisted queries / кэша на уровне данных.
5. **REST удобнее**, когда ресурсы простые и стабильные, важны HTTP-кэш, CDN и
   прозрачный мониторинг по статусам, а клиент — массовый/публичный.
6. **GraphQL оправдан**, когда экранам нужны разные срезы данных, важно убрать
   over/under-fetching и сократить round-trip'ы (мобильные/составные UI), а
   контракт должен быть строго типизированным и интроспектируемым.
7. На нашем домене Tasks разница невелика — он мелкий и плоский; выгода GraphQL
   растёт с ростом числа связей и разнообразия экранов.

---

## 6. Контрольные вопросы

1. **Что такое over-fetching и under-fetching?** *Over-fetching* — сервер
   присылает больше полей, чем нужно экрану (REST-список выше тащит
   `description`). *Under-fetching* — одного ответа не хватает и приходится
   делать ещё запросы (например, список, а потом деталь по каждому элементу —
   проблема N+1). GraphQL борется с обоими: клиент выбирает поля и может собрать
   несколько выборок в один запрос (§3.4).

2. **Почему GraphQL опасен без ограничений сложности запросов?** Один `POST`
   может содержать глубоко вложенный/широкий запрос (особенно при связях), и
   сервер обязан его выполнить — это нагрузка на БД/CPU вплоть до DoS. Поэтому
   вводят лимиты глубины и сложности, размер запроса, таймауты, пагинацию,
   persisted queries, отключение интроспекции и rate limiting.

3. **Почему кэширование REST обычно проще?** REST-чтение — это `GET` по
   уникальному URL, а HTTP-кэш (браузер, CDN, прокси) штатно кэширует по
   методу+URL+заголовкам. У GraphQL один URL и `POST` с разным телом — общий
   HTTP-кэш не применим, нужны persisted queries или кэш на уровне данных.

4. **Чем отличаются ошибки REST и GraphQL с точки зрения клиента?** В REST
   статус-код сразу говорит, что случилось (`401/404/400`), тело — формат
   `{"error":...}`; ветвление и мониторинг строятся на статусах. В GraphQL
   бизнес-ошибка обычно приходит с `HTTP 200` и лежит в `errors[]` (а `data`
   может быть `null`/частичной), поэтому клиент обязан разбирать тело даже при
   `200`; «не найдено» часто выражается как `data:null`, а не ошибкой.

5. **Когда REST, а когда GraphQL?** **REST** — простые стабильные ресурсы,
   важны HTTP-кэш/CDN, прозрачный мониторинг по статусам, публичный API с
   простыми клиентами. **GraphQL** — много экранов с разными наборами полей,
   нужно убрать over/under-fetching и round-trip'ы (мобильный/составной UI),
   агрегирование нескольких источников, строгий типизированный контракт с
   интроспекцией.
