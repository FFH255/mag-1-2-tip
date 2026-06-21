## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №8

**Тема:** CI/CD pipeline на GitHub Actions — тесты, сборка, упаковка Docker-образов и публикация в registry.
**Объект:** репозиторий с двумя Go-сервисами `auth` и `tasks` (один модуль `prac_08`, Go 1.25).

---

## 1. Что сделано

Настроен GitHub Actions pipeline в файле [`.github/workflows/ci.yml`](.github/workflows/ci.yml).
На каждый `push`/`pull_request` в `main`/`master` он:

1. забирает код (checkout);
2. ставит нужную версию Go (из `go.mod`);
3. прогоняет статанализ `go vet ./...`;
4. прогоняет тесты `go test -race -count=1 ./...`;
5. собирает весь модуль `go build ./...`;
6. собирает Docker-образы **обоих** сервисов (`auth` и `tasks`);
7. публикует образы в **GitHub Container Registry (ghcr.io)**.

> **Почему оба сервиса.** У `auth` и `tasks` есть собственные multi-stage Dockerfile
> ([`services/auth/Dockerfile`](services/auth/Dockerfile), [`services/tasks/Dockerfile`](services/tasks/Dockerfile)),
> и оба входят в [`deploy/docker-compose.yml`](deploy/docker-compose.yml). Сборка вынесена в `matrix`,
> поэтому добавить сервис — это одна строка, а не копипаста job.

---

## 2. Структура pipeline (jobs)

Пайплайн разбит на два job — так нагляднее, чем один общий:

| Job | Назначение | Условие запуска |
|-----|------------|-----------------|
| `test`   | `go vet` + `go test` + `go build` | всегда |
| `docker` | сборка Docker-образов `auth` и `tasks` + push в GHCR | только после успешного `test` (`needs: test`) |

```
push / pull_request
        │
        ▼
   ┌──────────┐      needs: test     ┌─────────────────────────────┐
   │  test    │ ───────────────────► │  docker (matrix: auth,tasks)│
   │ vet/test │                      │  buildx → login → build/push│
   │  /build  │                      └─────────────────────────────┘
   └──────────┘
```

### Job `test` — пошагово
- `actions/checkout@v4` — выгрузка репозитория.
- `actions/setup-go@v5` с `go-version-file: go.mod` — версия Go берётся из `go.mod` (нет рассинхрона), `cache: true` кеширует модули.
- `go vet ./...` — быстрый статический анализ.
- `go test -race -count=1 ./...` — тесты с детектором гонок; `-count=1` отключает кеш, чтобы тесты реально выполнились.
- `go build ./...` — компиляция всех пакетов модуля (оба сервиса входят сюда).

### Job `docker` — пошагово
- `actions/checkout@v4`.
- `docker/setup-buildx-action@v3` — поднимает Buildx (доступ к сборщику Docker в раннере).
- `docker/login-action@v3` — логин в `ghcr.io` под `github.actor` с встроенным `GITHUB_TOKEN`.
- `docker/metadata-action@v5` — формирует теги и labels образа (см. §3).
- `docker/build-push-action@v6` — сборка с `context: .` и `file: services/<service>/Dockerfile`, push в registry. Включён кеш слоёв `type=gha`.

**Про multi-service контекст:** оба Dockerfile рассчитаны на контекст = корень проекта
(один `go.mod` на всех), поэтому в CI указано `context: .` и `file: services/${{ matrix.service }}/Dockerfile` —
ровно как в `docker-compose.yml` (`context: ..`, `dockerfile: services/<svc>/Dockerfile`).

---

## 3. Стратегия версионирования образов

Теги формирует `docker/metadata-action`:

| Тег | Когда | Пример |
|-----|-------|--------|
| `sha-<short>` | всегда | `ghcr.io/<owner>/techip-tasks:sha-1a2b3c4` |
| `latest`      | только основная ветка | `ghcr.io/<owner>/techip-tasks:latest` |

Идея: по тегу `sha-...` всегда видно **точную версию** (конкретный коммит), а `latest` указывает на последнюю сборку основной ветки. Имя образа `metadata-action` автоматически приводит к нижнему регистру (требование GHCR).

---

## 4. Куда публикуется образ

- **Registry:** GitHub Container Registry — `ghcr.io`.
- **Имена образов:**
  - `ghcr.io/<owner>/techip-auth`
  - `ghcr.io/<owner>/techip-tasks`

  где `<owner>` — `${{ github.repository_owner }}` (владелец репозитория).
