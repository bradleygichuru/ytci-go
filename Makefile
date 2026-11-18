.PHONY: build run test test-short lint sqlc-generate dev seed-then-now verify-then-now-seed

build:
	go build -o bin/server ./cmd/server

run: build
	./bin/server

dev:
	go run ./cmd/server

test:
	go test ./... -count=1

test-short:
	go test -short ./... -count=1

lint:
	golangci-lint run

sqlc-generate:
	sqlc generate

seed-then-now:
	go run ./cmd/seed-then-now

verify-then-now-seed:
	psql "$${DATABASE_URL:-postgres://postgres@localhost:5432/ytci}" -v ON_ERROR_STOP=1 -f cmd/seed-then-now/verify.sql
