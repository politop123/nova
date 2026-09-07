#!/usr/bin/env bash
set -Eeuo pipefail

NOVA_ROOT_DIR="${NOVA_ROOT_DIR:-/opt/nova}"
COMPOSE_FILE="${COMPOSE_FILE:-$NOVA_ROOT_DIR/docker-compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$NOVA_ROOT_DIR/.env}"
BACKUP_DIR="${BACKUP_DIR:-$NOVA_ROOT_DIR/backups}"

cd "$NOVA_ROOT_DIR"
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_path="$BACKUP_DIR/nova-$timestamp.sql.gz"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres \
  pg_dump --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" | gzip > "$backup_path"
chmod 600 "$backup_path"
find "$BACKUP_DIR" -type f -name 'nova-*.sql.gz' -mtime +7 -delete
echo "Created PostgreSQL backup: $backup_path"
