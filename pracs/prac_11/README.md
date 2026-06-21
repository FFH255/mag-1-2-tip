## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №11

**Тема:** GraphQL API в Go на основе gqlgen: Query и Mutation.
**Технологии:** GraphQL, gqlgen, Go, `schema.graphqls`, резолверы, Playground/GraphiQL.
**Объект практики:** отдельный сервис [`services/graphql`](services/graphql/).

**Цель.** Спроектировать GraphQL-схему, сгенерировать серверный каркас gqlgen и
реализовать резолверы для запросов (Query) и изменений (Mutation), с проверкой
в Playground (в т.ч. с переменными).

> Все примеры ответов ниже — реальный вывод запущенного сервиса
> (`GRAPHQL_PORT=8090 go run ./services/graphql/cmd/graphql`, режим in-memory).

---

## 1. Где живёт код (структура сервиса)

Сделан **отдельный сервис** `graphql`, как рекомендует методичка:

```
services/graphql/
├── cmd/graphql/main.go            # точка входа: сервер + Playground (/), endpoint /query
├── gqlgen.yml                     # конфиг кодогенерации gqlgen
├── Dockerfile                     # сборка образа (как у auth/tasks)
├── graph/
│   ├── schema.graphqls            # ← СХЕМА ("контракт")
│   ├── resolver.go                # корневой Resolver + его зависимости
│   ├── schema.resolvers.go        # ← РЕЗОЛВЕРЫ ("реализация") — тут наша логика
│   ├── resolver_test.go           # юнит-тесты резолверов
│   ├── generated/generated.go     # АВТОГЕНЕРАЦИЯ gqlgen (не редактируется руками)
│   └── model/models_gen.go        # АВТОГЕНЕРАЦИЯ: входные типы Create/UpdateTaskInput
└── internal/
    ├── service/tasks.go           # доменная модель Task + бизнес-логика (TaskService)
    ├── repository/memory.go       # хранилище в памяти (для запуска без БД)
    ├── repository/postgres.go     # хранилище в той же таблице tasks, что и REST
    └── auth/auth.go               # упрощённая авторизация мутаций (опционально)
```

Ключевые файлы: [schema.graphqls](services/graphql/graph/schema.graphqls),
[schema.resolvers.go](services/graphql/graph/schema.resolvers.go),
[main.go](services/graphql/cmd/graphql/main.go).

---

## 2. Слой данных: единый источник истины

GraphQL-резолверы **не содержат бизнес-логики напрямую** — они делегируют в
[`service.TaskService`](services/graphql/internal/service/tasks.go) (валидация,
санитизация ввода, частичное обновление). За хранилищем стоит интерфейс
`TaskRepository` с двумя реализациями:

| Реализация | Когда | Источник данных |
|------------|-------|-----------------|
| `MemoryRepository` | `TASKS_DB_DSN` не задан (по умолчанию) | память процесса (с сидом `t_001`, `t_002`) |
| `PostgresRepository` | задан `TASKS_DB_DSN` | **та же таблица `tasks`**, что и у REST-сервиса |

> **Почему это «единый слой данных».** Go запрещает импортировать чужие
> `internal/`-пакеты, поэтому домен описан в самом сервисе graphql. Но единый
> источник истины обеспечивается **на уровне данных**: postgres-репозиторий
> ходит в ту же таблицу `tasks` (см. [миграцию](services/tasks/migrations/001_tasks.sql)),
> что и REST-сервис `tasks`. То есть GraphQL и REST читают/пишут одни и те же
> строки в одной БД. In-memory режим нужен только чтобы поднять Playground без
> PostgreSQL.

---

## 3. Схема GraphQL (контракт) и пояснение

Полностью — [services/graphql/graph/schema.graphqls](services/graphql/graph/schema.graphqls):

```graphql
type Task {
  id: ID!
  title: String!
  description: String
  done: Boolean!
}

type Query {
  tasks: [Task!]!          # список всех задач
  task(id: ID!): Task      # одна задача по id; null, если не найдена
}

input CreateTaskInput { title: String!, description: String }
input UpdateTaskInput { title: String, description: String, done: Boolean }

type Mutation {
  createTask(input: CreateTaskInput!): Task!
  updateTask(id: ID!, input: UpdateTaskInput!): Task!
  deleteTask(id: ID!): Boolean!
}
```

Пояснение типов и операций:

- **`Task`** — доменная модель. `id`, `title`, `done` обязательны (`!`),
  `description` — необязательно (может быть `null`).
- **Query.tasks** — `[Task!]!`: всегда массив (может быть пустым), элементы не `null`.
- **Query.task(id)** — возвращает `Task` или `null`, если задачи нет (сервер при
  этом не падает).
- **Mutation.createTask** — принимает `CreateTaskInput` (обязательный `title`),
  возвращает созданную задачу.
