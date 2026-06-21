## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
## Практическое занятие №5 — HTTPS (TLS) и защита от SQL-инъекций
## Выбранный вариант

**Вариант 1 — TLS на NGINX (TLS-терминация на reverse proxy).**

Обоснование:
* ближе к индустриальной практике (сертификаты, ciphers и редиректы обычно настраивают на периметре, а не в коде сервиса);
* сервис `tasks` остаётся HTTP и не знает про TLS — это упрощает локальную отладку и тесты;
* управление сертификатами вынесено в отдельный слой `deploy/tls`.

## Архитектура стенда

```
Клиент ──HTTPS (8443)──▶ NGINX (TLS-терминатор) ──HTTP (8082)──▶ tasks ──SQL──▶ PostgreSQL
                                                                     │
                                                                     └──gRPC──▶ Auth (host:50051)
```

* NGINX слушает `:8443` по HTTPS и проксирует весь трафик в контейнер `tasks:8082`.
* `tasks` работает только по HTTP внутри compose-сети.
* База данных `PostgreSQL 16` поднимается тем же `docker compose`. Миграция `001_tasks.sql` применяется автоматически через `docker-entrypoint-initdb.d`.
* `Auth` запускается на хосте (как в ПЗ №2-№4) и достижим из контейнера через `host.docker.internal:50051`.

## 1. Команды генерации сертификата

```bash
mkdir -p deploy/tls
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout deploy/tls/key.pem \
  -out deploy/tls/cert.pem \
  -days 365 \
  -subj "/CN=localhost"
```

`key.pem` и `cert.pem` добавлены в `deploy/tls/.gitignore` — приватный ключ в публичный репозиторий не попадает.

## 2. Конфигурация NGINX — `deploy/tls/nginx.conf`

```nginx
events {}

http {
    server_tokens off;

    server {
        listen 8443 ssl;
        server_name localhost;

        ssl_certificate     /etc/nginx/tls/cert.pem;
        ssl_certificate_key /etc/nginx/tls/key.pem;

        ssl_protocols             TLSv1.2 TLSv1.3;
        ssl_prefer_server_ciphers on;

        location / {
            proxy_pass http://tasks:8082;
            proxy_set_header Host              $host;
            proxy_set_header X-Forwarded-Proto https;
            proxy_set_header X-Forwarded-For   $remote_addr;
            proxy_set_header X-Request-ID      $http_x_request_id;
            proxy_set_header Authorization     $http_authorization;
        }
    }
}
```

## 3. Описание БД и миграция

Файл `services/tasks/migrations/001_tasks.sql`:

```sql
CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT PRIMARY KEY,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    due_date    TEXT        NOT NULL DEFAULT '',
    done        BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS tasks_title_idx ON tasks (title);
```

В сервисе добавлен слой репозитория — `services/tasks/internal/repository/postgres.go`.
Интерфейс `service.TaskRepository` описан в `services/tasks/internal/service/tasks.go`; сервисный слой не формирует SQL — всё делает репозиторий.

## 4. Демонстрация SQL-инъекции (до / после)

### Как выглядел бы уязвимый запрос (НЕ ИСПОЛЬЗУЕТСЯ в коде, только для демонстрации)

```go
// ОПАСНО — склейка строк с пользовательским вводом:
sql := "SELECT id, title FROM tasks WHERE title = '" + title + "'"
rows, err := pool.Query(ctx, sql)
```

При `title=' OR '1'='1` запрос превратится в:

```sql
SELECT id, title FROM tasks WHERE title = '' OR '1'='1'
```

Логика фильтра ломается — вернутся все строки таблицы.

### Как это реализовано в коде (безопасно, параметризованно)

`services/tasks/internal/repository/postgres.go`:

```go
func (r *PostgresRepository) SearchByTitle(ctx context.Context, title string) ([]service.Task, error) {
    rows, err := r.pool.Query(ctx, `
        SELECT id, title, description, due_date, done
        FROM tasks
        WHERE title = $1
        ORDER BY created_at
    `, title)
    ...
}
```

Ключевой момент: `title` передаётся как параметр `$1`. Драйвер `pgx` отправляет запрос и параметры по отдельности (extended query protocol) — пользовательский ввод никогда не интерпретируется как SQL, а только как значение.

