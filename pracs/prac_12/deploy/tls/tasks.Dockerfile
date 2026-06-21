FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/tasks ./services/tasks/cmd/tasks

FROM alpine:3.20

RUN adduser -D -u 10001 app
USER app

COPY --from=builder /out/tasks /usr/local/bin/tasks

EXPOSE 8082

ENTRYPOINT ["/usr/local/bin/tasks"]
