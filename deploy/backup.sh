#!/bin/sh
# Nightly Postgres dump with retention, run by events-backup.timer from the
# repo root. Credentials come from inside the running postgres container.
set -eu

BACKUP_DIR="${BACKUP_DIR:-backups}"
RETAIN="${RETAIN:-14}"

mkdir -p "$BACKUP_DIR"
out="$BACKUP_DIR/events_$(date +%F_%H%M%S).sql.gz"

docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip > "$out"

# Drop everything beyond the newest RETAIN dumps.
ls -1t "$BACKUP_DIR"/events_*.sql.gz 2>/dev/null | tail -n +$((RETAIN + 1)) | xargs -r rm --

echo "backup written: $out"
