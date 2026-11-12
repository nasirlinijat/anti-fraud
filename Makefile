-include .env
export

TEST_DATABASE_URL ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
TEST_REDIS_URL ?= redis://localhost:$(REDIS_PORT)/0

.PHONY: up down db run test test-integration test-ml lint lint-frontend migrate docs simulate

up:
	docker compose up -d --build --wait

down:
	docker compose down

db:
	docker compose up -d --wait db

run: migrate
	docker compose up -d --wait redis
	cd backend && go run ./cmd/api

# Packages share one test database, so their tests run one package at a time.
test: migrate
	docker compose up -d --wait redis
	cd backend && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_REDIS_URL="$(TEST_REDIS_URL)" go test -p 1 ./...

test-integration:
	cd backend && TEST_DATABASE_URL= go test -p 1 -tags=integration ./test/...

lint:
	@unformatted="$$(gofmt -l backend)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Files need gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	cd backend && go vet ./...

lint-frontend:
	cd frontend && npm run check

test-ml:
	cd ml && .venv/bin/python -m pytest tests

migrate:
	docker compose run --rm migrate
	docker compose run --rm --no-deps db-permissions

docs:
	@echo "Swagger UI: http://localhost:$${SWAGGER_PORT:-8090} (press Ctrl+C to stop)"
	docker compose up swagger-ui

simulate:
	cd backend && go run ./cmd/simulator $(ARGS)
