.PHONY: build run test test-short lint sqlc-generate dev

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
