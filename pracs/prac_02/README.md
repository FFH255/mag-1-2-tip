## Студент: Фадеев Всеволод Вадимович
## Группа: ЭФМО-02-25
# ПЗ №2 — gRPC

## Цель
Научиться работать с gRPC: описывать контракт в .proto,
поднимать gRPC-сервер и вызывать его из другого сервиса (клиента) с
дедлайном.

## Установка и запуск

(Необходимы предустановленные Go версии 1.22 и выше и Git)

Клонировать репозиторий:

```
git clone <URL_РЕПОЗИТОРИЯ>
cd pracs/prac_02
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
prac_02/
├── go.mod
├── go.sum
├── README.md
├── proto/
│   └── auth.proto
├── gen/
│   └── auth/
│       └── v1/
│           ├── auth.pb.go
│           └── auth_grpc.pb.go
├── services/
│   ├── auth/
│   │   ├── cmd/
│   │   │   └── auth/
│   │   │       └── main.go
│   │   └── internal/
│   │       ├── core/
│   │       ├── grpc/
│   │       │   └── server.go
│   │       ├── http/
│   │       │   ├── handler.go
│   │       │   └── handlers/
│   │       ├── platform/
│   │       │   └── config/
│   │       ├── repo/
│   │       └── service/
│   │           └── auth.go
│   └── tasks/
│       ├── cmd/
│       │   └── tasks/
│       │       └── main.go
│       └── internal/
│           ├── client/
│           │   └── authclient/
│           │       └── client.go
│           ├── http/
│           │   └── handler.go
│           └── service/
│               └── tasks.go
└── shared/
    ├── httpx/
    │   └── json.go
    └── middleware/
        ├── logging.go
        └── requestid.go
```

## .proto файл
```proto
syntax = "proto3";

package auth.v1;

option go_package = "example.com/tech-ip-proto/gen/auth/v1;authv1";

message VerifyRequest {
  string token = 1;
}

message VerifyResponse {
  bool valid = 1;
  string subject = 2;
}

service AuthService {
  rpc Verify(VerifyRequest) returns (VerifyResponse);
}
```

## Команды генерации
```
protoc --proto_path=. `
  --go_out=. --go_opt=module=prac_02 `
  --go-grpc_out=. --go-grpc_opt=prac_02 `
  proto/auth.proto
```

Код появится в:

- gen/auth/v1/auth.pb.go
- gen/auth/v1/auth_grpc.pb.go

## Маппинг ошибок

| gRPC code                |                 HTTP status | Когда возникает                    |
| ------------------------ | --------------------------: | ---------------------------------- |
| `codes.Unauthenticated`  |          `401 Unauthorized` | Невалидный или отсутствующий токен |
| `codes.Unavailable`      |           `502 Bad Gateway` | Сервис `Auth` недоступен           |
| `codes.DeadlineExceeded` |           `502 Bad Gateway` | Истёк `deadline` при вызове `Auth` |
| `codes.Internal`         |           `502 Bad Gateway` | Внутренняя ошибка сервиса `Auth`   |
| local/internal error     | `500 Internal Server Error` | Прочие внутренние ошибки `Tasks`   |


## Скриншоты
### Пример логов (успех + Auth недоступен)

<img width="1280" height="411" alt="image" src="https://github.com/user-attachments/assets/67e196a7-1664-4842-a8e4-de9e7cab8df5" />

<img width="1280" height="436" alt="image" src="https://github.com/user-attachments/assets/72e07898-bfb8-46e8-8e2e-32a517c3dc5f" />


### Получить токен

```
http://91.200.84.37:8083/v1/auth/login
```

<img width="1275" height="1004" alt="image" src="https://github.com/user-attachments/assets/b421ee73-5f5a-4733-84e8-7b5cf0e590a0" />

### Создать задачу

```
http://91.200.84.37:8084/v1/tasks
```

<img width="1278" height="1108" alt="image" src="https://github.com/user-attachments/assets/4189cbfb-eccb-48e0-9f3d-1dd6c7fdf035" />

### Проверить список задач

```
http://91.200.84.37:8084/v1/tasks
```

<img width="1272" height="1179" alt="image" src="https://github.com/user-attachments/assets/7cc8c074-31b2-4081-9a75-3bfbf33e3df1" />

### Список, когда Auth недоступен

```
http://91.200.84.37:8084/v1/tasks
```

<img width="1272" height="956" alt="image" src="https://github.com/user-attachments/assets/2702c1d2-ffda-4de7-96e7-d2921601d9c6" />

# Ответы на вопросы
- Что такое .proto и почему он считается контрактом? .proto — это файл, в котором на языке Protocol Buffers описываются структуры данных и методы сервиса. Он считается контрактом, потому что строго определяет, какие запросы и ответы могут передаваться между сервисами, и любое изменение требует явного согласования.
- Что такое deadline в gRPC и чем он полезен? Deadline — это максимальное время ожидания ответа от сервера, которое клиент устанавливает для каждого вызова. Он полезен тем, что предотвращает зависание клиента при недоступности сервера и позволяет быстро возвращать ошибки типа DeadlineExceeded.
- Почему “exactly-once” не даётся просто так даже в RPC? Из-за возможных сбоев сети, таймаутов и повторных отправок сервер может получить запрос несколько раз, а клиент — не получить подтверждение. Гарантия ровно одного выполнения требует идемпотентности или распределённых транзакций, что сложно реализовать.
- Как обеспечивать совместимость при расширении .proto? Использовать правила Protocol Buffers: добавлять новые поля с уникальными номерами, не менять типы и номера существующих полей, помечать необязательные поля как optional. Это позволяет старым клиентам работать с новыми серверами и наоборот.