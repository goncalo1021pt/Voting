#!/bin/sh
# Nightly Postgres dump with retention, run by events-backup.timer from the
# repo root. Credentials come from inside the running postgres container.
set -eu

BACKUP_DIR="${BACKUP_DIR:-backups}"
RETAIN="${RETAIN:-14}"

mkdir -p "$BACKUP_DIR"
stamp=$(date +%F_%H%M%S)
tmp="$BACKUP_DIR/.partial_$stamp.sql"
out="$BACKUP_DIR/events_$stamp.sql.gz"

# Dump to a temp file first — piped straight into gzip, the pipeline's exit
# status would be gzip's, and a failed dump (stack down, bad credentials)
# would land as a tiny "successful" archive that retention then rotates the
# real backups out for.
docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' > "$tmp" || {
	rm -f "$tmp"
	echo "backup FAILED: pg_dump exited nonzero" >&2
	exit 1
}
gzip -c "$tmp" > "$out"
rm -f "$tmp"

# Drop everything beyond the newest RETAIN dumps.
ls -1t "$BACKUP_DIR"/events_*.sql.gz 2>/dev/null | tail -n +$((RETAIN + 1)) | xargs -r rm --

echo "backup written: $out"