- **Авторизация:** встроенный `secrets.GITHUB_TOKEN` (отдельный PAT не требуется), права заданы через `permissions: packages: write`.

---

## 5. Секреты и переменные

| Что | Где хранится | Зачем |
|-----|--------------|-------|
| `GITHUB_TOKEN` | выдаётся Actions автоматически | login и push в GHCR |

Дополнительные секреты для текущего объёма (тесты + сборка + push в GHCR) **не нужны**.

Соблюдено правило задания: **никаких токенов/паролей в `.yml`** — используется только встроенный токен. `.env` с секретами в репозиторий не коммитится (`.env`, `.env.*` уже в [`.dockerignore`](.dockerignore)).

> Если позже добавлять деплой на VPS по SSH — приватный SSH-ключ и адрес сервера кладутся в
> **Settings → Secrets and variables → Actions** (например `SSH_PRIVATE_KEY`, `SSH_HOST`, `SSH_USER`)
> и читаются как `${{ secrets.SSH_PRIVATE_KEY }}`. Сам ключ в отчёте/коде не раскрывается.

---

## 6. Как проверить

1. Создать репозиторий на GitHub и запушить код (см. §8).
2. Открыть вкладку **Actions** — workflow `CI` запустится автоматически.
3. Убедиться, что оба job зелёные: `test` (vet/test/build) и `docker` (build+push для `auth` и `tasks`).
4. Образы появятся в **Packages** профиля/организации (`techip-auth`, `techip-tasks`).
5. Приложить ссылку на успешный прогон и/или скриншот в отчёт (раздел ниже).

> ⚠️ Локально тесты и сборка уже проверены и проходят (`go test ./...`, `go build ./...` — OK).
> Сам прогон CI и скриншот появляются только после пуша в GitHub — этот шаг выполняется вне данной среды.

### Скриншот/лог успешного прогона

<img width="1383" height="947" alt="image" src="https://github.com/user-attachments/assets/650cbd22-6087-4f1e-aa1c-fa48a2606aeb" />


---

## 7. Команды для первого пуша (репозиторий ещё не инициализирован)

```bash
git init
git add .
git commit -m "PZ8: CI на GitHub Actions (test + build + docker push в GHCR)"
git branch -M main
git remote add origin https://github.com/<owner>/tech-ip-GitHubActions.git
git push -u origin main
```

---

## 8. Контрольные вопросы

1. **Чем CI отличается от CD?**
   CI (Continuous Integration) — автоматическая интеграция изменений: на каждый push собираем и тестируем код, рано ловим поломки. CD (Continuous Delivery/Deployment) — продолжение: автоматическая упаковка и доставка артефакта до registry/сервера (Delivery — до готового к выкатке артефакта/ручного апрува; Deployment — вплоть до автоматического деплоя в прод).

2. **Почему `go test` должен запускаться в pipeline?**
   Чтобы поломка ловилась автоматически у всех и на каждом изменении, а не «на машине одного разработчика». CI прогоняет тесты в чистом окружении — это объективная проверка перед merge/сборкой образа; красный pipeline блокирует выкатку заведомо нерабочего кода.

3. **Что такое секреты CI и почему их нельзя хранить в репозитории?**
   Это чувствительные данные (токены, ключи, пароли), которые CI получает из защищённого хранилища (GitHub Secrets) и маскирует в логах. В репозитории их хранить нельзя: git хранит всю историю — даже удалённый позже секрет остаётся в коммитах, виден всем с доступом к репо (а у публичного — всему миру) и его придётся срочно ротировать.

4. **Почему важно версионировать docker-образы?**
   Чтобы точно знать, какой код задеплоен, и иметь возможность воспроизвести/откатить версию. Если всё всегда `latest`, нельзя понять, что именно работает на сервере, и откат превращается в угадывание. Тег по hash коммита (`sha-...`) однозначно привязывает образ к исходнику.

5. **Какие риски у автоматического деплоя без ручного контроля?**
   Любой сломанный, но «зелёный» по тестам коммит мгновенно уезжает в прод; плохая миграция БД или неверный конфиг могут уронить сервис; нет паузы, чтобы поймать проблему до пользователей. Снижают риск: staging-окружение, ручной approval (environments), health-check'и и автоматический откат, канареечные/поэтапные выкатки.
