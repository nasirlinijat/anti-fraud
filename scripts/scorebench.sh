#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
docker_host=${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}')}
case "$docker_host" in
    unix://*|npipe://*) ;;
    *) echo "The scoring benchmark requires a local Docker engine." >&2; exit 1 ;;
esac

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/scorebench.XXXXXX")
name="scorebench-${work_dir##*.}"
network=
db=
api=
cleanup() {
    if [ -n "$api" ]; then docker rm -f "$api" >/dev/null 2>&1 || true; fi
    if [ -n "$db" ]; then docker rm -fv "$db" >/dev/null 2>&1 || true; fi
    if [ -n "$network" ]; then docker network rm "$network" >/dev/null 2>&1 || true; fi
    rm -rf -- "$work_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "Source $(git -C "$root" rev-parse --short HEAD), host $(uname -sm)"
go version
docker info --format 'Docker {{.ServerVersion}} {{.Architecture}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes RAM'
docker build --iidfile "$work_dir/api-image" "$root/backend"
network=$(docker network create "$name")
password="local-$name"
api_key="api-$name"

db=$(docker run --detach --rm --name "$name-db" --network "$network" --network-alias db \
    --publish 127.0.0.1::5432 \
    -e POSTGRES_USER=scorebench -e POSTGRES_DB=scorebench -e POSTGRES_PASSWORD="$password" \
    postgres:18-alpine -c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track_utility=off)
for attempt in {1..60}; do
    if docker exec "$db" pg_isready -h 127.0.0.1 -U scorebench -d scorebench >/dev/null 2>&1; then break; fi
    if [ "$attempt" -eq 60 ]; then docker logs "$db" >&2; exit 1; fi
    sleep 1
done

db_url="postgres://scorebench:$password@db:5432/scorebench?sslmode=disable"
docker run --rm --network "$network" -v "$root/backend/migrations:/migrations:ro" migrate/migrate:v4.20.1 \
    -path=/migrations -database "$db_url" up
docker exec -i "$db" psql -X -v ON_ERROR_STOP=1 -U scorebench -d scorebench < "$root/scripts/scorebench-seed.sql"

api=$(docker run --detach --rm --name "$name-api" --network "$network" --publish 127.0.0.1::8080 \
    -e PORT=8080 -e DATABASE_URL="$db_url" -e API_KEY="$api_key" \
    -e MODEL_PATH=/models/lgbm-ieee-v5-combined.json "$(cat "$work_dir/api-image")")
db_address=$(docker port "$db" 5432/tcp)
api_address=$(docker port "$api" 8080/tcp)
for attempt in {1..60}; do
    if curl --fail --silent --max-time 1 "http://$api_address/ready" >/dev/null; then break; fi
    if [ "$attempt" -eq 60 ]; then docker logs "$api" >&2; exit 1; fi
    sleep 1
done

cd "$root/backend"
SCOREBENCH_DATABASE_URL="postgres://scorebench:$password@$db_address/scorebench?sslmode=disable" \
    SCOREBENCH_API_URL="http://$api_address" SCOREBENCH_API_KEY="$api_key" \
    go test -count=1 -timeout=5m -run '^TestScoreBenchmark$' -v ./internal/db
