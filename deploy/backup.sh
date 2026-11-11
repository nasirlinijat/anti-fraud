#!/usr/bin/env bash
set -euo pipefail

cd ~/anti-fraud/app
set -a
. ../.env
set +a
file=~/anti-fraud/backups/antifraud-$(date -u +%Y%m%dT%H%M%SZ).sql.gz
docker compose -p antifraud --env-file ../.env -f docker-compose.yml -f deploy/compose.prod.yml \
	exec -T db pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" | gzip > "$file"
ls -1t ~/anti-fraud/backups/antifraud-*.sql.gz | tail -n +8 | xargs -r rm --
echo "$file"
