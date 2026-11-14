# Helm chart

`deploy/helm/anti-fraud` runs the same stack as `docker-compose.yml` plus `deploy/compose.prod.yml` on Kubernetes, using the images that the `Images` workflow publishes to GitHub Container Registry.

```
Internet ──► Service edge (80, 443, 443/udp) ──► Caddy with deploy/Caddyfile
               ├── API_HOST      ──► Service backend   /score, /health, /ready only
               └── CONSOLE_HOST  ──► Service frontend  ──► Service backend
Service db     ──► StatefulSet db, PostgreSQL with a 5 GiB volume
Service redis  ──► Deployment redis, the blocklist cache without persistence
Job dbsetup    ──► runs the migrations, then backend/runtime-role.sql
```

| Kubernetes object | Compose service it replaces | Notes |
|---|---|---|
| StatefulSet and headless Service `db` | `db` | `pg_isready` probes, data on a PersistentVolumeClaim `data-db-0` |
| Deployment and Service `redis` | `redis` | the same `redis-server` flags, no disk |
| Job `<release>-dbsetup-<revision>` | `migrate` and `db-permissions` | one image with `migrate`, `psql`, the migrations and the roles SQL |
| Deployment and Service `backend` | `backend` | waits in an init container until the runtime role can read the schema |
| Deployment and Service `frontend` | `frontend` | unchanged image and `frontend/Caddyfile` |
| Deployment and Service `edge`, PVC `edge-data` | `edge` in `compose.prod.yml` | the certificates live on `edge-data` so a restart does not ask Let's Encrypt again |
| Secret `<release>-secrets` | `.env` | or an existing Secret, see below |

## Why the chart looks like this

- **Fixed Service names.** The Services are called `db`, `redis`, `backend`, `frontend` and `edge`, the same names as the Compose services. `frontend/Caddyfile` proxies to `backend:{$BACKEND_PORT}` and `deploy/Caddyfile` to `backend` and `frontend:8080`, so both Caddyfiles work unchanged. The cost is one release per namespace.
- **Caddy instead of an Ingress controller.** The console Caddy accepts only `Host: localhost` and a matching `Origin`, and the edge Caddy rewrites both before it forwards. The edge also allows only three API paths and gets its own certificates. An Ingress controller plus cert-manager would have to repeat all of that in annotations, so the chart runs the edge Caddy we already trust in production.
- **Database setup before the new code.** On `helm install` the setup Job is a normal resource: it starts with everything else and the backend's init container waits until the runtime role can read `simulation_runs`, which exists only after the migrations and the grants. On `helm upgrade` the Job is a `pre-upgrade` hook, so Helm runs the new migrations and grants first and only then rolls out the new backend. A failed migration stops the upgrade and the old backend keeps serving.
- **One backend replica.** The score rate limiter and the sign-in throttle count requests in memory per process. Two replicas would double both limits, so `backend.replicas` stays 1 until those counters move to Redis.
- **Rolling update without downtime.** The backend uses `maxUnavailable: 0`: the new pod must pass `/ready` before the old one stops. The old pod first waits 5 seconds (`preStop`) so the Service stops sending it requests, then the backend's own shutdown finishes the requests in progress.
- **Secrets restart the pods that read them.** The backend and frontend pods carry a checksum of the Secret, so `helm upgrade` with a new key restarts them. PostgreSQL does not, because it reads `POSTGRES_PASSWORD` only when it creates an empty database.
- **Least privilege.** The backend runs as a non-root user with a read-only root filesystem and no Linux capabilities. The setup Job runs as the `postgres` user (uid 70). The two Caddy containers keep the image defaults because the `caddy` binary needs its `cap_net_bind_service` file capability.

## Values

The full list with defaults is in [`anti-fraud/values.yaml`](anti-fraud/values.yaml).

| Value | Default | Meaning |
|---|---|---|
| `images.tag` | `latest` | tag for the four project images. Use `sha-<commit>` from the `Images` workflow to run an exact commit |
| `images.pullSecrets` | `[]` | image pull secrets, needed only while the GHCR packages are private |
| `existingSecret` | empty | name of a Secret you created yourself. Empty means the chart creates `<release>-secrets` from `secrets.*` |
| `secrets.apiKey`, `secrets.postgresPassword`, `secrets.runtimeDbPassword` | empty | required when the chart creates the Secret |
| `secrets.labelsDbPassword`, `reviewAnalystKeys`, `operatorApiKeys`, `chargebackWebhookSecret` | empty | optional, the same meaning as in `.env` |
| `backend.modelFile` | `lgbm-game-v9.json` | model file inside the backend image, like `MODEL_FILE` |
| `backend.simulationEnabled` | `false` | needs `secrets.labelsDbPassword` |
| `backend.ipIntelProvider` | `off` | `mock` only for demos, like production |
| `backend.scoreRateLimit` and the other tuning values | empty | `SCORE_RATE_LIMIT`, `SCORE_RATE_BURST`, `SCORE_TIMEOUT`, `DB_MAX_CONNS`, `TRACING_ENDPOINT`, `TRACING_SAMPLE_RATIO`, `DOGSTATSD_ADDR`. Empty values are not set |
| `edge.consoleHost`, `edge.apiHost` | empty, required | `https://console.<ip>.sslip.io` and `https://api.<ip>.sslip.io` for real certificates, or `http://...` hosts for a test cluster |
| `edge.service.type` | `LoadBalancer` | k3s answers it on the node's ports 80 and 443. `ClusterIP` for tests |
| `edge.persistence.enabled` | `true` | keep certificates on a 1 GiB volume |
| `postgres.storage`, `postgres.storageClass` | `5Gi`, cluster default | database volume |

