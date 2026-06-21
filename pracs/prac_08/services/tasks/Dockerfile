# syntax=docker/dockerfile:1.7

# ---------- Stage 1: builder ----------
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Сначала только модульные файлы — чтобы слой со скачанными зависимостями
# не инвалидировался при изменении исходников.
COPY go.mod go.sum ./
RUN go mod download

# Копируем остальной код модуля.
COPY . .

# Статическая сборка под Linux: бинарник не зависит от libc и работает в alpine/scratch.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /out/app ./services/tasks/cmd/tasks

# ---------- Stage 2: runner ----------
FROM alpine:3.20

# Сертификаты для исходящих TLS-запросов (например, к внешним API).
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 app

USER app

COPY --from=builder /out/app /usr/local/bin/app

# Информационно: какой порт сервис слушает по умолчанию (фактический — из TASKS_PORT).
EXPOSE 8082

ENTRYPOINT ["/usr/local/bin/app"]
