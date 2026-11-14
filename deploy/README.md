# Deploy to AWS

The whole stack runs on one EC2 server with Docker Compose: PostgreSQL, the backend, the console and a Caddy gateway that serves HTTPS with free Let's Encrypt certificates.

```
Internet ──► Caddy gateway (80, 443)
               ├── api.<ip>.sslip.io      ──► backend   POST /score, GET /health, GET /ready only
               └── console.<ip>.sslip.io  ──► console   sign-in with a console account
                                               └── backend (the console adds the API key for signed-in users)
             PostgreSQL is reachable only inside the server
```

| Host | Who uses it | Protection |
|---|---|---|
| `api.<ip>.sslip.io` | Game shops, the simulator, `replay.py` | `X-API-Key` header with `API_KEY`. Only `/score`, `/health` and `/ready` are open |
| `console.<ip>.sslip.io` | The team and analysts | Each person signs in with their own console account (admin or reviewer). Every API call carries the session, and the Tester page's `/score` is forwarded only for a signed-in account |

[sslip.io](https://sslip.io) is a free DNS service: `api.3-120-10-20.sslip.io` resolves to `3.120.10.20`, so no domain is needed. With a real domain, point two DNS records at the server and put those names in `CONSOLE_HOST` and `API_HOST`.

## Cost

About 20 USD a month from the AWS credits: a `t3.small` instance (2 vCPU, 2 GiB), a 20 GiB disk and one public IPv4 address. Stop the instance when it is not needed for a while; the Elastic IP keeps the address.

## 1. Create the server (AWS console, once)

1. Region: **Europe (Frankfurt) eu-central-1**.
2. EC2, **Launch instance**:
   - Name `anti-fraud`, image **Ubuntu Server 24.04 LTS**, architecture 64-bit (x86), type **t3.small**.
   - Key pair: create one, type ED25519, format `.pem`, and save it as `~/.ssh/anti-fraud.pem`.
   - Network settings: allow SSH from **My IP**, and allow HTTP and HTTPS from the internet.
   - Storage: 20 GiB gp3.
3. EC2, **Elastic IPs**, allocate an address and associate it with the instance.
4. Billing, **Budgets**: a monthly cost budget of 20 USD with an email alert.

## 2. Prepare the server (once)

From the repository root on your computer (Git Bash on Windows), with `IP` set to the Elastic IP:

```bash
IP=3.120.10.20
ssh -i ~/.ssh/anti-fraud.pem ubuntu@$IP 'bash -s' < deploy/server-setup.sh
```

It installs Docker, adds 2 GiB of swap for the image builds, and creates `~/anti-fraud/`.

Then create the secrets on the server. They are generated there and never leave it:

```bash
ssh -i ~/.ssh/anti-fraud.pem ubuntu@$IP
```

```bash
cd ~/anti-fraud
DASHED=$(curl -s https://checkip.amazonaws.com | tr . -)
cat > .env <<EOF
PORT=8080
FRONTEND_PORT=3000
POSTGRES_PORT=5432
API_KEY=$(openssl rand -hex 32)
POSTGRES_USER=antifraud
POSTGRES_PASSWORD=$(openssl rand -hex 24)
RUNTIME_DB_PASSWORD=$(openssl rand -hex 24)
POSTGRES_DB=antifraud
MODEL_FILE=lgbm-game-v9.json
IP_INTEL_PROVIDER=off
CONSOLE_HOST=console.$DASHED.sslip.io
API_HOST=api.$DASHED.sslip.io
EOF
chmod 600 .env
```

`API_KEY` is in `.env`; keep it in a password manager, never in Git or chat. Console users sign in with their own accounts. Session tokens are short-lived and are not deployment secrets. `deploy/env.production.example` lists every setting.

After the first deploy, create the first admin account. The backend image has no tools, so a temporary Go container runs `create-account` on the server's network and asks for the password without showing it:

```bash
ssh -t -i ~/.ssh/anti-fraud.pem ubuntu@$IP 'cd ~/anti-fraud && set -a && . ./.env && set +a && docker run --rm -it --network antifraud_default -v ~/anti-fraud/app/backend:/src:ro -w /src -e DATABASE_URL="postgres://antifraud_api:${RUNTIME_DB_PASSWORD}@db:5432/${POSTGRES_DB}?sslmode=disable" golang:1.26.11-alpine go run ./cmd/create-account -username <name>'
```

That admin adds the other accounts in the console. Older servers may still have `CONSOLE_USER` and `CONSOLE_PASSWORD_HASH` in `.env`; they are no longer used.

## 3. Deploy


From the repository root on your computer:

```bash
bash deploy/deploy.sh ubuntu@$IP origin/main ~/.ssh/anti-fraud.pem
```

It copies the chosen commit (default `origin/main`) to the server, builds the images there, applies the migrations, starts everything and waits until it is healthy. The first run takes a few minutes. Run the same command after every merge to update the server; nothing else is needed. The deployed commit is written to `~/anti-fraud/app/DEPLOYED_COMMIT`.

Check it:

```bash
curl https://api.$DASHED.sslip.io/health
```

Open `https://console.<dashed-ip>.sslip.io` in the browser and log in as `team`.

Send demo traffic to the server (from `backend`):

```bash
go run ./cmd/simulator -url https://api.<dashed-ip>.sslip.io -api-key <API_KEY> -count 500 -rate 10
```

## Container images

The `Images` workflow (`.github/workflows/images.yml`) builds four images from their Dockerfiles. On a pull request it only builds them, so a broken Dockerfile fails the check. On every push to `main` it also publishes them to GitHub Container Registry:

| Image | Tags |
|---|---|
| `ghcr.io/nasirlinijat/anti-fraud-backend` | `sha-<short commit>` for every main commit, and `latest` for the newest |
| `ghcr.io/nasirlinijat/anti-fraud-frontend` | the same |
| `ghcr.io/nasirlinijat/anti-fraud-dbsetup` | the same. The migrate tool, the migrations and `runtime-role.sql`, used by the Helm chart to set up the database |
| `ghcr.io/nasirlinijat/anti-fraud-edge` | the same. Caddy with `deploy/Caddyfile`, the HTTPS gateway in the Helm chart |

Use the `sha-` tag to run an exact commit; `latest` moves with every merge. The images carry the repository as their source label, so they appear under the repository's **Packages**. The first publish creates each package; if a package is private, the repository owner makes it public in its **Package settings**, or a cluster pulls it with an image pull secret.

```bash
docker pull ghcr.io/nasirlinijat/anti-fraud-backend:latest
```

`deploy/deploy.sh` still builds the images on the server; these registry images are for Kubernetes and other machines that should run exactly what CI built. The Helm chart that runs them is described in [`helm/README.md`](helm/README.md).

## Choosing the model

`MODEL_FILE` in `~/anti-fraud/.env` picks the model file from `backend/models/`:

| Value | Model |
|---|---|
| `lgbm-game-v9.json` | Trained on the simulator's game traffic and six generator-written worlds, see `ml/README.md` (the default of the repository) |
| `lgbm-game-v5.json` | The previous default, trained on the simulator's game traffic only |
| `lgbm-ieee-v5-combined.json` | Trained on IEEE-CIS card data, the real-data benchmark |
| empty | Rules only |

After changing it, run `bash deploy/deploy.sh` again.

## Operating

Run these on the server in `~/anti-fraud/app`:

```bash
alias dc='docker compose -p antifraud --env-file ../.env -f docker-compose.yml -f deploy/compose.prod.yml'
dc ps
dc logs -f backend
dc restart backend
```

The database administrator can inspect recent configuration changes. Each row identifies the actor, action, target, and server time. Blocklist events also retain the entry details, so restrict access to the audit table and backups.

```bash
dc exec -T db psql -U antifraud -d antifraud -c   'SELECT recorded_at, actor_kind, actor_id, action, resource_id FROM configuration_audit_events ORDER BY id DESC LIMIT 25'
```

### Backups

There are two layers. Nightly database dumps protect against mistakes, such as a bad migration or deleted data. Disk snapshots in AWS protect against losing the server itself, because the dumps live on the same disk as the database.

**Database dumps.** Back up the database now:

```bash
bash ~/anti-fraud/app/deploy/backup.sh
```

It writes a compressed `pg_dump` to `~/anti-fraud/backups/` and keeps the last 7. The server runs it every night at 03:00 UTC from the `ubuntu` user's crontab (`crontab -e`), with the output in `backups/backup.log`:

```
0 3 * * * bash /home/ubuntu/anti-fraud/app/deploy/backup.sh >> /home/ubuntu/anti-fraud/backups/backup.log 2>&1
```

Check a dump without touching the live database by restoring it into a temporary database:

```bash
f=$(ls -1t ~/anti-fraud/backups/*.sql.gz | head -1)
dc exec -T db createdb -U antifraud restore_check
gunzip -c "$f" | dc exec -T db psql -q -U antifraud -d restore_check > /dev/null
dc exec -T db psql -U antifraud -d restore_check -c 'SELECT count(*) FROM purchases'
dc exec -T db dropdb -U antifraud restore_check
```

Restore a dump over the live database (this replaces all current data, so take a fresh backup first):

```bash
bash ~/anti-fraud/app/deploy/backup.sh
f=~/anti-fraud/backups/<the dump to restore>.sql.gz
dc stop backend
dc exec -T db dropdb -U antifraud --force antifraud
dc exec -T db createdb -U antifraud antifraud
gunzip -c "$f" | dc exec -T db psql -q -U antifraud -d antifraud > /dev/null
dc up -d --wait
```

`dc up` runs the migrations and `db-permissions` again, so the restricted roles get their rights back.

**Disk snapshots.** An Amazon Data Lifecycle Manager policy takes a snapshot of the server's disk every day and keeps the last 7. AWS stores snapshots outside the instance, so they survive a lost server. Create it once in the AWS console:

1. EC2, **Elastic Block Store, Lifecycle Manager**, **Create lifecycle policy**, type **EBS snapshot policy**.
2. Target resource type **Volume**, target tag `Backup` = `daily`. Add the same tag to the server's volume (EC2, **Volumes**, the instance's volume, **Tags**).
3. Schedule: every 24 hours at 03:30 UTC, after the nightly dump. Retention type **Count**, **7**.
4. Keep the default IAM role, enable the policy, and create it.

