.PHONY: lint test build generate migrate

lint:
	golangci-lint run

test:
	go test ./... -count=1 -timeout 600s

build:
	go build -o bin/gabon ./cmd/gabon

generate:
	sqlc generate

migrate:
	goose -dir internal/db/migrations postgres "$(DATABASE_URL)" up
