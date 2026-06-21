# syntax=docker/dockerfile:1.7

# ---------- Stage 1: builder ----------
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /out/app ./services/auth/cmd/auth

# ---------- Stage 2: runner ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 app

USER app

COPY --from=builder /out/app /usr/local/bin/app

# HTTP и gRPC порты по умолчанию.
EXPOSE 8085 50051

ENTRYPOINT ["/usr/local/bin/app"]
