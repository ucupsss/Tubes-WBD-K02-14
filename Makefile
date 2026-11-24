.PHONY: up down db-reset obs-up obs-down run worker seed dataset k6 fmt lint test test-integration check cover web-install web-dev web-build web-lint web-test web-check

up:
	docker compose up -d

down:
	docker compose down

db-reset:
	docker compose down -v && docker compose up -d

obs-up:
	docker compose --profile obs up -d

obs-down:
	docker compose --profile obs down

run:
	go run ./cmd/server

worker:
	go run ./cmd/worker

seed:
	go run ./cmd/seed

dataset:
	go run ./cmd/dataset

k6:
	k6 run load/k6/api.js

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
