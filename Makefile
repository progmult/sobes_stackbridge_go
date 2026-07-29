.PHONY: up down logs test vet swagger

# Запуск сервиса и БД
up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api

# Разработка: требуется установленный Go
test:
	go test ./... -count=1

vet:
	go vet ./...

swagger:
	go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/api/main.go -o docs
