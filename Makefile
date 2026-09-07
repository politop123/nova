.PHONY: api worker web infra-up infra-down infra-full verify

api:
	go run ./apps/api

worker:
	go run ./apps/worker

web:
	pnpm --filter @nova/web dev

infra-up:
	docker compose -f infrastructure/docker/docker-compose.yml up -d postgres redis

infra-down:
	docker compose -f infrastructure/docker/docker-compose.yml down

infra-full:
	docker compose -f infrastructure/docker/docker-compose.yml --profile full up --build

verify:
	pnpm verify
