## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №9

**Тема:** распределённое кэширование на Redis. Стратегия **cache-aside** с TTL,
jitter, инвалидацией и деградацией при недоступности кэша.
**Объект:** сервис `tasks` (источник истины — PostgreSQL; кэшируем чтение задачи по id).
**Уровень стенда:** A — один узел Redis (standalone) в docker-compose.

---

## 1. Ключи кэша и как они формируются

Кэшируется обязательный минимум — **`GET /v1/tasks/{id}`** (чтение одной задачи).

| Что кэшируем | Ключ | Формирование |
|--------------|------|--------------|
| Задача по id | `tasks:task:<id>` | [`taskKey()`](../services/tasks/internal/cache/cache.go#L38) — префикс `tasks:task:` + id |

Значение — задача, сериализованная в **JSON**. Префикс `tasks:` отделяет
пространство имён сервиса от чужих ключей в общем Redis.

> Список (`GET /v1/tasks`, ключ `tasks:list`) — это опциональная часть задания,
> она **не реализовывалась**. Поэтому и инвалидация списка не нужна.

---

## 2. Реализация cache-aside

Логика живёт в сервисном слое — [`TaskService.Get`](../services/tasks/internal/service/tasks.go).
Кэш подключён через интерфейс [`TaskCache`](../services/tasks/internal/service/tasks.go),
поэтому сервис не знает, Redis за ним или заглушка.

Алгоритм `Get(ctx, id)`:

1. `cache.GetTask(id)` — попытка прочитать из Redis;
   - **hit** → распарсить JSON и сразу вернуть (в БД не идём);
   - **miss** → шаг 2.
2. `repo.Get(id)` — читаем из БД (источник истины). Если `ErrNotFound` —
   возвращаем 404, в кэш ничего не пишем.
3. `cache.SetTask(task)` — кладём найденную задачу в Redis с TTL.
4. Возвращаем задачу клиенту.

```
GET /v1/tasks/{id}
      │
      ▼
  ┌────────────┐  hit   ┌──────────────┐
  │ Redis GET  │──────► │ return task  │
  │ tasks:task │        └──────────────┘
  └─────┬──────┘
        │ miss / ошибка Redis
        ▼
  ┌────────────┐        ┌──────────────┐        ┌──────────────┐
  │  repo.Get  │──────► │ Redis SET TTL│──────► │ return task  │
  │   (БД)     │        │  (best-effort)│       └──────────────┘
  └────────────┘        └──────────────┘
```

Принцип: **Redis — ускоритель, БД — источник истины**. Реализация в
[`cache.RedisCache.GetTask`](../services/tasks/internal/cache/cache.go#L107) /
[`SetTask`](../services/tasks/internal/cache/cache.go#L136).

**Сериализация.** Задача кодируется `json.Marshal` при записи и `json.Unmarshal`
при чтении. Если значение в кэше не парсится — это логируется как WARN и
трактуется как **miss** (идём в БД), а не как ошибка ответа.

---

## 3. TTL и jitter

Настраиваются переменными окружения, значения по умолчанию:

| Переменная | Значение | Смысл |
|------------|----------|-------|
| `CACHE_TTL_SECONDS` | `120` | базовый TTL записи (в диапазоне 60–300 c для entity) |
| `CACHE_TTL_JITTER_SECONDS` | `30` | максимальный случайный разброс |

Итоговый TTL = `120 + rand(0..30)` секунд —
[`ttlWithJitter()`](../services/tasks/internal/cache/cache.go#L165).

**Зачем TTL:** кэш не хранит данные вечно и сам «протухает», поэтому даже без
явной инвалидации данные со временем перечитываются из БД.

**Зачем jitter:** если всем записям дать одинаковый TTL, они могут истечь
одновременно → одномоментный всплеск запросов в БД (**cache avalanche**).
Случайный разброс «размазывает» момент истечения.

---

## 4. Инвалидация при изменениях

Политика «изменил — сбросил» в [`TaskService`](../services/tasks/internal/service/tasks.go):

| Операция | Действие с кэшем |
|----------|------------------|
| `POST /v1/tasks` (Create) | ничего (новый id ещё не кэширован) |
| `PATCH /v1/tasks/{id}` (Update) | после успешной записи в БД → `cache.DelTask(id)` |
| `DELETE /v1/tasks/{id}` (Delete) | после успешного удаления в БД → `cache.DelTask(id)` |

Ключ удаляется **только после** успешной операции в БД. Следующий `GET`
получит miss и заново прогреет кэш актуальными данными. Ошибка удаления из Redis
логируется, но запрос не валит — см. [`DelTask`](../services/tasks/internal/cache/cache.go#L153).

---

## 5. Деградация при недоступности Redis (обязательная часть)

Кэш — необязательная зависимость, поэтому **все** операции кэша спроектированы
как best-effort и встроены в сам слой кэша:

- методы интерфейса `TaskCache` **не возвращают ошибок**;
- любая проблема Redis (узел недоступен, таймаут, битый JSON) → лог уровня
  **WARN** + поведение «как miss / no-op»;
- сервис продолжает работать через БД.

Технические гарантии «не зависнуть»:

- на каждую операцию — таймаут `cacheOpTimeout = 300ms`
  ([cache.go](../services/tasks/internal/cache/cache.go#L35));
- у клиента заданы `DialTimeout/ReadTimeout/WriteTimeout` и `MaxRetries: -1`
  (без ретраев — быстрее уходим в БД);
- на старте — best-effort `Ping`: при ошибке только WARN, сервис **не падает**
  ([main.go `buildCache`](../services/tasks/cmd/tasks/main.go)), стартует в
  DB-only режиме и подхватит кэш, когда Redis оживёт.

Если `REDIS_ADDR` не задан вовсе — подключается
[`NopCache`](../services/tasks/internal/cache/cache.go#L188) (всегда miss),
и сервис работает строго по БД без единой проверки «включён ли кэш».

Поведение деградации зафиксировано юнит-тестом
[`TestGet_DegradesWhenCacheUnavailable`](../services/tasks/internal/service/cache_aside_test.go):
при «всегда промахивающемся» кэше каждый `GET` доходит до БД и возвращает данные
без ошибки.

---

## 6. Запуск и проверка

### 6.1. Поднять Redis-стенд (Уровень A)

```bash
cd deploy/redis
docker compose up -d
docker compose ps          # redis должен быть healthy
```

Стенд: [`deploy/redis/docker-compose.yml`](../deploy/redis/docker-compose.yml) —
один узел `redis:7-alpine` на `localhost:6379`.

> Для полной связки (db + auth + tasks + redis) Redis также добавлен в
> [`deploy/docker-compose.yml`](../deploy/docker-compose.yml): там `tasks` уже
> получает `REDIS_ADDR=redis:6379`.

### 6.2. Запустить сервис tasks с кэшем

Локальный запуск (Redis из стенда + БД/auth из основного compose):

```bash
REDIS_ADDR=localhost:6379 \
CACHE_TTL_SECONDS=120 \
CACHE_TTL_JITTER_SECONDS=30 \
TASKS_PORT=8082 \
go run ./services/tasks/cmd/tasks
```

Переменные окружения сервиса:

| Переменная | По умолчанию | Назначение |
|------------|--------------|------------|
| `REDIS_ADDR` | — (пусто = кэш выключен) | адрес узла; список через запятую → кластер |
| `REDIS_PASSWORD` | пусто | пароль, если задан |
| `REDIS_DB` | `0` | номер БД (standalone) |
| `CACHE_TTL_SECONDS` | `120` | базовый TTL |
| `CACHE_TTL_JITTER_SECONDS` | `30` | разброс TTL |

### 6.3. Создать задачу

```bash
curl -i -X POST http://localhost:8082/v1/tasks \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer demo-token" \
  -d '{"title":"Cache","description":"Redis","due_date":"2026-01-20"}'
# → скопировать id из ответа
```

### 6.4. Два чтения подряд (miss → hit)

```bash
curl -i http://localhost:8082/v1/tasks/<id> -H "Authorization: Bearer demo-token"
curl -i http://localhost:8082/v1/tasks/<id> -H "Authorization: Bearer demo-token"
```

В логах сервиса (JSON) видно:

```json
{"level":"debug","msg":"cache miss","task_id":"t_...","component":"cache"}
{"level":"debug","msg":"cache hit","task_id":"t_...","component":"cache"}
```

Проверка непосредственно в Redis:

```bash
docker exec -it pz9_redis redis-cli GET tasks:task:<id>
docker exec -it pz9_redis redis-cli TTL tasks:task:<id>   # ≈ 120..150
```

### 6.5. Симуляция недоступности Redis (деградация)

```bash
cd deploy/redis && docker compose stop
curl -i http://localhost:8082/v1/tasks/<id> -H "Authorization: Bearer demo-token"
```

Ожидаемо:

- сервис **не падает**, отдаёт `200` с данными из БД;
- в логах WARN: `cache read failed, falling back to DB`.

После `docker compose start` кэш снова начинает работать без перезапуска сервиса.

### 6.6. Проверка инвалидации

```bash
# прогрели кэш
curl -s http://localhost:8082/v1/tasks/<id> -H "Authorization: Bearer demo-token" >/dev/null
# изменили — кэш должен сброситься
curl -i -X PATCH http://localhost:8082/v1/tasks/<id> \
  -H "Content-Type: application/json" -H "Authorization: Bearer demo-token" \
  -d '{"done":true}'
docker exec -it pz9_redis redis-cli GET tasks:task:<id>   # → (nil)
```

### 6.7. Автотесты (без Docker)

```bash
go test ./services/tasks/internal/service/
```

Покрывают cache-aside, инвалидацию и деградацию:
[`cache_aside_test.go`](../services/tasks/internal/service/cache_aside_test.go) —
`TestGet_CacheAside`, `TestUpdateDelete_InvalidateCache`,
`TestGet_DegradesWhenCacheUnavailable`.

---

## 7. Контрольные вопросы

1. **Что такое cache-aside и почему он часто используется?**
   Приложение само управляет кэшем «сбоку»: на чтении сначала смотрит в кэш, при
   промахе идёт в БД и кладёт результат в кэш. Популярен потому, что прост,
   кэшируются только реально запрашиваемые данные, а кэш не обязателен — при его
   сбое система продолжает работать через БД.

2. **Зачем нужен TTL?**
   Чтобы кэш не хранил данные вечно: запись сама «протухает» и перечитывается из
   БД. Это ограничивает рассинхрон кэша и БД и не даёт кэшу бесконтрольно расти.

3. **Что такое cache avalanche и как помогает jitter?**
   Лавина — когда у множества ключей TTL истекает почти одновременно, и поток
   промахов разом бьёт в БД (вплоть до отказа). Jitter добавляет к TTL случайный
   разброс, «размазывая» моменты истечения во времени и сглаживая нагрузку.

4. **Почему Redis не должен быть «источником истины»?**
   Кэш эфемерен и не обязан быть консистентным: данные истекают по TTL, узел
   может перезапуститься/потерять данные, кэш может быть недоступен. Истина — в
   надёжном хранилище (БД); кэш лишь ускоряет чтение её копии.

5. **Как правильно вести себя сервису при падении Redis?**
   Деградировать, а не падать: ошибки кэша логировать (WARN/ERROR), трактовать
   как промах и обслуживать запрос из БД. Операции кэша должны иметь таймауты,
   чтобы недоступный Redis не «подвешивал» запросы.

---

## 8. Где что лежит

| Файл | Назначение |
|------|------------|
| [`services/tasks/internal/cache/cache.go`](../services/tasks/internal/cache/cache.go) | `RedisCache` (cache-aside, TTL+jitter, деградация) и `NopCache` |
| [`services/tasks/internal/service/tasks.go`](../services/tasks/internal/service/tasks.go) | интерфейс `TaskCache`, cache-aside в `Get`, инвалидация в `Update`/`Delete` |
| [`services/tasks/cmd/tasks/main.go`](../services/tasks/cmd/tasks/main.go) | `buildCache` — конфиг из env, выбор Redis/Nop, ping на старте |
| [`deploy/redis/docker-compose.yml`](../deploy/redis/docker-compose.yml) | Redis-стенд, Уровень A (один узел) |
| [`deploy/docker-compose.yml`](../deploy/docker-compose.yml) | интегрированная связка с Redis |
| [`services/tasks/internal/service/cache_aside_test.go`](../services/tasks/internal/service/cache_aside_test.go) | тесты cache-aside / инвалидации / деградации |
