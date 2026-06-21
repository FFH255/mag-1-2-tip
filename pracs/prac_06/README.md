## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №6 — Защита от CSRF/XSS. Работа с secure cookies

## Что сделано

- Сервис `auth` после успешного логина выдаёт **две cookie**: `session` и `csrf_token`.
- Реализовано CSRF middleware по схеме **Double Submit Cookie** — все `POST/PUT/PATCH/DELETE` на `tasks` проверяют `X-CSRF-Token` против cookie `csrf_token`.
- Сервис `tasks` поддерживает аутентификацию через cookie `session` (наряду с `Authorization: Bearer`, оставленным для обратной совместимости с ПЗ №1–№5).
- Поле `description` (и `title`) санитизируется на бэкенде: `<`, `>`, `"`, `'` заменяются на HTML-сущности.
- На все ответы обоих сервисов добавлены заголовки безопасности: `Content-Security-Policy`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`.
- NGINX обновлён: `/v1/auth/*` проксируется на auth (host-сервис), `/v1/*` — на tasks, проброшены заголовки `Cookie` и `X-CSRF-Token`.

## 1. Какие cookies используются и какие флаги установлены

| Cookie | Значение | Флаги |
|---|---|---|
| `session` | токен сессии (для демо совпадает с `demo-token`) | `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, `Max-Age=3600` |
| `csrf_token` | 32-байтный random hex | `Secure`, `SameSite=Lax`, `Path=/`, `Max-Age=3600` (без `HttpOnly` — читается из JS) |

Почему так:

- `HttpOnly` на `session` — JS не может украсть токен сессии при XSS.
- `Secure` — куки отправляются только по HTTPS, не протекают по открытому HTTP.
- `SameSite=Lax` — браузер не приложит куки к cross-site `POST/DELETE/PATCH`, что сильно режет поверхность CSRF ещё до проверки токена.
- `csrf_token` специально **без** `HttpOnly` — иначе фронтенд не смог бы его прочитать и поставить в заголовок `X-CSRF-Token`.

Код выдачи cookies — [services/auth/internal/http/handler.go:60-87](services/auth/internal/http/handler.go#L60-L87).

## 2. CSRF-подход: Double Submit Cookie

Идея и реализация:

1. При логине `auth` кладёт `csrf_token=<random>` в cookie (она доступна JS) и `session=<token>` в HttpOnly cookie.
2. Клиент на каждый state-changing запрос читает значение `csrf_token` из cookie и кладёт его в заголовок `X-CSRF-Token`.
3. Middleware `CSRF` на сервисе `tasks` сравнивает cookie и заголовок константным временем; несовпадение или отсутствие → `403 Forbidden`.
4. `GET/HEAD/OPTIONS` проходят без проверки — они не меняют состояние.

Почему это защищает: вредоносный сайт (`evil.com`) может спровоцировать браузер отправить куки на наш домен, но **не может прочитать cookie другого домена** (same-origin policy) и, значит, не может подставить корректный `X-CSRF-Token`. Запрос отвергается 403.

Код middleware — [shared/middleware/csrf.go](shared/middleware/csrf.go).

## 3. Какие запросы защищены

Проверяются все state-changing методы на tasks:

- `POST /v1/tasks`
- `PATCH /v1/tasks/{id}`
- `DELETE /v1/tasks/{id}`

`GET /v1/tasks`, `GET /v1/tasks/{id}`, `GET /v1/tasks/search` — без CSRF, так как не меняют состояние.

## 4. XSS: что сделано

- Поля `title` и `description` проходят через `sanitizeText` — `strings.Replacer`, который превращает `< > " '` в `&lt; &gt; &quot; &#39;`. Даже если фронтенд забудет экранировать вывод, `<script>alert(1)</script>` в `description` сохранится как `&lt;script&gt;alert(1)&lt;/script&gt;` и будет показан текстом, а не исполнен как код. См. [services/tasks/internal/service/tasks.go:118-132](services/tasks/internal/service/tasks.go#L118-L132).
- Middleware `SecurityHeaders` ставит заголовки на все ответы: `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`. См. [shared/middleware/security_headers.go](shared/middleware/security_headers.go).

## 5. Инструкция запуска

Требуется: Docker Desktop, Go 1.25+, `openssl`, `curl`.

```bash
# 1. Сгенерировать сертификат (если ещё нет)
mkdir -p deploy/tls
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout deploy/tls/key.pem -out deploy/tls/cert.pem \
  -days 365 -subj "/CN=localhost"

# 2. Запустить Auth на хосте (HTTP :8085 + gRPC :50051)
AUTH_PORT=8085 AUTH_GRPC_PORT=50051 go run ./services/auth/cmd/auth

# 3. Поднять postgres + tasks + nginx
cd deploy/tls
docker compose up -d --build
```

NGINX слушает `https://localhost:8443`:
- `/v1/auth/*` → auth на хосте через `host.docker.internal:8085`;
- `/v1/tasks*` → tasks в контейнере.

## 6. Примеры запросов (curl)

### 6.1. Логин и получение cookies

```bash
curl -k -i -X POST https://localhost:8443/v1/auth/login \
  -H "Content-Type: application/json" \
  -c cookies.txt \
  -d '{"username":"student","password":"student"}'
```

Ожидаемо в ответе два `Set-Cookie` заголовка:

```
Set-Cookie: session=demo-token; Path=/; Expires=...; Max-Age=3600; HttpOnly; Secure; SameSite=Lax
Set-Cookie: csrf_token=<hex>; Path=/; Expires=...; Max-Age=3600; Secure; SameSite=Lax
```

Значение `csrf_token` лежит в `cookies.txt` — достать его можно так:

```bash
CSRF=$(awk '$6=="csrf_token" {print $7}' cookies.txt)
echo "$CSRF"
```

### 6.2. POST без CSRF → 403

```bash
curl -k -i -X POST https://localhost:8443/v1/tasks \
  -H "Content-Type: application/json" \
  -b cookies.txt \
  -d '{"title":"CSRF test","description":"no header"}'
```

Ожидаемо: `HTTP/1.1 403 Forbidden`, тело `{"error":"csrf token missing"}`.

### 6.3. POST с корректным X-CSRF-Token → 201

```bash
curl -k -i -X POST https://localhost:8443/v1/tasks \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"title":"CSRF ok","description":"with token"}'
```

Ожидаемо: `HTTP/1.1 201 Created` и JSON созданной задачи.

### 6.4. Демонстрация санитизации description (XSS)

```bash
curl -k -i -X POST https://localhost:8443/v1/tasks \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"title":"XSS","description":"<script>alert(1)</script>"}'
```

Ожидаемо в ответе и в БД: `description` сохранён как `&lt;script&gt;alert(1)&lt;/script&gt;`. Тег `<script>` не может быть выполнен как код при последующем отображении.

### 6.5. Проверка заголовков безопасности

```bash
curl -k -s -D - -o /dev/null https://localhost:8443/v1/tasks -b cookies.txt | grep -iE '^(content-security|x-content|x-frame|referrer)'
```

Ожидаемо:

```
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: no-referrer
```

## 7. Unit-тесты

Написаны тесты на middleware и санитайзер:

```bash
go test ./shared/middleware/... ./services/tasks/internal/service/...
```

Проверяют: GET проходит без CSRF, POST без cookie/без header/с mismatch → 403, POST с совпадающими токенами → 200, заголовки безопасности выставляются, `sanitizeText` корректно экранирует HTML-символы.

## 8. Контрольные вопросы

1. **Почему CSRF возможен при использовании cookies?** Браузер автоматически прикрепляет куки к любому запросу на домен-владелец cookie, даже если запрос инициировала сторонняя вредоносная страница (тег `<img>`, скрытая форма, `fetch` c `credentials: 'include'`). Сервер видит куку сессии и считает запрос легитимным, хотя пользователь его не инициировал. Уязвимость — в разрыве между «кто отправил запрос» и «чьи куки приложены».

2. **Что делает флаг SameSite и какие есть режимы?** `SameSite` управляет тем, прикладываются ли куки к cross-site запросам. Режимы:
   - `Strict` — куки идут только на запросы с того же сайта; максимально жёстко, но ломает типовые UX-сценарии (переход по ссылке из письма не увидит юзера залогиненным).
   - `Lax` (дефолт в современных браузерах) — куки идут при навигации top-level (клик по ссылке), но **не** на cross-site `POST/PUT/DELETE` или подзапросы (iframe, img, fetch). Разумный минимум.
   - `None` — куки идут всегда, включая cross-site; **обязателен** флаг `Secure`. Нужен, когда реально требуется third-party поведение (SSO, виджеты).

3. **Чем HttpOnly защищает от XSS и почему не «лечит» его полностью?** `HttpOnly` запрещает JS читать cookie через `document.cookie`, поэтому даже успешная XSS-инъекция не может просто украсть сессионный токен и отправить атакующему. Но XSS не «лечится»: выполненный в контексте страницы JS может вызывать API от имени пользователя (`fetch` с `credentials: 'include'` всё равно приложит HttpOnly куку автоматически), читать DOM, делать keylogger, подменять контент. `HttpOnly` лишь закрывает один конкретный вектор — exfiltration токена, а не саму возможность инъекции.

4. **Почему Secure обязателен, если cookie несёт сессию?** Без `Secure` браузер отправит куку и по обычному `http://` — любое пассивное прослушивание (открытый Wi-Fi, MITM) увидит токен в явном виде и сможет переиспользовать. `Secure` гарантирует, что кука уйдёт только по TLS, где трафик зашифрован и защищён от MITM. Фактически это обязательное условие, чтобы TLS защищал сессию, а не только тело запроса.

5. **Как работает double-submit CSRF защита?** Сервер при логине кладёт случайный CSRF-токен одновременно в cookie (не-HttpOnly) и ожидает его же в заголовке `X-CSRF-Token` на каждом state-changing запросе. Middleware сравнивает cookie и заголовок: равны — пропускает, иначе 403. Работает потому, что атакующий с чужого домена **не может прочитать cookie нашего домена** (same-origin policy), значит не может сформировать корректный заголовок, даже если браузер сам приложит куку. Плюсы: не нужен серверный state (stateless). Минусы: защита ломается, если есть отдельная XSS (атакующий JS сможет прочитать куку и сам поставить заголовок), а также требует аккуратного SameSite.

6. **Что такое XSS и какие базовые меры защиты применимы на backend?** XSS (Cross-Site Scripting) — класс атак, при котором контролируемый атакующим ввод попадает в HTML-страницу и исполняется браузером жертвы как JavaScript. Базовые меры со стороны backend:
   - **Экранирование/санитизация** пользовательского ввода при сохранении или выдаче: `<`, `>`, `"`, `'` → HTML-сущности. Для полей, где формально HTML допустим, — whitelisting (например, bluemonday), а не чёрный список.
   - **Content-Security-Policy** — ограничивает, откуда можно грузить скрипты/стили/фреймы; для JSON API простейшее `default-src 'none'`.
   - **X-Content-Type-Options: nosniff** — запрещает браузеру угадывать MIME и исполнять JSON/текст как скрипт.
   - **HttpOnly на session cookie** — чтобы XSS, если всё же случится, не смогла утянуть токен.
   - Правильные `Content-Type` на ответах (`application/json; charset=utf-8`) и отказ от рендеринга пользовательского HTML в шаблонах без экранирования.