- **Mutation.updateTask** — частичное обновление: поля `UpdateTaskInput`
  необязательны, переданное `null`/опущенное поле **не меняет** значение.
- **Mutation.deleteTask** — `true`, если удалили; `false`, если задачи не было.

Тип `Task` в [gqlgen.yml](services/graphql/gqlgen.yml) замаплен прямо на
доменную модель `service.Task` — одна Go-структура и для маршалинга в GraphQL, и
для бизнес-логики (без лишних DTO).

---

## 4. Резолверы: где живут и как связаны с данными

Логика дописана в [schema.resolvers.go](services/graphql/graph/schema.resolvers.go).
Корневой `Resolver` хранит ссылку на `*service.TaskService`
([resolver.go](services/graphql/graph/resolver.go)). Каждый резолвер — тонкая
обёртка над методом сервиса:

| GraphQL-операция | Резолвер | Вызывает | Особое поведение |
|------------------|----------|----------|------------------|
| `Query.tasks` | `Tasks` | `svc.List` | `[]Task` → `[]*Task` |
| `Query.task(id)` | `Task` | `svc.Get` | не найдена → `null` (без ошибки) |
| `Mutation.createTask` | `CreateTask` | `svc.Create` | пустой `title` → GraphQL error |
| `Mutation.updateTask` | `UpdateTask` | `svc.Update` | `nil`-поля не меняются; нет задачи → error |
| `Mutation.deleteTask` | `DeleteTask` | `svc.Delete` | нет задачи → `false` (стабильнее, чем падать) |

«Контракт vs реализация»: `schema.graphqls` описывает **что** умеет API,
сгенерированный `generated.go` связывает схему с Go-типами, а
`schema.resolvers.go` — **как** это реализовано.

Резолверы покрыты тестами ([resolver_test.go](services/graphql/graph/resolver_test.go)):
`go test ./services/graphql/...` → `ok`.

---

## 5. Генерация gqlgen (как воспроизвести)

```bash
cd services/graphql
# конфиг и схема уже есть; перегенерировать каркас:
go run github.com/99designs/gqlgen@v0.17.81 generate
```

Результат — `graph/generated/generated.go` (исполняемая схема, маршалинг,
интерфейсы резолверов) и `graph/model/models_gen.go` (входные типы). Эти файлы
не редактируются руками; бизнес-логику дописываем только в `*.resolvers.go`.
Команда также зафиксирована в директиве `//go:generate` в `resolver.go`.

> Пути в `gqlgen.yml` относительны **рабочему каталогу**, поэтому генерацию
> запускаем именно из `services/graphql`.

---

## 6. Запуск сервиса

GraphQL слушает `:8090`, Playground доступен на `/`, запросы — POST на `/query`.

```bash
# из корня репозитория
export GRAPHQL_PORT=8090          # порт (по умолчанию 8090)
go run ./services/graphql/cmd/graphql
```

Проверка: открыть в браузере <http://localhost:8090/> (Playground) либо слать
POST на `/query`.

### Переменные окружения

| Переменная | По умолчанию | Назначение |
|------------|--------------|------------|
| `GRAPHQL_PORT` | `8090` | HTTP-порт сервиса |
| `TASKS_DB_DSN` | *(пусто)* | DSN PostgreSQL. Пусто → in-memory режим. Задан → общая с REST таблица `tasks` |
| `GRAPHQL_REQUIRE_AUTH` | `false` | `true` → мутации требуют Bearer-токен |
| `AUTH_GRPC_ADDR` | `localhost:50051` | адрес gRPC Auth для проверки токена (если авторизация включена) |

Лог старта (in-memory, без авторизации):

```text
{"level":"warning","msg":"TASKS_DB_DSN is not set: using in-memory repository (data is not persisted)",...}
{"level":"warning","msg":"authorization is DISABLED (GRAPHQL_REQUIRE_AUTH != true): all operations are open",...}
{"endpoint":"/query","msg":"server started","playground":"http://localhost:8090/","port":"8090",...}
```

### Docker (опционально)

Сервис добавлен в [deploy/docker-compose.yml](deploy/docker-compose.yml) с
`TASKS_DB_DSN`, указывающим на общий `db` — так GraphQL и REST работают с одной
БД. Образ собирается из [services/graphql/Dockerfile](services/graphql/Dockerfile).

---

## 7. Примеры запросов и мутаций (проверены в Playground)

Endpoint: `POST http://localhost:8090/query`. Ниже — запрос, переменные и
**реальный** ответ сервиса.

### 7.1. Получить список задач

```graphql
query { tasks { id title done } }
```
```json
{"data":{"tasks":[
  {"id":"t_001","title":"First task","done":false},
  {"id":"t_002","title":"Done task","done":true}
]}}
```

### 7.2. Получить задачу по id (с переменными)

```graphql
query GetTask($id: ID!) {
  task(id: $id) { id title description done }
}
```
Variables: `{ "id": "t_001" }`
```json
{"data":{"task":{"id":"t_001","title":"First task","description":"seeded sample","done":false}}}
```

