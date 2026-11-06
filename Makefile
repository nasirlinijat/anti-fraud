-include .env
export

.PHONY: up down db run test lint migrate generate

up:
	docker compose up -d --build --wait

down:
	docker compose down

db:
	docker compose up -d --wait db

run: db
	cd backend && go run ./cmd/api

test: migrate
	cd backend && TEST_DATABASE_URL="$(DATABASE_URL)" go test ./...

lint:
	@unformatted="$$(gofmt -l backend)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Files need gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	cd backend && go vet ./...

migrate:
	docker compose run --rm migrate

generate:
	docker compose run --rm sqlc
