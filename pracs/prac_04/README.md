## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №4 — Prometheus + Grafana

**Объект:** сервис `tasks`

**Цель:** собрать и визуализировать метрики HTTP-сервиса (RPS, ошибки, latency, активные запросы).

---

## 1. Какие метрики добавлены

Метрики инициализируются в [shared/metrics/metrics.go](../shared/metrics/metrics.go) и снимаются middleware [shared/middleware/metrics.go](../shared/middleware/metrics.go).

| Имя | Тип | Labels | Назначение |
|---|---|---|---|
| `http_requests_total` | Counter | `service`, `method`, `route`, `status` | Сколько HTTP-запросов обработал сервис (разбивка по методу, route-шаблону и HTTP-коду). |
| `http_request_duration_seconds` | Histogram (buckets `0.01, 0.05, 0.1, 0.3, 1, 3`) | `service`, `method`, `route` | Длительность запросов — основа для p95/p99. |
| `http_in_flight_requests` | Gauge | `service` | Сколько запросов сейчас выполняется (обрабатывается параллельно). |

Плюс включены стандартные Go-коллекторы (`go_*`, `process_*`) через `prometheus.NewGoCollector` / `NewProcessCollector`.

### Нормализация route

Чтобы `/v1/tasks/t_001`, `/v1/tasks/t_002` и т.д. не порождали отдельные серии, путь в middleware нормализуется:

- `/v1/tasks` → `/v1/tasks`
- `/v1/tasks/<id>` → `/v1/tasks/:id`
- `/metrics` → `/metrics`

Это защищает Prometheus от роста кардинальности и позволяет корректно считать p95 по “логическому” ручке.

---

## 2. Пример вывода `/metrics`

Ниже — фрагмент после небольшой нагрузки (50 успешных `GET /v1/tasks`, 20 с неверным токеном, 1 `POST /v1/tasks`, 1 `GET /v1/tasks/:id`).

<img width="1280" height="856" alt="image" src="https://github.com/user-attachments/assets/41a42101-2f78-4632-ac20-9c16b4ec6e87" />


Route `/v1/tasks/:id` корректно «схлопывает» конкретные ID — значит, middleware-классификатор работает.

---

## 3. docker-compose и prometheus.yml

Инфраструктура лежит в [deploy/monitoring/](../deploy/monitoring/).

### [docker-compose.yml](../deploy/monitoring/docker-compose.yml)

Поднимает два контейнера:

- **prometheus** (`prom/prometheus:v2.54.1`) — порт `9090`, конфиг пробрасывается read-only. Добавлен `extra_hosts: host.docker.internal:host-gateway`, чтобы из контейнера можно было достучаться до сервиса, который запускается на хосте (`localhost:8086`).
- **grafana** (`grafana/grafana:11.2.0`) — порт `3000`, логин/пароль `admin/admin`, включён анонимный Viewer. Provisioning-папка монтируется read-only — дашборд и datasource подхватываются автоматически при старте.

Для обоих сервисов заданы volume’ы (`prometheus_data`, `grafana_data`), чтобы метрики и настройки переживали рестарт.

### [prometheus.yml](../deploy/monitoring/prometheus.yml)

```yaml
global:
  scrape_interval: 5s          # для учебной наблюдаемости удобно
  evaluation_interval: 15s

scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ["localhost:9090"]

  - job_name: tasks
    metrics_path: /metrics
    static_configs:
      - targets: ["host.docker.internal:8086"]
        labels:
          service: tasks
          env: dev
```

**Где target:** `host.docker.internal:8086` — это сервис `tasks`, запущенный на хосте (`go run ./services/tasks/cmd/tasks`). Если добавить `tasks` в docker-compose, target меняется на `tasks:8086`.

### Grafana provisioning

- [datasources/prometheus.yml](../deploy/monitoring/grafana/provisioning/datasources/prometheus.yml) — Prometheus добавляется как data source с фиксированным `uid: prometheus` (чтобы дашборд ссылался на него без ручной правки).
- [dashboards/dashboards.yml](../deploy/monitoring/grafana/provisioning/dashboards/dashboards.yml) — папка `Tasks`, автоподхват JSON из `/var/lib/grafana/dashboards`.
- [dashboards/tasks.json](../deploy/monitoring/grafana/dashboards/tasks.json) — сам дашборд «Tasks service — метрики», uid `tasks-pz4`.

