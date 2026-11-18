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

# --- Execution contracts (ticket #359) -------------------------------------

contracts-test:
	go test ./internal/contracts ./cmd/contracts-mock -count=1

contracts-validate:
	go test ./internal/contracts -run 'TestOpenAPI|TestCloud|TestRole|TestFixture|TestMigrationsApplyOnFreshDatabase' -count=1 -v

mock:
	go run ./cmd/contracts-mock -addr :8099

contracts-clients:
	@echo "TypeScript (mobile, #360):"
	@echo "  npx --yes openapi-typescript@7 api/openapi/contracts.v1.yaml -o ../ytci-mobile/lib/api/generated/contracts.ts"
	@echo "Go (optional):"
	@echo "  go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest -config api/openapi/oapi-codegen.yaml api/openapi/contracts.v1.yaml"
