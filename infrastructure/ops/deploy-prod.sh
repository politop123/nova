#!/usr/bin/env bash
set -Eeuo pipefail

NOVA_ROOT_DIR="${NOVA_ROOT_DIR:-/opt/nova}"
COMPOSE_FILE="${COMPOSE_FILE:-$NOVA_ROOT_DIR/docker-compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$NOVA_ROOT_DIR/.env}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1/health}"

cd "$NOVA_ROOT_DIR"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "Missing deployment environment: $ENV_FILE" >&2
  exit 1
fi

if [[ -n "${1:-}" ]]; then
  export IMAGE_TAG="$1"
fi

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")
"${compose[@]}" --profile migration pull
"${compose[@]}" up -d postgres redis
"${compose[@]}" --profile migration run --rm migrate
"${compose[@]}" pull api worker web caddy
"${compose[@]}" up -d api worker web caddy

curl --fail --silent --show-error --retry 15 --retry-delay 2 "$HEALTH_URL" >/dev/null
"${compose[@]}" ps
docker image prune -af --filter 'until=168h'
echo "NOVA deployment is healthy: $HEALTH_URL"
