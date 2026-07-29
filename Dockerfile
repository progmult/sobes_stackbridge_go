FROM golang:1.25-alpine AS builder

WORKDIR /src

# Слой с зависимостями кэшируется отдельно от исходников.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -trimpath убирает пути сборочной машины из бинаря, -s -w выкидывают
# отладочные таблицы.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.20

# Образ оставлен полноценным, а не scratch: busybox нужен для healthcheck
# в docker compose.
RUN adduser -D -u 10001 app

COPY --from=builder /out/api /app/api

USER app

EXPOSE 8080

ENTRYPOINT ["/app/api"]
