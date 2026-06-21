## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №10

**Тема:** горизонтальное масштабирование и балансировка нагрузки.
**Технологии:** Docker Compose, несколько реплик сервиса, NGINX upstream, health endpoint, идентификация инстанса.
**Объект практики:** сервис `tasks` (2 реплики) + NGINX как load balancer.

**Цель.** Запустить несколько экземпляров одного сервиса, распределить трафик
через NGINX и убедиться, что запросы реально балансируются, а система устойчиво
работает при падении одной реплики.

---

## 1. Сколько реплик и как они конфигурируются

Запущено **2 реплики** сервиса `tasks` за балансировщиком NGINX. Образ у них
один и тот же (`techip-tasks:0.1`) — отличаются только переменными окружения.
Наружу реплики **не** публикуются; единственная точка входа — NGINX на `:8080`.

| Параметр | tasks_1 | tasks_2 | Назначение |
|----------|---------|---------|------------|
| `INSTANCE_ID` | `tasks-1` | `tasks-2` | попадает в заголовок ответа `X-Instance-ID` |
| `TASKS_PORT` | `8082` | `8082` | внутренний HTTP-порт (наружу не проброшен) |
| `AUTH_GRPC_ADDR` | `auth:50051` | `auth:50051` | общий сервис авторизации |
| `TASKS_DB_DSN` | `…@db:5432/tasks` | `…@db:5432/tasks` | **общий** PostgreSQL — единый источник данных |
| `REDIS_ADDR` | `redis:6379` | `redis:6379` | **общий** кэш (cache-aside) |

Лог старта обеих реплик (виден `instance_id`):

```text
pz10_tasks_1 | {"instance_id":"tasks-1","msg":"server started","port":"8082","service":"tasks",...}
pz10_tasks_2 | {"instance_id":"tasks-2","msg":"server started","port":"8082","service":"tasks",...}
```

### Требование stateless

`tasks` не хранит данные в памяти процесса: источник истины — **общий PostgreSQL**,
кэш Redis — тоже общий. Поэтому какая бы реплика ни ответила, данные одинаковы и
«не прыгают». Это и есть условие корректного горизонтального масштабирования.

Подтверждение — вставили одну запись напрямую в общую БД и опросили LB несколько
раз: ответы tasks-1 и tasks-2 идентичны.

```text
req 1 -> inst=tasks-2     req 2 -> inst=tasks-1     req 3 -> inst=tasks-2 ...
$ curl -s http://localhost:8080/v1/tasks -H "Authorization: Bearer demo-token"
[{"id":"demo-1","title":"shared via postgres","done":false}]
```

---

## 2. Конфиг NGINX: upstream и server (ключевые фрагменты)

Полный файл — [deploy/lb/nginx.conf](deploy/lb/nginx.conf).

```nginx
# Пул реплик. Алгоритм по умолчанию — round-robin (запросы по очереди).
# max_fails/fail_timeout дают отказоустойчивость: после 1 неудачи реплика
# на 5с исключается из ротации, затем NGINX пробует её снова.
upstream tasks_upstream {
    server tasks_1:8082 max_fails=1 fail_timeout=5s;
    server tasks_2:8082 max_fails=1 fail_timeout=5s;
}

server {
    listen 8080;
    server_name localhost;

    location / {
        proxy_pass http://tasks_upstream;

        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID      $http_x_request_id;   # сквозная трассировка
        proxy_set_header Authorization     $http_authorization;  # пробрасываем токен

        # если реплика отдала сетевую ошибку/таймаут/5xx — прозрачно повторяем
        # запрос на следующей живой реплике.
        proxy_next_upstream error timeout http_502 http_503 http_504;
    }
}
```

`docker-compose.yml` стенда — [deploy/lb/docker-compose.yml](deploy/lb/docker-compose.yml):
`nginx` (порт `8080` наружу), `tasks_1`/`tasks_2` (без проброса), плюс общие
`db`, `auth`, `redis`. NGINX внутри сети ходит к репликам по DNS-именам
`tasks_1:8082` и `tasks_2:8082`.

---

## 3. Health endpoint

Добавлен `GET /health` (без авторизации), всегда отвечает `200`:

```go
// services/tasks/internal/http/handler.go
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
    httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

Проверка через балансировщик:

```text
$ curl -i http://localhost:8080/health
HTTP/1.1 200 OK
Content-Type: application/json
X-Instance-Id: tasks-2
X-Request-Id: req-14cf535e20f78c2a