Несуществующий id → `null`, сервер не падает:

```json
{"data":{"task":null}}
```

### 7.3. Создать задачу

```graphql
mutation Create($input: CreateTaskInput!) {
  createTask(input: $input) { id title done }
}
```
Variables:
```json
{ "input": { "title": "GraphQL task", "description": "created via mutation" } }
```
```json
{"data":{"createTask":{"id":"t_b0c0327790ffb13c","title":"GraphQL task","done":false}}}
```

### 7.4. Обновить задачу (частично — только `done`)

```graphql
mutation Update($id: ID!, $input: UpdateTaskInput!) {
  updateTask(id: $id, input: $input) { id title description done }
}
```
Variables: `{ "id": "t_001", "input": { "done": true } }`
```json
{"data":{"updateTask":{"id":"t_001","title":"First task","description":"seeded sample","done":true}}}
```

`title` и `description` не передавали — они остались прежними (частичное
обновление работает корректно).

### 7.5. Удалить задачу

```graphql
mutation Delete($id: ID!) { deleteTask(id: $id) }
```
Variables: `{ "id": "t_002" }` → `{"data":{"deleteTask":true}}`
Несуществующий id → `{"data":{"deleteTask":false}}`

### Доп. проверки

- Валидация: `createTask(input:{title:""})` →
  `{"errors":[{"message":"title is required","path":["createTask"]}],"data":null}`
- Защита от XSS: `title="<script>x</script>"` сохраняется как
  `&lt;script&gt;x&lt;/script&gt;` (санитизация на бэкенде).

---

## 8. Авторизация в GraphQL (упрощённо)

По умолчанию авторизация **отключена** — чтобы сфокусироваться на механике
GraphQL (это допускается методичкой и зафиксировано в логе старта warning'ом).

Реализован опциональный режим (`GRAPHQL_REQUIRE_AUTH=true`):

- **Как прокидывается.** HTTP-middleware
  ([auth.HTTPMiddleware](services/graphql/internal/auth/auth.go)) достаёт
  `Authorization: Bearer ...` из заголовка и кладёт токен в контекст запроса.
- **Где проверяется.** Операционный middleware gqlgen
  (`srv.AroundOperations(...)`) срабатывает **до резолверов**: для операций типа
  **Mutation** он требует валидный токен, а **Query** пропускает без проверки.
  Сам токен валидируется тем же сервисом **Auth по gRPC** (`Verify`), что и в
  REST, — единый источник правды об аутентификации.

Проверка логики (инстанс с `GRAPHQL_REQUIRE_AUTH=true`):

```text
# Query без токена — разрешён:
{"data":{"tasks":[{"id":"t_001","title":"First task"}, ...]}}

# Mutation без токена — отклонена ещё до бизнес-логики:
{"errors":[{"message":"unauthorized: valid Bearer token is required for mutations"}],"data":null}
```

---

## 9. Контрольные вопросы

1. **Чем отличаются Query и Mutation?** `Query` — операции чтения, не меняют
   состояние и могут выполняться параллельно. `Mutation` — операции изменения
   (создать/обновить/удалить); поля мутации в одном запросе выполняются строго
   последовательно. Семантически это аналог «GET vs POST/PUT/DELETE» в REST.

2. **Что такое GraphQL schema и почему это «контракт»?** Схема (SDL) описывает
   типы, поля и операции (Query/Mutation), их обязательность и связи. Это
   контракт между клиентом и сервером: клиент знает, что можно запросить и что
   придёт в ответ, а сервер обязан это реализовать. Схема же даёт интроспекцию,
   валидацию запросов и автодополнение в Playground.

3. **Что такое резолвер?** Функция/метод, который «разрешает» (вычисляет)
   значение конкретного поля схемы: например, `Query.tasks` или
   `Mutation.createTask`. Схема говорит *что* вернуть, резолвер — *как* это
   получить (из БД, кэша, другого сервиса). В нашем случае резолверы делегируют
   в `TaskService`.

4. **Почему GraphQL решает проблему over-fetching?** Клиент сам указывает,
   какие поля ему нужны, и получает ровно их — не больше. В REST эндпоинт обычно
   отдаёт фиксированный «толстый» объект, из-за чего по сети едут лишние поля
   (over-fetching) или приходится делать несколько запросов (under-fetching).
   В примере 7.1 мы попросили только `id/title/done` — `description` в ответ не
   попал.

5. **Риски GraphQL без ограничений сложности запросов?** Клиент может прислать
   очень «тяжёлый» или глубоко вложенный запрос (особенно при циклических
   связях), что приведёт к перегрузке БД/CPU и фактически к DoS. Поэтому в
   проде вводят ограничения: max depth/complexity, лимит размера запроса,
   таймауты, пагинацию, persisted queries, отключение интроспекции и rate
   limiting.
