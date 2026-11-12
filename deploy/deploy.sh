#!/usr/bin/env bash
set -euo pipefail

host="${1:?usage: bash deploy/deploy.sh user@host [git-ref] [ssh-key-file]}"
ref="${2:-origin/main}"
key="${3:-}"
ssh_args=(-o StrictHostKeyChecking=accept-new)
if [ -n "$key" ]; then
	ssh_args+=(-i "$key")
fi

cd "$(git rev-parse --show-toplevel)"
git fetch -q origin
commit="$(git rev-parse --short "$ref")"
echo "Deploying $ref ($commit) to $host"

git archive --format=tar.gz "$ref" |
	ssh "${ssh_args[@]}" "$host" 'rm -rf ~/anti-fraud/next && mkdir -p ~/anti-fraud/next && tar -xz -C ~/anti-fraud/next'

ssh "${ssh_args[@]}" "$host" bash -s -- "$commit" <<'REMOTE'
set -euo pipefail
cd ~/anti-fraud
if [ ! -f .env ]; then
	echo "~/anti-fraud/.env is missing, see deploy/README.md" >&2
	exit 1
fi
rm -rf previous
if [ -d app ]; then
	mv app previous
fi
mv next app
cd app
echo "$1" > DEPLOYED_COMMIT
files=(-f docker-compose.yml -f deploy/compose.prod.yml)
if grep -q '^DD_API_KEY=.' ../.env; then
	files+=(-f deploy/compose.datadog.yml)
fi
compose() {
	docker compose -p antifraud --env-file ../.env "${files[@]}" "$@"
}
compose up -d --build --wait --remove-orphans
docker image prune -f > /dev/null
compose ps
echo "Deployed $1"
REMOTE
