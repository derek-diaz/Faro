.PHONY: up down logs api-test frontend-build backend-build

up:
	docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build

down:
	docker compose down

logs:
	docker compose logs -f

api-test:
	go test ./...

backend-build:
	go build ./cmd/faro-api

frontend-build:
	cd frontend && npm run build