Snapshots store only the blocks that are in use and that changed, so for this disk they cost about 1 USD a month. To restore, create a volume from a snapshot (EC2, **Snapshots**, **Create volume from snapshot**, in the instance's availability zone), stop the instance, detach the old volume, attach the new one as the root device, and start the instance again.

Roll back to the previous deploy:

```bash
cd ~/anti-fraud && mv app failed && mv previous app && cd app && dc up -d --build --wait
```

Start from an empty database, for example before a replay or a demo:

```bash
dc down && docker volume rm antifraud_db-data && dc up -d --wait
```

## Monitoring with Datadog

The Datadog agent runs as one more container next to the others. It starts only when `DD_API_KEY` is set in `~/anti-fraud/.env`; without it, `deploy/deploy.sh` deploys the stack as before.

| Source | What reaches Datadog |
|---|---|
| Server | CPU, memory, disk, network, processes (host `anti-fraud-prod`) |
| Containers | CPU, memory and restarts of every container, and all their logs |
| Postgres | Connections, database size, and fraud metrics from the `decisions` table: decisions per minute by outcome, scoring latency p50/p95/p99, review queue size, average model score |
| Gateway | Requests per address, status codes and average response time (`antifraud.gateway.*`), and access logs without API keys, logins or cookies |
| Uptime | `http.can_connect` on the backend's `/ready`, which also checks the database |
| Scoring | Every saved decision as DogStatsD metrics (`antifraud.score.*`): outcomes, fired rules, points, model scores and latency by ruleset and model version |
| Traces | One APM trace per request with its database queries, model and rules, under the service `anti-fraud-backend` |

The checks are configured in `deploy/datadog/conf.d/`. The fraud metrics are SQL queries in `postgres.d/conf.yaml`, so they need no change to the backend.

### Turn it on

1. Create a Datadog account in the **EU1** region.
2. Add the API key to the server (from **Organization Settings, API Keys**). Paste it when asked:

   ```bash
   ssh -t -i ~/.ssh/anti-fraud.pem ubuntu@$IP 'read -rsp "Datadog API key: " k; echo; printf "DD_API_KEY=%s
DD_SITE=datadoghq.eu
" "$k" >> ~/anti-fraud/.env; echo saved'
   ```

3. Run `bash deploy/deploy.sh` again. The server appears under **Infrastructure** within a few minutes.

With `DD_API_KEY` set, `deploy/deploy.sh` uses `deploy/compose.datadog.yml` and `deploy/compose.tracing.yml`, and prints the compose files it uses. Tracing turns on the agent's APM and sends every request as a trace. To keep the agent but turn tracing off, for example when the APM trial ends, add `DD_APM_ENABLED=false` to `~/anti-fraud/.env` and deploy again: the agent's APM stays off and the backend gets no tracing endpoint.

### Dashboard and monitors

`deploy/datadog/dashboard.json` and `deploy/datadog/monitors.json` define the **Anti-fraud production** dashboard and five monitors: backend or database down, slow scoring, gateway 5xx errors, low memory, and a growing review queue. To create or update them, add an application key (from **Organization Settings, Application Keys**) as `DD_APP_KEY` in the same way, then run on the server:

```bash
bash ~/anti-fraud/app/deploy/datadog/setup.sh
```

Running it again updates the dashboard and monitors with the same names instead of creating copies. The slow scoring and gateway error monitors resolve when their metric stops reporting, because no traffic and no 5xx responses both send no data. Add a notification channel, for example `@your-email`, to the monitor messages in Datadog.

### Turn it off

Remove the `DD_` lines from `~/anti-fraud/.env` and run `bash deploy/deploy.sh` again; the agent container is removed.

## Kubernetes on the same server (k3s)

The same stack can run on [k3s](https://k3s.io), a small Kubernetes, with the Helm chart in [`helm/`](helm/README.md) and the images from GitHub Container Registry. Compose stays installed as the way back. Nothing in this section is needed while production runs on Compose.

| | Compose (today) | k3s |
|---|---|---|
| Deploy | `deploy/deploy.sh` builds on the server | `deploy/k8s-deploy.sh` pulls the `sha-` images CI built for a `main` commit |
| Update | containers are replaced, a short gap | rolling update, the new backend must pass `/ready` first; a failed upgrade rolls back by itself |
| Undo | deploy the previous commit again | `helm rollback` |
| Secrets | `~/anti-fraud/.env` | the same `.env`; the script turns it into the Secret `anti-fraud-secrets` |
| Backups | cron runs `backup.sh` | CronJob `db-backup`, same file format, keeps 7, on the volume `db-backups` |
| Datadog | agent container | official Datadog chart with [`helm/datadog-values.yaml`](helm/datadog-values.yaml); same host name, checks, dashboard and monitors |

Server size: k3s, the Datadog agent and the app need about 3 GiB, so the server moves from `t3.small` (2 GiB) to `t3.medium` (4 GiB), about 17 USD a month more.

### 1. Prepare (no downtime except the resize)

1. **Resize**, in the AWS console: EC2, select the instance, **Instance state, Stop**. When it is stopped, **Actions, Instance settings, Change instance type**, choose `t3.medium`, then **Instance state, Start**. The Elastic IP and the disk stay; Compose starts again by itself. Raise the AWS budget alert from 20 to 40 USD.
2. **Install k3s and Helm** on the server. Traefik is turned off because the chart's own Caddy answers on ports 80 and 443:

   ```bash
   ssh -i ~/.ssh/anti-fraud.pem ubuntu@$IP
   curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.37.2+k3s1 INSTALL_K3S_EXEC="--disable traefik" sh -
   mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown ubuntu: ~/.kube/config && chmod 600 ~/.kube/config
   curl -fsSL https://get.helm.sh/helm-v4.3.1-linux-amd64.tar.gz | tar -xz linux-amd64/helm && sudo install linux-amd64/helm /usr/local/bin/helm && rm -r linux-amd64
   kubectl get nodes
   ```

   k3s runs next to Docker. It takes ports 80 and 443 only when the chart's `edge` Service exists, which happens in step 2.

### 2. Move production (about 15 minutes of downtime)

Tell the team first. `$IP` is the Elastic IP; the commands marked *server* run in `~/anti-fraud/app` over SSH, the others on your computer in the repository.

0. Deploy the newest `main` with Compose as usual (`bash deploy/deploy.sh ubuntu@$IP origin/main ~/.ssh/anti-fraud.pem`), so the server has `deploy/row-counts.sql` and the old and new database are on the same migration.
1. *server*: stop traffic, count the rows, take the moving dump, then stop every Compose container (the Datadog agent too):

   ```bash
   alias dc='docker compose -p antifraud --env-file ../.env -f docker-compose.yml -f deploy/compose.prod.yml'
   dc stop edge backend
   dc exec -T db psql -X -A -F' ' -t -U antifraud -d antifraud < deploy/row-counts.sql > ~/anti-fraud/backups/counts-before.txt
   dc exec -T db pg_dump --clean --if-exists -U antifraud -d antifraud | gzip > ~/anti-fraud/backups/move.sql.gz
   docker stop $(docker ps -q --filter label=com.docker.compose.project=antifraud)
   crontab -l | grep -v backup.sh | crontab -
   ```

2. Deploy the newest `main` commit. The database is empty, so the setup Job creates the schema and roles:

   ```bash
   bash deploy/k8s-deploy.sh ubuntu@$IP origin/main ~/.ssh/anti-fraud.pem
   ```

3. *server*: stop the backend, load the dump into the new database, compare the counts. `diff` prints nothing when every table matches:

   ```bash
   kubectl -n anti-fraud scale deploy/backend --replicas=0
   gunzip -c ~/anti-fraud/backups/move.sql.gz | kubectl -n anti-fraud exec -i db-0 -c postgres -- sh -c 'psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
   kubectl -n anti-fraud exec -i db-0 -c postgres -- sh -c 'psql -X -A -F" " -t -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < deploy/row-counts.sql > ~/anti-fraud/backups/counts-after.txt
   diff ~/anti-fraud/backups/counts-before.txt ~/anti-fraud/backups/counts-after.txt && echo "all rows moved"
   ```

4. Deploy once more. The setup hook gives the restricted roles their rights on the restored tables and the backend starts again:

   ```bash
   bash deploy/k8s-deploy.sh ubuntu@$IP origin/main ~/.ssh/anti-fraud.pem
   ```

5. Check: the console signs in with your usual account, `curl https://api.$DASHED.sslip.io/ready` answers `ready`, the five Datadog monitors are OK, and a first backup works:

   ```bash
   kubectl -n anti-fraud create job --from=cronjob/db-backup first-backup
   kubectl -n anti-fraud wait --for=condition=complete job/first-backup --timeout 2m
   kubectl -n anti-fraud exec db-0 -c postgres -- ls -l /backups
   ```

Caddy asks Let's Encrypt for new certificates at the first start, which takes a few seconds. If Let's Encrypt refuses, Caddy uses ZeroSSL instead.

**Going back during the move.** Nothing on the Compose side was deleted, so:

```bash
helm -n anti-fraud uninstall anti-fraud datadog
docker start $(docker ps -aq --filter label=com.docker.compose.project=antifraud)
(crontab -l; echo '0 3 * * * bash /home/ubuntu/anti-fraud/app/deploy/backup.sh >> /home/ubuntu/anti-fraud/backups/backup.log 2>&1') | crontab -
```

Purchases scored on k3s after the move are not in the Compose database. To keep them, take a dump from `db-0` and restore it with the steps under [Backups](#backups) before starting the backend.

### Operating on k3s

| Task | Command |
|---|---|
| Deploy a merged commit | `bash deploy/k8s-deploy.sh ubuntu@$IP <commit or origin/main> ~/.ssh/anti-fraud.pem` |
| See what runs | *server*: `kubectl -n anti-fraud get pods` and `helm -n anti-fraud history anti-fraud` |
| Undo the last deploy | *server*: `helm -n anti-fraud rollback anti-fraud --wait` |
| Backend logs | *server*: `kubectl -n anti-fraud logs -f deploy/backend` |
| Change a setting or key | edit `~/anti-fraud/.env`, deploy again; changed keys restart the pods that use them |
| List backups | *server*: `kubectl -n anti-fraud exec db-0 -c postgres -- ls -l /backups` |
| Copy a backup to your computer | `ssh -i ~/.ssh/anti-fraud.pem ubuntu@$IP "kubectl -n anti-fraud exec db-0 -c postgres -- cat /backups/<file>" > <file>` |
| Create a console account | *server*: `kubectl -n anti-fraud exec -it deploy/backend -c backend -- /create-account -username <name>` |

`k8s-deploy.sh` deploys only commits on `main`, because only those have images. It reads `MODEL_FILE`, `SIMULATION_ENABLED`, `IP_INTEL_PROVIDER`, `SCORE_RATE_*`, `DB_MAX_CONNS`, `CONSOLE_HOST`, `API_HOST`, the `DD_` settings and every secret from `.env`. With `DD_API_KEY` set it also installs or updates the Datadog chart; `DD_APM_ENABLED=false` turns tracing off as before. Quoted values in `.env` work the same as with Compose: the quotes are not part of the value.

Restoring a nightly dump over the live database on k3s (take a fresh backup first; this replaces all data):

```bash
kubectl -n anti-fraud create job --from=cronjob/db-backup before-restore
kubectl -n anti-fraud scale deploy/backend --replicas=0
kubectl -n anti-fraud exec db-0 -c postgres -- sh -c 'dropdb -U "$POSTGRES_USER" --force "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'
kubectl -n anti-fraud exec db-0 -c postgres -- sh -c 'gunzip -c /backups/<file> | psql -X -q -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

Then deploy again from your computer; the setup hook restores the roles' rights and starts the backend.

The EBS snapshot policy keeps working: k3s keeps its volumes, including the database and `db-backups`, under `/var/lib/rancher/k3s/storage` on the same disk.

## Security notes

- PostgreSQL, the backend port and the console port listen only inside the server; only the gateway is public.
- The gateway rejects console requests from other sites, and the console's own checks still apply behind it.
- SSH is open only to the IP that created the instance. When your IP changes, edit the instance's security group.
- Secrets live only in `~/anti-fraud/.env` on the server.

## IP intelligence upgrade

The release that adds IP intelligence runs no lookup in production: `deploy/compose.prod.yml` sets `IP_INTEL_PROVIDER` to `off` unless `.env` sets it. Leave it off. The only provider is a mock that flags documentation addresses, so turning it on adds lookup work without useful results. Migration 000030 adds the nullable `decisions.ip_intel` column, which stays null while the provider is off. The `datacenter_ip` and `anonymous_ip` rules start off and never fire without a provider.

## Evaluation credential upgrade

Before enabling simulation on an existing server, add a separate random
`LABELS_DB_PASSWORD` to its private `.env`. Deployment provisions `antifraud_labels`
and the backend's private evaluation connection. Keep `SIMULATION_ENABLED=false`
when evaluation credentials are absent. Host simulator and import commands need
`LABELS_DATABASE_URL` for the same database; use private connectivity, not a public
PostgreSQL port. Existing scoring credentials remain unable to read label tables.

Before upgrading console authentication, verify an admin can sign in. Remove obsolete console key entries from environment files and existing deployment Secrets after verification. Preserve the service key, webhook secret, database credentials, and a private configuration backup for rollback.
