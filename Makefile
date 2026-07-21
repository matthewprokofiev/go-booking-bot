.PHONY: build run test test-integration lint vet tidy migrate-up migrate-down up down docker-build

GOOSE_DRIVER ?= postgres
DATABASE_URL ?= postgres://booking:booking@localhost:5432/booking?sslmode=disable

# При явном -o Go не дописывает .exe сам, а Windows не запустит файл без расширения.
BINARY ?= bin/bot
ifeq ($(OS),Windows_NT)
	BINARY := bin/bot.exe
endif

# Версия запинена и запускается через go run: не требует глобальной установки
# и гарантирует, что локально и в CI линтит одна и та же версия.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

build:
	go build -o $(BINARY) ./cmd/bot

run:
	go run ./cmd/bot

test:
	go test -race ./...

# Интеграционные тесты поднимают настоящий Postgres в testcontainers — нужен запущенный Docker.
test-integration:
	go test -tags=integration -count=1 -timeout 600s ./...

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

tidy:
	go mod tidy

# Migrations also run automatically on app start; these targets are for manual work.
migrate-up:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations $(GOOSE_DRIVER) "$(DATABASE_URL)" up

migrate-down:
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations $(GOOSE_DRIVER) "$(DATABASE_URL)" down

up:
	docker compose up -d --build

down:
	docker compose down

docker-build:
	docker compose build
