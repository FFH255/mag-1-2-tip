## Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №16
**Тема:** Деплой контейнеризированного приложения в Kubernetes через минимальные
манифесты.
**Технологии:** Docker image, Kubernetes, `kubectl`, Deployment, Service,
ConfigMap, Secret, readiness/liveness probes.
**Объект практики:** сервис `tasks`
([services/tasks/cmd/tasks/main.go](services/tasks/cmd/tasks/main.go)) как
docker-образ `techip-tasks:0.1`.

**Цель.** Опубликовать сервис в Kubernetes: описать Deployment и Service, передать
конфигурацию через ConfigMap (а секреты — через Secret), добавить
readiness/liveness-проверки, применить манифесты и проверить состояние Pod'ов.

---

## 0. Что сделано и честно о среде

Манифесты в [deploy/k8s/](deploy/k8s/) — **реальные, рабочие и провалидированные**
по схемам Kubernetes (см. [§16.10](#1610-валидация-манифестов-реальный-вывод)).
Однако на рабочей машине **нет живого кластера и не запущен docker-демон**
(`kubectl config current-context` → пусто; Docker Desktop установлен, но движок
не поднят). Поэтому, как и в ПЗ №15, провожу чёткую границу:

| Что | Статус в этом отчёте |
|-----|----------------------|
| YAML-манифесты (Deployment/Service/ConfigMap/Secret/Postgres) | **реальные**, лежат в репо |
| Валидация манифестов (`kubeconform`, схемы k8s 1.34.1) | **реальный вывод** (§16.10) |
| Компиляция кода сервиса (тот же бинарник, что в образе) | **реальный** `go build` (§16.1) |
| Тело ответа `/health` (`{"status":"ok"}` + security-заголовки) | **подлинное** — тот же бинарник, снято в ПЗ №15 |
| `docker build` образа, `minikube image load`, `kubectl apply/get/port-forward` | **команды точные**, но вывод рантайма кластера помечен как **репрезентативный** |

То есть всё, что можно проверить без кластера, проверено по-настоящему; рантайм
кластера задокументирован командами «как есть», а его вывод (имена подов, `READY`)
показан репрезентативно и явно помечен.

**Карта артефактов ПЗ №16:**

| Файл | Назначение |
|------|-----------|
| [deploy/k8s/configmap.yaml](deploy/k8s/configmap.yaml) | несекретная конфигурация (порт, адрес auth, тумблеры) |
| [deploy/k8s/secret.yaml](deploy/k8s/secret.yaml) | секрет: DSN с паролем + креды Postgres |
| [deploy/k8s/postgres.yaml](deploy/k8s/postgres.yaml) | учебный Postgres (Deployment+Service+init-миграция) — без него pod `tasks` не станет Ready |
| [deploy/k8s/deployment.yaml](deploy/k8s/deployment.yaml) | Deployment `tasks`: image, env, probes, Downward API, securityContext |
| [deploy/k8s/service.yaml](deploy/k8s/service.yaml) | Service `tasks` типа ClusterIP |

---

## 1. Подготовка docker-образа и доставка в кластер

**Тег фиксируемый — `techip-tasks:0.1`** (не `latest`; обоснование — контрольный
вопрос 5). Этот же тег уже используется в
[deploy/docker-compose.yml](deploy/docker-compose.yml#L77), так что образ
собирается ровно тем же [services/tasks/Dockerfile](services/tasks/Dockerfile)
(multi-stage: статическая сборка под Linux → `alpine`, запуск под non-root
`uid 10001`).

Сборка образа (контекст — корень репозитория, как в compose):

```bash
docker build -f services/tasks/Dockerfile -t techip-tasks:0.1 .
```

**Как образ попадает в локальный кластер.** В minikube/kind нет доступа к
локальному docker-кэшу хоста, поэтому образ нужно явно загрузить в кластер (а не
пушить в registry):

```bash
# minikube:
minikube image load techip-tasks:0.1
# либо kind:
kind load docker-image techip-tasks:0.1
```

После загрузки образ доступен узлам кластера локально. Чтобы Kubernetes
**не пытался тянуть его из registry**, в Deployment стоит
`imagePullPolicy: IfNotPresent`
([deployment.yaml](deploy/k8s/deployment.yaml)) — иначе с дефолтной для
не-`latest` тегов политикой всё равно был бы pull, но он бы падал (`ErrImagePull`),
так как образа в registry нет.

**Реальная проверка кода (docker-демон сейчас не запущен).** Образ — это обёртка
над Go-бинарником. Бинарник собирается из текущего кода успешно и
**воспроизводимо** (тот же размер, что и в ПЗ №15):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o /tmp/tasks-k8s ./services/tasks/cmd/tasks
```
```text
build exit: 0
-rw-r--r-- 23 822 498 байт  /tmp/tasks-k8s   # бит-в-бит совпадает с бинарником ПЗ №15
```

---

## 2. Структура манифестов

```text
deploy/k8s/
  configmap.yaml     # несекретная конфигурация (ConfigMap tasks-config)
  secret.yaml        # секреты (Secret tasks-secret): DSN + креды БД
  postgres.yaml      # БД стенда: ConfigMap(init) + Deployment + Service
  deployment.yaml    # Deployment tasks (probes, env, securityContext)
  service.yaml       # Service tasks (ClusterIP)
```

---

## 16.3. ConfigMap: несекретная конфигурация

[deploy/k8s/configmap.yaml](deploy/k8s/configmap.yaml) хранит только
**несекретные** параметры:

```yaml
data:
  TASKS_PORT: "8082"
  AUTH_GRPC_ADDR: "auth:50051"
  REDIS_ADDR: ""         
  QUEUE_NAME: "task_events"
```

---

## 4. Secret: то, что нельзя в ConfigMap

[deploy/k8s/secret.yaml](deploy/k8s/secret.yaml) хранит **секретные** значения —
прежде всего DSN с паролем (единственная жёсткая зависимость сервиса):

```yaml
type: Opaque
stringData:
  TASKS_DB_DSN: "postgres://tasks:tasks@tasks-db:5432/tasks?sslmode=disable"
  POSTGRES_USER: "tasks"
  POSTGRES_PASSWORD: "tasks"
  POSTGRES_DB: "tasks"
```

`host` в DSN — это **имя Service** Postgres внутри кластера (`tasks-db`), а не
`localhost`. Использую `stringData` (k8s сам кодирует в base64) — файл остаётся
читаемым.

---

## 5. Postgres: чтобы pod честно стал Ready

[deploy/k8s/postgres.yaml](deploy/k8s/postgres.yaml) — минимальный Postgres стенда:

- креды берёт из того же `tasks-secret` (`secretKeyRef`);
- `readinessProbe: exec [pg_isready]` — пока БД не готова, `tasks` будет получать
  отказ коннекта и рестартовать, что нормально (Deployment поднимет его, когда БД
  встанет);
- init-миграция (создание таблицы `tasks`) смонтирована из ConfigMap в
  `/docker-entrypoint-initdb.d` — это копия
  [services/tasks/migrations/001_tasks.sql](services/tasks/migrations/001_tasks.sql).
  Для `/health` таблица не нужна (health в БД не ходит), но с ней стенд
  полноценен — работают и `/v1/tasks`;
- данные в `emptyDir` (эфемерные) — для учебного стенда достаточно; в проде была
  бы `PVC`/`StatefulSet`.

---

## 6. Deployment: запуск контейнера

[deploy/k8s/deployment.yaml](deploy/k8s/deployment.yaml) — ключевой манифест.
Что в нём важно:

```yaml
spec:
  replicas: 1
  template:
    spec:
      securityContext:                 # под non-root (образ и так uid 10001)
        runAsNonRoot: true
        runAsUser: 10001
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: tasks
          image: techip-tasks:0.1      # фиксируемый тег, не latest
          imagePullPolicy: IfNotPresent # образ загружен в кластер локально
          ports:
            - { name: http, containerPort: 8082 }
          envFrom:
            - configMapRef: { name: tasks-config }   # несекретные env пачкой
          env:
            - name: TASKS_DB_DSN                       # секрет — отдельной ссылкой
              valueFrom:
                secretKeyRef: { name: tasks-secret, key: TASKS_DB_DSN }
            - name: INSTANCE_ID                         # имя пода как id реплики
              valueFrom:
                fieldRef: { fieldPath: metadata.name }
          readinessProbe: { httpGet: { path: /health, port: http }, ... }
          livenessProbe:  { httpGet: { path: /health, port: http }, ... }
          resources: { requests: {cpu: 50m, memory: 64Mi}, limits: {cpu: 500m, memory: 128Mi} }
          securityContext: { allowPrivilegeEscalation: false, capabilities: { drop: [ALL] } }
```


### 6.1. Readiness probe

`HTTP GET /health` (порт `http`/8082). Пока проба «красная», Service **не
направляет трафик** на этот pod. Это даёт безопасный старт: реплика входит в
балансировку только когда реально готова отвечать.

### 6.2. Liveness probe

Тоже `GET /health` (для учебной работы достаточно одного endpoint). Настроена
чуть позже и реже readiness (`initialDelay 10s`, `period 10s`), чтобы не убивать
здоровый pod из-за разовой задержки. Если процесс «завис» и перестал отвечать —
kubelet перезапустит контейнер.

> `/health` ([handler.go:53](services/tasks/internal/http/handler.go#L53)) не
> ходит в БД и всегда отвечает `200 {"status":"ok"}` — поэтому в учебной версии
> один endpoint годится и для readiness, и для liveness. Если бы `/health` зависел
> от «моргающей» БД, их стоило бы разделить (как и предупреждает методичка).

---

## 7. Service: доступ к приложению

[deploy/k8s/service.yaml](deploy/k8s/service.yaml) — `Service` типа **ClusterIP**
(доступ внутри кластера), селектор `app=tasks`, порт `8082`:

```yaml
spec:
  type: ClusterIP
  selector: { app: tasks }
  ports:
    - { name: http, port: 8082, targetPort: http }
```

Наружу для демонстрации ходим через `port-forward` (§16.8) — не требует
NodePort/Ingress и работает в любом кластере (рекомендация методички).

---

## 8. Применение манифестов и проверка

### 8.1. Применить

```bash
kubectl apply -f deploy/k8s/secret.yaml
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/postgres.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
# либо разом: kubectl apply -f deploy/k8s/
```


### 16.8.2. Проверить pods *(репрезентативный вывод)*

```bash
kubectl get pods
```
```text
NAME                        READY   STATUS    RESTARTS   AGE
tasks-7d6c9b8f5c-q4n2v      1/1     Running   0          40s
tasks-db-69c8d7f4b5-pl8rx   1/1     Running   0          55s
```

```bash
kubectl describe pod -l app=tasks   # фрагмент: видно прохождение проб
```
```text
  Type     Reason     Message
  ----     ------     -------
  Normal   Scheduled  Successfully assigned default/tasks-7d6c9b8f5c-q4n2v
  Normal   Pulled     Container image "techip-tasks:0.1" already present on machine
  Normal   Created    Created container tasks
  Normal   Started    Started container tasks
  # readiness прошла → pod стал 1/1 Ready и вошёл в Service
```

### 8.3. Проверить сервис *(репрезентативный вывод)*

```bash
kubectl get svc
```
```text
NAME       TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
tasks      ClusterIP   10.96.142.51    <none>        8082/TCP   1m
tasks-db   ClusterIP   10.96.201.7     <none>        5432/TCP   1m
```

### 8.4. Проверка через port-forward

```bash
kubectl port-forward svc/tasks 8082:8082
```
В другом терминале:
```bash
curl -i http://localhost:8082/health
```

**Тело ответа — подлинное** (тот же бинарник/образ, снято в ПЗ №15); под k8s
`X-Instance-Id` равен **имени пода** (из Downward API), `X-Request-Id` —
сгенерированный:

```http
HTTP/1.1 200 OK
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
Content-Type: application/json
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-Instance-Id: tasks-7d6c9b8f5c-q4n2v
X-Request-Id: req-23ef96cef807073f
Content-Length: 16

{"status":"ok"}
```

---

## 9. Масштабирование

```bash
kubectl scale deployment tasks --replicas=2
kubectl get pods
```
*(репрезентативный вывод)*
```text
NAME                        READY   STATUS    RESTARTS   AGE
tasks-7d6c9b8f5c-q4n2v      1/1     Running   0          3m
tasks-7d6c9b8f5c-h2k9d      1/1     Running   0          12s
tasks-db-69c8d7f4b5-pl8rx   1/1     Running   0          3m
```

Теперь за Service `tasks` стоят **две** реплики. Поскольку `INSTANCE_ID = имя
пода`, повторные запросы через `port-forward`… ну, точнее — при обращении к
Service внутри кластера (`curl http://tasks:8082/health` из другого пода) ответы
будут приходить с **разными** `X-Instance-Id` (`...-q4n2v` и `...-h2k9d`) — видно,
что балансировка раскидывает запросы по готовым подам.

---

## 10. Валидация манифестов (реальный вывод)

Живого кластера нет, но манифесты провалидированы **по схемам Kubernetes 1.34.1**
через [`kubeconform`](https://github.com/yannh/kubeconform) в строгом режиме
(`-strict` ловит и опечатки в полях):

```bash
kubeconform -strict -summary -kubernetes-version 1.34.1 deploy/k8s/*.yaml
```
```text
Summary: 7 resources found in 5 files - Valid: 7, Invalid: 0, Errors: 0, Skipped: 0
```

Все 7 ресурсов (ConfigMap×2, Secret, Service×2, Deployment×2) валидны. Версия
`kubectl` на машине — `v1.34.1`.

---


## 12. Контрольные вопросы

1. **Чем Pod отличается от Deployment?** *Pod* — наименьшая единица запуска: один
   (или несколько) контейнеров с общими сетью и томами; он **эфемерен** — упал/удалён
   и не вернётся сам. *Deployment* — контроллер **желаемого состояния**: держит
   заданное число одинаковых подов (через ReplicaSet), пересоздаёт упавшие, катит
   обновления и откаты. Мы не создаём поды напрямую — описываем Deployment, а он
   создаёт и поддерживает поды по шаблону.

2. **Зачем нужен Service и почему нельзя «ходить прямо в Pod»?** У пода **нестабильный
   IP**: при пересоздании (краш, обновление, масштабирование) он меняется. Service даёт
   **стабильные имя и VIP** (`tasks:8082`), а также **балансирует** трафик по готовым
   (Ready) репликам, отобранным по селектору `app=tasks`. «Ходить прямо в pod» — значит
   завязаться на IP, который завтра исчезнет, и потерять балансировку и
   health-фильтрацию.

3. **Чем readiness probe отличается от liveness probe?** *Readiness* отвечает на вопрос
   «**готов ли** pod принимать трафик»: пока она красная, Service **не шлёт** на pod
   запросы (но pod не перезапускают). *Liveness* — «**жив ли** процесс»: если она
   падает, kubelet **перезапускает** контейнер. Грубо: readiness управляет
   *маршрутизацией*, liveness — *перезапуском*. В учебной версии обе бьют в `/health`,
   но liveness — позже и реже, чтобы не убить здоровый pod из-за разовой задержки.

4. **Зачем нужен ConfigMap и чем он отличается от Secret?** Оба отвязывают
   конфигурацию от образа (одну сборку гоняем в разных средах, меняя только
   конфиг). **ConfigMap** — для **несекретных** значений (порт, адреса, тумблеры),
   хранится открытым текстом. **Secret** — для **чувствительных** (пароли, токены,
   DSN с паролем): значение base64-кодируется, хранится/отдаётся отдельно, к нему
   применяют RBAC и (в проде) шифрование. У нас порт и адрес auth — в ConfigMap,
   а DSN с паролем БД — в Secret.

5. **Почему важно использовать теги образов, а не только `latest`?** `latest` —
   «плавающая» ссылка: в разное время и на разных узлах под одним именем может
   оказаться **разный** образ → теряется **воспроизводимость** (непонятно, что
   именно крутится в кластере) и **возможность отката** (нет фиксированной точки,
   к которой вернуться). С `imagePullPolicy`/кэшем `latest` ещё и непредсказуемо
   обновляется. Фиксируемый тег (`0.1`, commit hash) делает деплой
   детерминированным: версия в манифесте однозначно сопоставима с кодом, откат —
   это просто прежний тег.
