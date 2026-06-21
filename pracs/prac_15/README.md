## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №15
**Тема:** Публикация сервиса `tasks` на Linux VPS и управление им через `systemd`.
**Технологии:** Linux VPS, SSH/SCP, `systemd` (unit + sandbox-директивы), переменные
окружения (`EnvironmentFile`), логи `journalctl`, firewall (`ufw`), reverse proxy
(NGINX — опционально).
**Объект практики:** сервис `tasks`
([services/tasks/cmd/tasks/main.go](services/tasks/cmd/tasks/main.go)) —
кросс-компилированный Go-бинарник под `linux/amd64`.

**Выбранный вариант — A (бинарник + systemd).** Курс «про systemd», поэтому деплоим
**нативный бинарник** под управлением unit-файла, а не контейнер. Это даёт прямой
контакт с `systemctl`, `journalctl`, `EnvironmentFile` и sandbox-директивами — ровно
тем, что заявлено в теме.

---

## 0. Что сделано и как это воспроизвести (честно о среде)

В репозиторий добавлен готовый, **реальный** набор артефактов деплоя
([deploy/vps/](deploy/vps/)) и проверена вся локальная часть процедуры (сборка
бинарника + смоук-тест запуска). VPS-часть (`systemctl`/`journalctl`) выполняется на
удалённой Linux-машине; здесь она задокументирована командами «как есть», а выводы
`systemctl status` помечены как **репрезентативные**. При этом **строки логов —
подлинные**: это дословный stdout того самого бинарника, снятый при локальном
смоук-тесте против эфемерного PostgreSQL (см. [§15.6](#пз-15-логи)).

| Артефакт | Назначение |
|----------|-----------|
| [deploy/vps/tasks.service](deploy/vps/tasks.service) | systemd unit (Type=simple, не-root, Restart=always, sandbox) |
| [deploy/vps/tasks.env.example](deploy/vps/tasks.env.example) | шаблон `/etc/tasks/tasks.env` (по реальному `main.go`, не по примеру методички) |
| [deploy/vps/setup-vps.sh](deploy/vps/setup-vps.sh) | однократная подготовка VPS: пользователь, каталоги, env, установка unit |
| [deploy/vps/deploy.sh](deploy/vps/deploy.sh) | сборка → `scp` → атомарная замена бинарника → рестарт |
| [deploy/vps/rollback.sh](deploy/vps/rollback.sh) | откат на `tasks.old` |

---

## 1. Подготовка VPS

**Подключение по SSH** (с локальной машины):

```bash
ssh user@<VPS_IP>
```

**Обновление пакетов** (Ubuntu/Debian):

```bash
sudo apt update && sudo apt upgrade -y
```

---

## 2. Пользователь, каталоги и конфиг

Всё ниже делает идемпотентный скрипт [deploy/vps/setup-vps.sh](deploy/vps/setup-vps.sh);
вот что именно он выполняет и почему.

**Системный пользователь без shell и без `$HOME`** — чтобы сервис не работал от root

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin tasksuser
```

**Каталог приложения:**

```bash
sudo mkdir -p /opt/tasks
sudo chown -R tasksuser:tasksuser /opt/tasks
```

**Конфиг — отдельным файлом вне репозитория**, с правами `0600 root:root` (внутри
лежит пароль БД — это секрет):

```bash
sudo mkdir -p /etc/tasks
sudo install -m 0600 -o root -g root deploy/vps/tasks.env.example /etc/tasks/tasks.env
sudo nano /etc/tasks/tasks.env   # подставить реальные секреты
```

---

## 3. Сборка бинарника и доставка на VPS

Сборка **статического** бинарника под Linux с локальной машины (выполнено реально,
вывод ниже подлинный):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o services/tasks/bin/tasks ./services/tasks/cmd/tasks
```

```text
services/tasks/bin/tasks: ELF 64-bit LSB executable, x86-64, statically linked, stripped
sha256: b862c5c641cf2be0d6d91eba17e15e60f0ad2a6a0dc05d69e6d7136d9d5547bc
size:   23 822 498 байт (~22.7 MiB)
```

Доставка и размещение (делает [deploy.sh](deploy/vps/deploy.sh)):

```bash
scp services/tasks/bin/tasks user@203.0.113.10:/tmp/tasks
# на VPS:
sudo mv /tmp/tasks /opt/tasks/tasks
sudo chown tasksuser:tasksuser /opt/tasks/tasks
sudo chmod 755 /opt/tasks/tasks
```

Итоговая структура на VPS:

```text
/opt/tasks/tasks            # бинарник (root-owner? нет: tasksuser:tasksuser, 0755)
/etc/tasks/tasks.env        # конфиг/секреты (root:root, 0600)
/etc/systemd/system/tasks.service   # unit
```

---

## 4. systemd unit

Файл [deploy/vps/tasks.service](deploy/vps/tasks.service) → `/etc/systemd/system/tasks.service`:

```ini
[Unit]
Description=Tasks Service (REST API, ПЗ №15)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=tasksuser
Group=tasksuser
WorkingDirectory=/opt/tasks
EnvironmentFile=/etc/tasks/tasks.env
ExecStart=/opt/tasks/tasks
Restart=always
RestartSec=2

# Безопасность (sandbox)
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectControlGroups=true
ProtectKernelTunables=true
ProtectKernelModules=true

LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

**Объяснение ключевых параметров:**

| Параметр | Зачем |
|----------|-------|
| `After=/Wants=network-online.target` | сервис ходит наружу (БД/Redis/Rabbit/auth) — ждём не просто сетевой стек, а его готовность |
| `Type=simple` | процесс не демонизируется сам, systemd считает его запущенным сразу после `fork` |
| `User=tasksuser` | **не root** — компрометация сервиса не даёт прав на всю систему |
| `EnvironmentFile=` | конфиг/секреты отдельно от кода и бинарника, права `0600` |
| `Restart=always` + `RestartSec=2` | автоперезапуск при падении с паузой 2 c — без busy-loop, если падает сразу |
| `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, … | sandbox: ФС почти вся read-only, нет доступа к home/устройствам/ядру. Сервис только слушает порт и пишет логи в stdout — записи на диск ему не нужно, поэтому ограничения ничего не ломают |
| `WantedBy=multi-user.target` | автозапуск на обычной (без graphical) загрузке |

---

## 5. Запуск и управление

```bash
sudo systemctl daemon-reload     # перечитать unit-файлы
sudo systemctl enable tasks      # автозапуск при загрузке
sudo systemctl start tasks       # запустить сейчас
sudo systemctl status tasks      # проверить
```

Управление: `start` / `stop` / `restart` / `status`. Репрезентативный вывод
`systemctl status tasks` (рабочий сервис):

```text
● tasks.service - Tasks Service (REST API, ПЗ №15)
     Loaded: loaded (/etc/systemd/system/tasks.service; enabled; preset: enabled)
     Active: active (running) since Sat 2026-05-30 03:48:01 UTC; 6s ago
   Main PID: 12345 (tasks)
      Tasks: 8 (limit: 1131)
     Memory: 12.4M
     CGroup: /system.slice/tasks.service
             └─12345 /opt/tasks/tasks
```

---

## 6. Логи и диагностика через journalctl

Сервис пишет **структурированный JSON в stdout** — systemd сам перенаправляет его в
journald, отдельный лог-файл не нужен.

```bash
sudo journalctl -u tasks --no-pager -n 30   # последние 30 строк
sudo journalctl -u tasks -f                  # «хвост» в реальном времени
sudo journalctl -u tasks -p err --since today  # только ошибки за сегодня
```

**Подлинный stdout бинарника** (снят локальным смоук-тестом против эфемерного
PostgreSQL; под journald эти же строки идут с префиксом `<дата> host tasks[PID]:`):

```json
{"level":"warning","msg":"REDIS_ADDR is not set, caching is disabled (DB-only mode)","service":"tasks","ts":"2026-05-30T03:47:58.990+03:00"}
{"level":"warning","msg":"RABBIT_URL is not set, event publishing is disabled","service":"tasks","ts":"2026-05-30T03:47:59.087+03:00"}
{"level":"warning","msg":"RABBIT_URL is not set, job queue producer is disabled","service":"tasks","ts":"2026-05-30T03:47:59.088+03:00"}
{"auth_grpc":"127.0.0.1:50051","instance_id":"tasks-vps-1","level":"info","msg":"server started","port":"8092","service":"tasks","ts":"2026-05-30T03:47:59.088+03:00"}
{"duration_ms":0,"level":"info","method":"GET","msg":"request completed","path":"/health","request_id":"req-23ef96cef807073f","service":"tasks","status":200,"ts":"2026-05-30T03:48:01.150+03:00"}
```

**Как выглядит упавший сервис в логах** (тоже подлинная строка — запуск без доступной
БД с верными правами, но неверным паролем): процесс пишет `level":"fatal"` и
завершается, а systemd по `Restart=always` поднимает его снова через 2 c:

```json
{"error":"failed to connect ... failed SASL auth ... (SQLSTATE 28P01)","level":"fatal","msg":"failed to ping db","service":"tasks","ts":"2026-05-30T03:21:16.805+03:00"}
```

---

## 7. Проверка доступности (/health)

Эндпоинт [`GET /health`](services/tasks/internal/http/handler.go#L39) авторизации не
требует и всегда отвечает `200 {"status":"ok"}`. **Подлинный ответ** (смоук-тест):

```bash
curl -i http://<VPS_IP>:8092/health  
```

```http
HTTP/1.1 200 OK
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
Content-Type: application/json
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-Instance-Id: tasks-vps-1
X-Request-Id: req-23ef96cef807073f
Content-Length: 16

{"status":"ok"}
```

> `X-Instance-Id: tasks-vps-1` подтверждает, что ответил именно наш инстанс (значение
> из `INSTANCE_ID`), а security-заголовки навешивает middleware из прошлых ПЗ.

---

## 8. Порты и firewall

Важная оговорка из методички: **наружу открываем только необходимое.** Текущий код
биндит `:8082` на **всех** интерфейсах ([main.go](services/tasks/cmd/tasks/main.go#L128)
— `addr := ":" + port`), поэтому есть два честных варианта:

1. **Учебный (прямое открытие порта):** разрешить 8082 в firewall.
   ```bash
   sudo ufw allow OpenSSH
   sudo ufw allow 8082/tcp
   sudo ufw enable
   ```
2. **«Как в проде» (рекомендуется):** сервис слушает локально, наружу смотрит NGINX на
   80/443 и проксирует на `127.0.0.1:8082`; в firewall открыты только 80/443 и SSH.
   Чтобы сервис слушал именно localhost, нужно либо ставить его за NGINX и закрыть 8082
   firewall'ом, либо доработать код до бинда на `127.0.0.1`.

---

## 9. Обновление и откат

**Обновление** — одной командой с локальной машины ([deploy.sh](deploy/vps/deploy.sh)):

```bash
VPS=user@<VPS_IP> ./deploy/vps/deploy.sh
```

Скрипт: собирает бинарник → `scp` в `/tmp/tasks` → на VPS `systemctl stop` →
`mv /opt/tasks/tasks /opt/tasks/tasks.old` (сохранение для отката) →
`mv /tmp/tasks /opt/tasks/tasks` → `chown/chmod` → `systemctl start` → печатает статус.

**Откат** ([rollback.sh](deploy/vps/rollback.sh)) — если новая версия не прошла `/health`:

```bash
VPS=user@<VPS_IP> ./deploy/vps/rollback.sh
```

Возвращает `tasks.old` на место и перезапускает сервис.

---

## 10. Контрольные вопросы

1. **Зачем systemd и чем он лучше `screen`/`tmux`?** systemd — штатный init/менеджер
   сервисов: автозапуск при загрузке (`enable`), автоперезапуск при падении
   (`Restart=always`), единый сбор логов (`journalctl`), управление зависимостями
   (`After=`), лимиты и sandbox. `screen`/`tmux` — это ручная сессия: упадёт сервис или
   перезагрузится сервер — никто его не поднимет, логи разрозненны, прав/лимитов нет.
2. **Почему не от root?** Принцип наименьших привилегий: если сервис скомпрометируют
   или он содержит уязвимость, ущерб ограничен правами `tasksuser` (нет shell, нет
   home), а не всей системой. Плюс `NoNewPrivileges`/`ProtectSystem` ещё сильнее
   сужают песочницу.
3. **Зачем env-конфиг в `/etc/...`, а не в репозитории?** Секреты (пароль БД, URL
   брокера) не должны попадать в git и в бинарник. Файл `/etc/tasks/tasks.env` с правами
   `0600 root:root` читается только при старте сервиса; одну и ту же сборку можно
   разворачивать в разных окружениях, меняя лишь env.
4. **Как посмотреть логи упавшего сервиса?** `journalctl -u tasks -n 100` (или `-p err`,
   `-b`, `--since`). journald хранит историю независимо от того, жив процесс или нет —
   видно последнюю `fatal`-строку с причиной (в нашем случае `failed to ping db`).
5. **Что дают `Restart=always` и `RestartSec`?** `Restart=always` — поднимать сервис
   после любого завершения (краш, OOM, ненулевой выход); `RestartSec=2` — пауза перед
   рестартом, чтобы при падении сразу на старте не уйти в busy-loop и дать зависимостям
   (БД/сети) шанс подняться.

