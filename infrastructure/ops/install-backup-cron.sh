#!/usr/bin/env bash
set -Eeuo pipefail

NOVA_ROOT_DIR="${NOVA_ROOT_DIR:-/opt/nova}"
mkdir -p "$NOVA_ROOT_DIR/backups"
chmod 700 "$NOVA_ROOT_DIR/backups"
cron_line="17 2 * * * $NOVA_ROOT_DIR/backup-postgres.sh >> $NOVA_ROOT_DIR/backups/backup.log 2>&1"

(crontab -l 2>/dev/null | grep -vF "$NOVA_ROOT_DIR/backup-postgres.sh" || true; echo "$cron_line") | crontab -
echo "Installed daily PostgreSQL backup cron job at 02:17 server time."