{"status":"ok"}
```

Зачем нужен: для проб готовности/живости (readiness/liveness) и чтобы
балансировщик/оркестратор мог исключать неготовые реплики из ротации.

---

## 4. Идентификация инстанса (`X-Instance-ID`)

Чтобы видеть, какая реплика ответила, добавлено middleware, которое ставит на
**каждый** ответ заголовок `X-Instance-ID` со значением из `INSTANCE_ID`:

```go
// shared/middleware/instance_id.go
func InstanceID(id string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if id != "" {
                w.Header().Set(HeaderInstanceID, id) // "X-Instance-ID"
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

Оно подключено в цепочку в [services/tasks/cmd/tasks/main.go](services/tasks/cmd/tasks/main.go):
`RequestID → InstanceID → SecurityHeaders → Metrics → AccessLog → CSRF → mux`.
Заголовок ставится до обработчика, поэтому попадает и в `/health`, и в ошибки.

---

## 5. Демонстрация распределения запросов (round-robin)

10 запросов подряд через балансировщик:

```bash
for i in $(seq 1 10); do
  curl -s -i http://localhost:8080/v1/tasks \
    -H "Authorization: Bearer demo-token" | grep -i "X-Instance-ID"
done
```

```text
req  1 -> X-Instance-Id: tasks-1
req  2 -> X-Instance-Id: tasks-2
req  3 -> X-Instance-Id: tasks-1
req  4 -> X-Instance-Id: tasks-2
req  5 -> X-Instance-Id: tasks-1
req  6 -> X-Instance-Id: tasks-2
req  7 -> X-Instance-Id: tasks-1
req  8 -> X-Instance-Id: tasks-2
req  9 -> X-Instance-Id: tasks-1
req 10 -> X-Instance-Id: tasks-2
```

Видно чёткое чередование `tasks-1` / `tasks-2` — трафик реально распределяется.

---

## 6. Демонстрация отказоустойчивости (падение реплики)

Останавливаем одну реплику и повторяем запросы:

```bash
docker compose stop tasks_1
for i in $(seq 1 5); do
  curl -s -i http://localhost:8080/v1/tasks \
    -H "Authorization: Bearer demo-token" | grep -i "X-Instance-ID"
done
```

```text
req  1 -> X-Instance-Id: tasks-2
req  2 -> X-Instance-Id: tasks-2
req  3 -> X-Instance-Id: tasks-2
req  4 -> X-Instance-Id: tasks-2
req  5 -> X-Instance-Id: tasks-2

health HTTP 200   # /health продолжает отвечать
```

Сервис продолжает обслуживать запросы — теперь отвечает только `tasks-2`.
NGINX по `max_fails`/`proxy_next_upstream` исключил упавшую реплику. После
`docker compose start tasks_1` балансировка снова идёт по обеим репликам.

---

## 7. Запуск стенда

```bash
cd deploy/lb
docker compose up -d --build
docker compose ps
```

```text
NAME           SERVICE   STATUS
pz10_auth      auth      Up (healthy deps)
pz10_db        db        Up (healthy)
pz10_nginx     nginx     Up   0.0.0.0:8080->8080/tcp
pz10_redis     redis     Up (healthy)
pz10_tasks_1   tasks_1   Up
pz10_tasks_2   tasks_2   Up
```

Остановка: `docker compose down -v` (с удалением томов БД/кэша).

### Что менялось в коде относительно прошлых ПЗ

| Файл | Изменение |
|------|-----------|
| [shared/middleware/instance_id.go](shared/middleware/instance_id.go) | новое middleware: заголовок `X-Instance-ID` из `INSTANCE_ID` |
| [services/tasks/cmd/tasks/main.go](services/tasks/cmd/tasks/main.go) | чтение `INSTANCE_ID` (фолбэк — hostname), подключение middleware |
| [services/tasks/internal/http/handler.go](services/tasks/internal/http/handler.go) | endpoint `GET /health` без авторизации |
| [shared/middleware/metrics.go](shared/middleware/metrics.go) | классификатор маршрута для `/health` |
| [deploy/lb/](deploy/lb/) | стенд: `docker-compose.yml` + `nginx.conf` |

---

## 8. Контрольные вопросы

1. **Горизонтальное vs вертикальное масштабирование.** Вертикальное — «усилить»
   один узел (больше CPU/RAM): просто, но упирается в потолок железа и в единую
   точку отказа. Горизонтальное — добавлять одинаковые экземпляры сервиса и
   распределять между ними нагрузку; масштабируется почти линейно и переживает
   отказ отдельных узлов, но требует, чтобы сервис был stateless.

2. **Зачем нужен load balancer.** Он — единая точка входа, которая раздаёт
   запросы по живым репликам (round-robin, least-conn и т. п.), убирает упавшие
   из ротации (отказоустойчивость), скрывает за собой число и адреса инстансов,
   а заодно может терминировать TLS, ограничивать частоту и логировать трафик.

3. **Почему сервис должен быть stateless.** Балансировщик может направить
   следующий запрос на любую реплику. Если состояние (сессии, данные) лежит в
   памяти конкретного инстанса, ответы будут зависеть от того, кто ответил, а при
   его падении состояние потеряется. Состояние выносят во внешние общие хранилища
   (БД, Redis) — тогда реплики взаимозаменяемы.

4. **Алгоритмы балансировки.** Round-robin (по очереди, по умолчанию в NGINX),
   weighted round-robin (с весами), least connections (наименее загруженной),
   IP hash / consistent hashing (привязка клиента к реплике — sticky sessions),
   random, по задержке/ответу (least time). В этом ПЗ — round-robin.

5. **Зачем нужен `/health` endpoint.** Чтобы балансировщик и оркестратор могли
   автоматически проверять готовность реплики и направлять трафик только на
   здоровые инстансы (а неготовые — выводить из ротации и перезапускать). Это
   основа авто-восстановления и бесшовных деплоев.
```
