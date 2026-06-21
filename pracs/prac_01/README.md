## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №1 — Микросервисы Auth + Tasks

## Цель
Научиться декомпозировать небольшую систему на два сервиса и
организовать корректное синхронное взаимодействие по HTTP (с
таймаутами, статусами ошибок и прокидыванием request-id)

В рамках ПЗ мы делаем учебную систему из двух компонентов:
1) Auth service — отвечает за “проверку доступа”
(упрощённая логика).
2) Tasks service — CRUD задач, но каждый запрос требует
проверки через Auth

## Установка и запуск

(Необходимы предустановленные Go версии 1.22 и выше и Git)

Клонировать репозиторий:

```
git clone <URL_РЕПОЗИТОРИЯ>
cd pracs/prac_01
```

Команда запуска сервера:

Терминал 1
```
go run ./services/auth/cmd/auth
```
Терминал 2
```
go run ./services/tasks/cmd/tasks
```

## Структура проекта
```plaintext
prac_01/
├── go.mod
├── go.sum
├── cmd/
│   ├── auth/
│   │   └── main.go
│   └── tasks/
│       └── main.go
├── internal/
│   ├── auth/
│   │   ├── service/
│   │   │   └── auth.go
│   │   └── http/
│   │       ├── router.go
│   │       └── handlers/
│   │           └── auth.go
│   ├── tasks/
│   │   ├── service/
│   │   │   └── tasks.go
│   │   ├── client/
│   │   │   └── authclient.go
│   │   └── http/
│   │       ├── router.go
│   │       └── handlers/
│   │           └── tasks.go
│   └── shared/
│       ├── httpx/
│       │   └── json.go
│       └── middleware/
│           ├── logging.go
│           └── requestid.go
├── README.md
```

## Границы ответственности
Auth service
* выдаёт “токен” (упрощённо),
* проверяет токен,
* возвращает информацию: валиден/не валиден.

Tasks service
* хранит и управляет задачами,
* перед выполнением операций проверяет токен через Auth.

## Схема взаимодействия
```mermaid
sequenceDiagram
    participant C as Client
    participant T as Tasks service
    participant A as Auth service
    C->>T: Request with Authorization
    T->>A: GET /v1/auth/verify (timeout 2-3s)

    alt Valid token
        A-->>T: 200 OK (valid)
        T-->>C: 200 OK / 201 Created / 204 No Content
    else Invalid token
        A-->>T: 401 Unauthorized
        T-->>C: 401 Unauthorized
    else Forbidden action
        A-->>T: 200 OK (valid)
        T-->>C: 403 Forbidden
    else Auth timeout or server error
        A-->>T: timeout / 5xx
        T-->>C: 502 Bad Gateway / 503 Service Unavailable
    end
```

## Список эндпоинтов (Auth и Tasks)

- Auth service
  - `POST /v1/auth/login`
  - `GET /v1/auth/verify`
- Tasks service
  - `POST /v1/tasks`
  - `GET /v1/tasks`
  - `GET /v1/tasks/{id}`
  - `PATCH /v1/tasks/{id}`
  - `DELETE /v1/tasks/{id}`


## Учётные данные для demo

- username: `student`
- password: `student`
- token: `demo-token`

## Скриншоты
### Скрин/лог с request-id, подтверждающий прокидывание.

<img width="1795" height="565" alt="image" src="https://github.com/user-attachments/assets/6c3769e6-9f4e-4b50-a304-b7dc4dcacef8" />



<img width="1796" height="513" alt="image" src="https://github.com/user-attachments/assets/ee2358cd-af8d-4b6b-967b-7adf7a459b8c" />


### Получить токен

```
http://91.200.84.37:8081/v1/auth/login
```

<img width="479" height="461" alt="image" src="https://github.com/user-attachments/assets/83e8c3e2-1445-45fe-92b3-fffeecab7b4f" />


### Проверка токена напрямую

```
http://91.200.84.37:8081/v1/auth/verify
```

<img width="597" height="458" alt="image" src="https://github.com/user-attachments/assets/c8e5b8b0-bb26-4d80-88e4-7f66c3b2fc46" />


### Создать задачу через Tasks (с проверкой Auth)

```
http://91.200.84.37:8082/v1/tasks
```

<img width="568" height="517" alt="image" src="https://github.com/user-attachments/assets/3ccb74b7-e6a1-411a-b082-37f253d1ee94" />


### Попробовать без токена (должно быть 401)

```
http://91.200.84.37:8082/v1/tasks
```

<img width="536" height="455" alt="image" src="https://github.com/user-attachments/assets/cd5f3661-b8ba-4327-b432-9c8095908631" />

# Ответы на вопросы
- Почему межсервисный вызов должен иметь таймаут? Чтобы сервис не зависал навсегда в ожидании ответа от другого сервиса, если тот упал или недоступен по сети.
- Чем request-id помогает при диагностике ошибок? Позволяет связать воедино логи разных сервисов, через которые прошел один запрос. По айди можно найти запросы и поэтапно пройти путь и найти где возникла ошибка.
- Какие статусы нужно вернуть клиенту при невалидном токене? 401 Unauthorized
- Чем опасно “делить одну БД” между сервисами? Высокая нагрузка на базу данных Изменение от одного сервиса может сломать взаимодействие с другим сервисом