---

## 4. Графики в Grafana

Дашборд содержит 4 панели (три обязательные + бонусный gauge):

<img width="2611" height="1220" alt="image" src="https://github.com/user-attachments/assets/23d1e9ae-98ee-49c5-9c86-7f085d0b9eb5" />


1. **RPS по маршрутам**
   — видно трафик отдельно на `/v1/tasks`, `/v1/tasks/:id`, `/metrics`. При прогоне нагрузочного цикла (50 запросов списком) линия `/v1/tasks` резко поднималась, остальные оставались плоскими.

2. **Ошибки (4xx/5xx)**
   — после прогонов с заголовком `Authorization: Bearer wrong` на графике отдельной линией появляется `401`.

3. **Latency p95 по маршрутам**
   — 95-й перцентиль длительности. На нашем учебном сервисе укладывается в первый bucket (<10 мс), поэтому линия стабильно около нуля — это ожидаемо, т.к. `tasks` работает с in-memory хранилищем.

4. **Активные запросы (in-flight)** — stat-панель по `http_in_flight_requests{service="tasks"}`. Во время прогона нагрузочного `for`-цикла gauge кратковременно поднимался до 1–2 и возвращался в 0.

---

## 5. Инструкция запуска всей связки

### Подготовка

```bash
git clone <repo>
cd prac_04
go mod download
```

### Терминал 1 — auth (нужен для авторизации tasks)

```bash
go run ./services/auth/cmd/auth
# HTTP :8085, gRPC :50051
```

### Терминал 2 — tasks (на этом сервисе висит /metrics)

```bash
go run ./services/tasks/cmd/tasks
# HTTP :8086, /metrics доступен на http://localhost:8086/metrics
```

### Терминал 3 — мониторинг

```bash
cd deploy/monitoring
docker compose up -d
```

### Проверка

- `curl -s http://localhost:8086/metrics | head -n 30` — видим `http_requests_total`, `http_request_duration_seconds_bucket`, `http_in_flight_requests`.
- `http://localhost:9090/targets` — target `tasks` в состоянии **UP**.
- `http://localhost:3000` — Grafana, данные уже подхвачены, дашборд **Tasks service — метрики** лежит в папке *Tasks*.

### Нагрузка для «живых» графиков

```bash
# 50 успешных запросов
for i in {1..50}; do
  curl -s http://localhost:8086/v1/tasks -H "Authorization: Bearer demo-token" > /dev/null
done

# 20 ошибок авторизации
for i in {1..20}; do
  curl -s http://localhost:8086/v1/tasks -H "Authorization: Bearer wrong" > /dev/null
done

# пара item-запросов (чтобы сработал route /v1/tasks/:id)
curl -s -X POST http://localhost:8086/v1/tasks \
  -H "Authorization: Bearer demo-token" \
  -H "Content-Type: application/json" \
  -d '{"title":"demo"}'
curl -s http://localhost:8086/v1/tasks/t_001 -H "Authorization: Bearer demo-token"
```

### Остановка

```bash
cd deploy/monitoring
docker compose down          # оставит данные в volume
docker compose down -v       # и удалит volume’ы
```

---

## Ответы на вопросы
1. Чем метрики отличаются от логов и зачем нужны оба подхода? Метрики — числовые данные для мониторинга трендов и alerting, логи — события и ошибки для дебага. Нужны оба.
2. Чем Counter отличается от Gauge? Counter только увеличивается, Gauge может расти и уменьшаться.
3. Почему latency нужно измерять histogram, а не просто средним значением? Среднее скрывает выбросы, histogram показывает распределение (p95/p99).
4. Что такое labels и почему опасна высокая кардинальность? Много уникальных значений labels (например, user_id) раздувает метрики и занимает много памяти.
5. Зачем нужны p95/p99 и почему среднее может “врать”? Они показывают реальную производительность для большинства запросов, среднее скрывает "медленные" запросы, но не врёт.
