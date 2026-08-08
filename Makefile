.PHONY: up down logs dev dev-down dev-logs api-test frontend-build backend-build dns-reliability

up:
	docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build

down:
	docker compose down

logs:
	docker compose logs -f

dev:
	docker compose -f docker-compose.dev.yml up --build

dev-down:
	docker compose -f docker-compose.dev.yml down

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f

api-test:
	go test ./...

dns-reliability:
	bash tools/dns-reliability.sh

backend-build:
	go build ./cmd/faro-api

frontend-build:
	cd frontend && npm run build
