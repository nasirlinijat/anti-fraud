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
REVIEW_ANALYST_KEYS=[{"analyst_id":"analyst-1","key":"$(openssl rand -hex 32)"},{"analyst_id":"analyst-2","key":"$(openssl rand -hex 32)"}]
OPERATOR_API_KEYS=[{"operator_id":"operator-1","key":"$(openssl rand -hex 32)"}]
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

`API_KEY` and the individual analyst and operator keys are in `.env`; share them through a password manager, never in Git or chat. `deploy/env.production.example` lists every setting.

After the first deploy, create the first admin account. The backend image has no tools, so a temporary Go container runs `create-account` on the server's network and asks for the password without showing it:

```bash
ssh -t -i ~/.ssh/anti-fraud.pem ubuntu@$IP 'cd ~/anti-fraud && set -a && . ./.env && set +a && docker run --rm -it --network antifraud_default -v ~/anti-fraud/app/backend:/src:ro -w /src -e DATABASE_URL="postgres://antifraud_api:${RUNTIME_DB_PASSWORD}@db:5432/${POSTGRES_DB}?sslmode=disable" golang:1.26.11-alpine go run ./cmd/create-account -username <name>'
```

That admin adds the other accounts in the console. Older servers may still have `CONSOLE_USER` and `CONSOLE_PASSWORD_HASH` in `.env`; they are no longer used.

## 3. Deploy

For an existing server, add a new `RUNTIME_DB_PASSWORD` and individual `OPERATOR_API_KEYS` to `~/anti-fraud/.env` before upgrading. Generate the runtime password with `openssl rand -hex 24` and each operator key with `openssl rand -hex 32`. Keep the existing PostgreSQL administrator credentials and volume. Compose applies migrations with the administrator, then provisions the restricted API role on the same database. Configuration writes return 503 until operator keys are configured.

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

## Choosing the model

`MODEL_FILE` in `~/anti-fraud/.env` picks the model file from `backend/models/`:

| Value | Model |
|---|---|
| `lgbm-game-v9.json` | Trained on the simulator's game traffic and six generator-written worlds, see `ml/README.md` (the default of the repository) |
| `lgbm-game-v5.json` | The previous default, trained on the simulator's game traffic only |
| `lgbm-ieee-v5-combined.json` | Trained on IEEE-CIS card data, the real-data benchmark |
| `lgbm-game-v2.json` | The earlier game model, trained on easier simulated traffic |
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