Database passwords go into connection URLs, so use letters and digits only (`openssl rand -hex 24`), as with `.env`.

## Install

The Secret should not live in a values file in Git. Create it from the server's `.env` style values, then point the chart at it:

```bash
kubectl create namespace anti-fraud
kubectl -n anti-fraud create secret generic anti-fraud-secrets \
  --from-literal=API_KEY="$API_KEY" \
  --from-literal=POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
  --from-literal=RUNTIME_DB_PASSWORD="$RUNTIME_DB_PASSWORD" \
  --from-literal=LABELS_DB_PASSWORD="$LABELS_DB_PASSWORD" \
  --from-literal=REVIEW_ANALYST_KEYS="$REVIEW_ANALYST_KEYS" \
  --from-literal=OPERATOR_API_KEYS="$OPERATOR_API_KEYS" \
  --from-literal=CHARGEBACK_WEBHOOK_SECRET="$CHARGEBACK_WEBHOOK_SECRET"
```

```bash
helm install anti-fraud deploy/helm/anti-fraud -n anti-fraud \
  --set existingSecret=anti-fraud-secrets \
  --set images.tag=sha-<commit> \
  --set edge.consoleHost=https://console.<ip>.sslip.io \
  --set edge.apiHost=https://api.<ip>.sslip.io \
  --set backend.scoreRateLimit=80 \
  --wait --timeout 10m
```

Then create the first admin account. The password is asked twice and never shown:

```bash
kubectl -n anti-fraud exec -it deploy/backend -c backend -- /create-account -username <name>
```

The backend image carries the `create-account` command for this, and the container already has `DATABASE_URL`.

## Upgrade, roll back, remove

```bash
helm upgrade anti-fraud deploy/helm/anti-fraud -n anti-fraud --reuse-values --set images.tag=sha-<new commit> --wait --timeout 10m
helm history anti-fraud -n anti-fraud
helm rollback anti-fraud <revision> -n anti-fraud --wait
```

- A rollback brings back the old pods but never undoes a migration. So far no migration removes or renames a table or column, so an older backend normally keeps working on the newer schema. Before rolling back across a migration, read it: a new `NOT NULL` column or a stricter check can reject the old code's writes.
- To change `RUNTIME_DB_PASSWORD`, run the upgrade twice. The setup hook runs before Helm updates the Secret, so the first upgrade still sets the old password on the role, and the second sets the new one. Until then the new backend pod fails its init container and the old one keeps serving.
- `helm uninstall` removes the pods and Services but keeps the volumes `data-db-0` and `edge-data`, so the data and certificates survive. Delete them with `kubectl delete pvc` only when you mean to lose them.

## Backups

The nightly `deploy/backup.sh` runs `pg_dump` inside the Compose `db` container. On Kubernetes the same dump is:

```bash
kubectl -n anti-fraud exec db-0 -- sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > anti-fraud-$(date -u +%F).dump
```

The EBS snapshot policy keeps working on a k3s server, because k3s stores volumes on the same disk.

## CI

`.github/workflows/helm.yml` runs on every pull request that touches the backend, the console, the Caddyfile or the chart:

1. `helm lint` and `helm template` with [`ci-values.yaml`](ci-values.yaml), and once more with production-like values (existing Secret, LoadBalancer, persistence, pull secret).
2. A render without secrets must fail, so the `required` checks stay in place.
3. `kubeconform -strict` checks every rendered object against the Kubernetes schemas.
4. A kind cluster builds and loads the four images, installs the chart, waits for the setup Job, and through the edge checks `/health`, `/ready`, a real `POST /score`, the console page, that the API host hides `/admin`, and a console sign-in with an account made by `create-account`.
5. `helm upgrade` with a changed value, which runs the setup hook first, then `helm rollback` to revision 1, each followed by `/ready`.

On failure the job prints the pods, events and the last 100 log lines of every container.

## Try it on your computer

With Docker Desktop's Kubernetes turned on and Helm installed, build the images with the `ci` tag, load them into the cluster node and install with the CI values:

```bash
docker build -t ghcr.io/nasirlinijat/anti-fraud-backend:ci backend
docker build -t ghcr.io/nasirlinijat/anti-fraud-dbsetup:ci -f backend/Dockerfile.dbsetup backend
docker build -t ghcr.io/nasirlinijat/anti-fraud-frontend:ci frontend
docker build -t ghcr.io/nasirlinijat/anti-fraud-edge:ci -f deploy/Dockerfile.edge deploy
for i in backend dbsetup frontend edge; do docker save ghcr.io/nasirlinijat/anti-fraud-$i:ci | docker exec -i desktop-control-plane ctr -n k8s.io images import -; done
for i in postgres:18-alpine redis:8-alpine; do docker pull $i && docker save --platform linux/amd64 $i | docker exec -i desktop-control-plane ctr -n k8s.io images import --platform linux/amd64 -; done
helm install af deploy/helm/anti-fraud -f deploy/helm/ci-values.yaml --set images.tag=ci -n af-test --create-namespace --wait
kubectl -n af-test port-forward svc/edge 8088:80
```

Then `curl -H "Host: api.localhost" http://127.0.0.1:8088/ready`. Remove it with `helm uninstall af -n af-test` and `kubectl delete namespace af-test`.
