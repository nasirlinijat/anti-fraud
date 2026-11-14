#!/usr/bin/env bash
set -euo pipefail

host="${1:?usage: bash deploy/k8s-deploy.sh user@host [git-ref] [ssh-key-file]}"
ref="${2:-origin/main}"
key="${3:-}"
ssh_args=(-o StrictHostKeyChecking=accept-new)
if [ -n "$key" ]; then
	ssh_args+=(-i "$key")
fi

cd "$(git rev-parse --show-toplevel)"
git fetch -q origin
commit="$(git rev-parse "$ref^{commit}")"
if ! git merge-base --is-ancestor "$commit" origin/main; then
	echo "$ref is not on origin/main; images are published only for main commits" >&2
	exit 1
fi
tag="sha-${commit:0:7}"
echo "Deploying $ref ($tag) to $host"

git archive --format=tar.gz "$commit" deploy/helm deploy/datadog |
	ssh "${ssh_args[@]}" "$host" 'rm -rf ~/anti-fraud/k8s-next && mkdir -p ~/anti-fraud/k8s-next && tar -xz -C ~/anti-fraud/k8s-next'

ssh "${ssh_args[@]}" "$host" bash -s -- "$tag" <<'REMOTE'
set -euo pipefail
tag="$1"
ns=anti-fraud
datadog_chart_version=3.270.1
cd ~/anti-fraud
if [ ! -f .env ]; then
	echo "~/anti-fraud/.env is missing, see deploy/README.md" >&2
	exit 1
fi
rm -rf k8s-previous
if [ -d k8s ]; then
	mv k8s k8s-previous
fi
mv k8s-next k8s
set -a
. ./.env
set +a

kubectl get namespace "$ns" > /dev/null 2>&1 || kubectl create namespace "$ns"

secret_file="$(mktemp)"
trap 'rm -f "$secret_file"' EXIT
chmod 600 "$secret_file"
for name in API_KEY POSTGRES_PASSWORD RUNTIME_DB_PASSWORD LABELS_DB_PASSWORD REVIEW_ANALYST_KEYS OPERATOR_API_KEYS CHARGEBACK_WEBHOOK_SECRET; do
	if [ -n "${!name+x}" ]; then
		printf '%s=%s
' "$name" "${!name}"
	fi
done > "$secret_file"
kubectl -n "$ns" create secret generic anti-fraud-secrets --from-env-file="$secret_file" --dry-run=client -o yaml | kubectl apply -f - > /dev/null
secrets_checksum="$(sha256sum "$secret_file" | cut -d" " -f1)"
echo "Secret anti-fraud-secrets applied"

pull_args=()
if [ -n "${GHCR_TOKEN:-}" ]; then
	kubectl -n "$ns" create secret docker-registry ghcr --docker-server=ghcr.io \
		--docker-username="${GHCR_USER:?set GHCR_USER with GHCR_TOKEN}" --docker-password="$GHCR_TOKEN" \
		--dry-run=client -o yaml | kubectl apply -f - > /dev/null
	pull_args=(--set 'images.pullSecrets[0].name=ghcr')
fi

datadog=false
if [ -n "${DD_API_KEY:-}" ]; then
	datadog=true
	printf '%s' "$DD_API_KEY" > "$secret_file"
	kubectl -n "$ns" create secret generic datadog-secret --from-file=api-key="$secret_file" --dry-run=client -o yaml | kubectl apply -f - > /dev/null
	helm repo add datadog https://helm.datadoghq.com > /dev/null 2>&1 || true
	helm repo update datadog > /dev/null
	helm upgrade --install datadog datadog/datadog --version "$datadog_chart_version" -n "$ns" \
		-f k8s/deploy/helm/datadog-values.yaml \
		--set datadog.site="${DD_SITE:-datadoghq.eu}" \
		--set-file 'datadog.confd.postgres\.yaml=k8s/deploy/datadog/conf.d/postgres.d/conf.yaml' \
		--set-file 'datadog.confd.http_check\.yaml=k8s/deploy/datadog/conf.d/http_check.d/conf.yaml' \
		--set-file 'datadog.confd.openmetrics\.yaml=k8s/deploy/datadog/conf.d/openmetrics.d/conf.yaml' \
		--wait --timeout 5m
fi
rm -f "$secret_file"

tracing=true
if [ "${DD_APM_ENABLED:-}" = "false" ]; then
	tracing=false
fi

settings=(
	--set-string images.tag="$tag"
	--set-string secretsChecksum="$secrets_checksum"
	--set-string postgres.user="${POSTGRES_USER:?}"
	--set-string postgres.database="${POSTGRES_DB:?}"
	--set-string edge.consoleHost="${CONSOLE_HOST:?}"
	--set-string edge.apiHost="${API_HOST:?}"
	--set-string backend.modelFile="${MODEL_FILE:-lgbm-game-v9.json}"
	--set-string backend.ipIntelProvider="${IP_INTEL_PROVIDER:-off}"
	--set backend.simulationEnabled="${SIMULATION_ENABLED:-false}"
	--set datadog.enabled="$datadog"
	--set datadog.tracing="$tracing"
)
for pair in SCORE_RATE_LIMIT:scoreRateLimit SCORE_RATE_BURST:scoreRateBurst SCORE_TIMEOUT:scoreTimeout DB_MAX_CONNS:dbMaxConns; do
	name="${pair%%:*}"
	value="${!name:-}"
	if [ -n "$value" ]; then
		settings+=(--set-string "backend.${pair##*:}=$value")
	fi
done
if [ -n "${TRACING_SAMPLE_RATIO:-}" ]; then
	settings+=(--set-string datadog.tracingSampleRatio="$TRACING_SAMPLE_RATIO")
fi

upgrade_args=()
if helm -n "$ns" status anti-fraud > /dev/null 2>&1; then
	upgrade_args=(--rollback-on-failure)
fi
helm upgrade --install anti-fraud k8s/deploy/helm/anti-fraud -n "$ns" \
	-f k8s/deploy/helm/values-production.yaml "${settings[@]}" "${pull_args[@]}" "${upgrade_args[@]}" \
	--wait --timeout 10m
echo "$tag" > k8s/DEPLOYED_TAG
kubectl -n "$ns" get pods
helm -n "$ns" history anti-fraud --max 5
echo "Deployed $tag"
REMOTE
