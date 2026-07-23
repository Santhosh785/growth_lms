.PHONY: docker-build docker-up docker-down migrate-up migrate-down migrate-fresh lint test fmt ci build setup backup health status

DATABASE_URL ?= $(shell grep -E '^LMS_DATABASE_URL=' .env 2>/dev/null | cut -d '=' -f2-)
MIGRATIONS_DIR = db/migrations

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

migrate-up:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

migrate-fresh:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" drop -f
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

lint:
	gofmt -l . | tee /tmp/gofmt-out; test ! -s /tmp/gofmt-out
	golangci-lint run ./...

fmt:
	gofmt -w .

test:
	go test -race ./...

ci: fmt lint test
	@echo "CI checks passed locally. (gitleaks and migration validation run in GitHub Actions.)"

# Operational commands via the app binary (internal/cli). These read the same
# validated config the services use, so they can never target a different
# database than the running server.
build:
	go build -o bin/app ./cmd/app

setup: build
	./bin/app setup

backup: build
	./bin/app backup

health: build
	./bin/app health

status: build
	./bin/app status
