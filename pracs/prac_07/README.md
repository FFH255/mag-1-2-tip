## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №7

**Тема:** контейнеризация Go-сервисов `tasks` и `auth`, multi-stage Dockerfile, запуск связки через docker-compose.

## 1. Структура файлов

```
prac_07/
├── .dockerignore                     # общий контекст сборки
├── go.mod                            # модуль один на весь проект
├── services/
│   ├── auth/
│   │   ├── cmd/auth/main.go
│   │   └── Dockerfile                # multi-stage build для auth
│   └── tasks/
│       ├── cmd/tasks/main.go
│       └── Dockerfile                # multi-stage build для tasks
└── deploy/
    └── docker-compose.yml            # связка auth + tasks + postgres
```

> В этом репозитории **один Go-модуль** на корне (`go.mod`), а не отдельный модуль на сервис.
> Поэтому контекст сборки — корень проекта, а Dockerfile подключается через `-f`.

## 2. Dockerfile — пояснение стадий

### Stage 1 — `builder` (golang:1.25-alpine)
1. `WORKDIR /src` — рабочая директория внутри образа.
2. Копируются только `go.mod` / `go.sum`, выполняется `go mod download`.
   Это даёт **кеширование зависимостей**: пока эти файлы не меняются,
   слой со скачанными модулями переиспользуется.
3. Копируется остальной код (`COPY . .`).
4. Сборка статического бинарника:
   `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./services/<svc>/cmd/<svc>`.
   Флаги `-trimpath` и `-ldflags="-s -w"` уменьшают размер и убирают пути сборщика.

### Stage 2 — `runner` (alpine:3.20)
1. Минимальный образ Alpine (~7 МБ).
2. Ставятся только `ca-certificates` (для исходящих TLS) и заводится непривилегированный пользователь `app`.
3. Из `builder` копируется уже готовый бинарник в `/usr/local/bin/app`.
4. `EXPOSE` — только информационная метка (для tasks 8082, для auth 8085 + gRPC 50051).
5. `ENTRYPOINT ["/usr/local/bin/app"]` — единая точка входа.

В итоговом образе **нет** ни компилятора Go, ни исходников, ни `.git`.

## 3. Команды сборки и запуска

### 3.1. Поштучно — без compose

Из корня проекта:

```bash
# Сборка
docker build -t techip-tasks:0.1 -f services/tasks/Dockerfile .
docker build -t techip-auth:0.1  -f services/auth/Dockerfile  .

# Запуск auth
docker run --rm -d --name auth_dev \
  -p 8085:8085 -p 50051:50051 \
  -e AUTH_PORT=8085 -e AUTH_GRPC_PORT=50051 \
  techip-auth:0.1

# Запуск tasks (на Windows/Mac — host.docker.internal до auth/postgres на хосте)
docker run --rm -p 8082:8082 \
  -e TASKS_PORT=8082 \
  -e AUTH_GRPC_ADDR=host.docker.internal:50051 \
  -e TASKS_DB_DSN="postgres://tasks:tasks@host.docker.internal:5432/tasks?sslmode=disable" \
  techip-tasks:0.1
```

### 3.2. Через compose (рекомендуется)

Из папки `deploy/`:

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f tasks
docker compose down -v   # остановить и снести том БД
```

## 4. Переменные окружения

| Сервис | Переменная        | Назначение                              | Пример (compose)                                      |
|--------|-------------------|-----------------------------------------|-------------------------------------------------------|
| auth   | `AUTH_PORT`       | HTTP-порт                               | `8085`                                                |
| auth   | `AUTH_GRPC_PORT`  | gRPC-порт                               | `50051`                                               |
| tasks  | `TASKS_PORT`      | HTTP-порт                               | `8082`                                                |
| tasks  | `AUTH_GRPC_ADDR`  | адрес auth (gRPC) внутри сети           | `auth:50051`                                          |
| tasks  | `TASKS_DB_DSN`    | строка подключения к Postgres           | `postgres://tasks:tasks@db:5432/tasks?sslmode=disable`|

Конфиг **не вшивается** в образ — все значения подставляются через `environment:` в compose
или через `-e` в `docker run`.

## 5. Взаимодействие сервисов внутри docker-сети

`docker compose up` создаёт общую сеть, в которой каждый сервис доступен по своему имени:

```
+---------+        gRPC :50051        +--------+
|  tasks  | ────────────────────────► |  auth  |
+---------+                           +--------+
     │
     │ TCP :5432
     ▼
+---------+
|   db    |  (postgres:16-alpine, healthcheck pg_isready)
+---------+
```

Поэтому в `tasks` указано `AUTH_GRPC_ADDR=auth:50051`, а не `localhost:50051`:
**в docker-сети localhost — это сам контейнер**, а не хост и не другой контейнер.

`tasks` запускается только после того, как `db` прошёл healthcheck (`depends_on.condition: service_healthy`).

## 6. Пример проверки через curl

```bash
# Список задач (требуется заголовок Authorization)
curl -i http://localhost:8082/v1/tasks \
  -H "Authorization: Bearer demo-token" \
  -H "X-Request-ID: pz7-001"
```

## 7. Контрольные вопросы

1. **Image vs container.** Image — неизменяемый шаблон (слои файловой системы + метаданные).
   Container — запущенный экземпляр image с собственным изменяемым слоем, процессами и сетью.
   Из одного image можно поднять сколько угодно контейнеров.
2. **Зачем multi-stage build.** Чтобы инструменты сборки (компилятор Go, заголовки) и исходники
   остались в промежуточной стадии, а в финальный образ попал только бинарник. Образ становится
   меньше, безопаснее (меньше CVE-поверхность) и быстрее тянется/раскатывается.
3. **Почему секреты не кладут в Dockerfile.** Каждая инструкция формирует слой; слои
   попадают в реестр и в `docker history`. Любой, у кого есть доступ к образу, увидит секрет.
   Используются переменные окружения, secret-стораджи, `--secret` build-mounts, либо внешние
   менеджеры (Vault, AWS Secrets Manager и т.п.).
4. **Почему в docker-сети нельзя ходить на localhost другого контейнера.** У каждого контейнера
   свой сетевой namespace, и `localhost` указывает на петлю самого контейнера. Чтобы дойти
   до другого контейнера, нужно использовать его имя в compose-сети (DNS внутри сети) или его IP.
5. **Зачем .dockerignore.** Он урезает контекст сборки, который Docker отправляет демону:
   меньше трафика, быстрее сборка, лучше работает кеш слоёв и в образ не попадают
   `.git`, локальные `bin/`, временные файлы и секреты вроде `.env`.

## 8. Типовые проблемы и как их избежали

- **Сборка не находит файлы** — контекст сборки указан как корень проекта (`context: ..`),
  и Dockerfile подключается через `-f services/<svc>/Dockerfile`.
- **Сервис недоступен снаружи** — оба сервиса слушают `:PORT`, что разворачивается в `0.0.0.0:PORT`,
  а не `127.0.0.1`.
- **Tasks не видит auth** — внутри compose используется DNS-имя `auth:50051`, не `localhost`.
- **В образ попадает мусор** — `.dockerignore` исключает `.git`, `bin`, `deploy`, `docs`, логи и `.env`.