### Пример проверки одной командой `curl`

Создаём задачу с безобидным заголовком:

```bash
curl -k -i -X POST https://localhost:8443/v1/tasks \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer demo-token" \
  -d '{"title":"SQL safe","description":"use params","due_date":"2026-01-15"}'
```

Попытка "атаки" через параметризованный поиск — строка просто ищется как текст и вернёт пустой массив `[]`:

```bash
curl -k -i "https://localhost:8443/v1/tasks/search?title=%27%20OR%20%271%27%3D%271" \
  -H "Authorization: Bearer demo-token"
```

Обычный поиск вернёт ранее созданную задачу:

```bash
curl -k -i "https://localhost:8443/v1/tasks/search?title=SQL%20safe" \
  -H "Authorization: Bearer demo-token"
```

### Обработка ошибок БД

Ошибка от `pgx` логируется на сервере через `log.WithError(err).Error(...)`, но клиенту возвращается `500 Internal Server Error` с телом `{"error":"internal error"}` — внутренние детали БД (имена таблиц, текст запроса, состояние пула) наружу не уходят.

## 5. Инструкция запуска стенда

Требуется: Docker Desktop, Go 1.25+, `openssl`.

```bash
# 1. Сгенерировать сертификат (один раз)
mkdir -p deploy/tls
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout deploy/tls/key.pem -out deploy/tls/cert.pem \
  -days 365 -subj "/CN=localhost"

# 2. Запустить Auth сервис на хосте (gRPC на :50051)
AUTH_PORT=8081 AUTH_GRPC_PORT=50051 go run ./services/auth/cmd/auth

# 3. Поднять стенд с TLS: postgres + tasks + nginx
cd deploy/tls
docker compose up -d --build

# 4. Проверить HTTPS
curl -k -i https://localhost:8443/v1/tasks \
  -H "Authorization: Bearer demo-token" \
  -H "X-Request-ID: pz5-https-001"
```

Остановка:

```bash
cd deploy/tls
docker compose down -v
```

## 6. Контрольные вопросы

1. **Какие свойства даёт TLS соединению?** Конфиденциальность (шифрование трафика симметричным алгоритмом, согласованным в handshake), целостность (MAC/AEAD не даёт изменить содержимое незаметно) и аутентификацию сервера (клиент проверяет, что сертификат подписан доверенным CA и выдан для нужного имени).
2. **Почему самоподписанный сертификат не подходит для продакшна?** Его подписал сам владелец ключа, а не публичный удостоверяющий центр. Браузеры и HTTP-клиенты не имеют в trust-store такого корня, поэтому показывают предупреждение или отказываются соединяться. Без CA невозможно проверить подлинность сервера и защититься от MITM.
3. **В чём отличие TLS-терминации на NGINX от TLS в приложении?** При терминации на NGINX шифрование заканчивается на периметре, внутрь идёт HTTP. Это упрощает управление сертификатами, ротацию, ciphers, HTTP/2 и редиректы http→https. TLS в приложении даёт end-to-end шифрование до самого сервиса, но требует, чтобы каждый сервис умел читать сертификаты и поддерживать TLS-конфигурацию.
4. **Как возникает SQL-инъекция?** Когда пользовательский ввод склеивается со строкой запроса как текст SQL, а не передаётся как параметр. Специальные символы (кавычки, `;`, `--`, `OR '1'='1'`) ломают структуру запроса и позволяют изменить его смысл.
5. **Почему параметризованный запрос защищает от SQLi?** Сервер получает SQL-шаблон и параметры отдельными сообщениями. Параметры не парсятся как SQL — они всегда являются значениями конкретных типов. Никакая кавычка или `OR '1'='1'` внутри параметра не может изменить структуру запроса.
6. **Почему детали ошибок БД нельзя показывать клиенту?** Текст ошибки может раскрывать имена таблиц и колонок, часть запроса, версии БД, путь к файлу, — всё это помогает атакующему строить следующие шаги. Клиенту возвращаем обобщённый `500 internal error`, подробности пишем только в серверный лог с `request_id` для разбора.
