.PHONY: up down db-reset run seed fmt lint test test-integration check cover

up:
	docker compose up -d

down:
	docker compose down

db-reset:
	docker compose down -v && docker compose up -d

run:
	go run ./cmd/server

seed:
	go run ./cmd/seed

fmt:
	gofmt -w .

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"

test:
	go test -count=1 ./...

test-integration:
	go test -count=1 -tags=integration ./...

check: lint test-integration

cover:
	go test -tags=integration -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
