.PHONY: up down db-reset run worker seed dataset fmt lint test test-integration check cover web-install web-dev web-build web-lint web-test web-check

up:
	docker compose up -d

down:
	docker compose down

db-reset:
	docker compose down -v && docker compose up -d

run:
	go run ./cmd/server

worker:
	go run ./cmd/worker

seed:
	go run ./cmd/seed

dataset:
	go run ./cmd/dataset

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

# Frontend SPA (frontend/). Hasil build ditulis ke web/dist lalu di-embed ke binary Go.
web-install:
	cd frontend && npm install

web-dev:
	cd frontend && npm run dev

web-build:
	cd frontend && npm run build

web-lint:
	cd frontend && npm run lint

web-test:
	cd frontend && npm test

web-check: web-lint web-test web-build
