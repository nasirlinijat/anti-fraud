# Deploy to AWS

The whole stack runs on one EC2 server with Docker Compose: PostgreSQL, the backend, the console and a Caddy gateway that serves HTTPS with free Let's Encrypt certificates.

```
Internet ──► Caddy gateway (80, 443)
               ├── api.<ip>.sslip.io      ──► backend   POST /score, GET /health, GET /ready only
               └── console.<ip>.sslip.io  ──► console   behind a team login
                                               └── backend (the console adds the API key)
             PostgreSQL is reachable only inside the server
```

| Host | Who uses it | Protection |
|---|---|---|
| `api.<ip>.sslip.io` | Game shops, the simulator, `replay.py` | `X-API-Key` header with `API_KEY`. Only `/score`, `/health` and `/ready` are open |
| `console.<ip>.sslip.io` | The team and analysts | Browser login (`CONSOLE_USER` and its password), plus individual keys for analyst decisions and operator configuration writes |

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
CONSOLE_PASSWORD=$(openssl rand -base64 18)
HASH=$(docker run --rm caddy:2.11-alpine caddy hash-password --plaintext "$CONSOLE_PASSWORD")
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
MODEL_FILE=lgbm-ieee-v5-combined.json
CONSOLE_HOST=console.$DASHED.sslip.io
API_HOST=api.$DASHED.sslip.io
CONSOLE_USER=team
CONSOLE_PASSWORD_HASH='$HASH'
EOF
chmod 600 .env
echo "$CONSOLE_PASSWORD" > console-password.txt
chmod 600 console-password.txt
```

`console-password.txt` holds the console login for the team. `API_KEY` and the individual analyst and operator keys are in `.env`; share them through a password manager, never in Git or chat. `deploy/env.production.example` lists every setting.

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
| `lgbm-ieee-v5-combined.json` | Trained on IEEE-CIS card data (the default of the repository) |
| `lgbm-game-v2.json` | Trained on the simulator's game traffic, see `ml/README.md` |
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

Back up the database:

```bash
bash ~/anti-fraud/app/deploy/backup.sh
```

It writes a compressed `pg_dump` to `~/anti-fraud/backups/` and keeps the last 7. To run it every night at 03:00 UTC, add this line with `crontab -e`:

```
0 3 * * * bash ~/anti-fraud/app/deploy/backup.sh > /dev/null
```

Roll back to the previous deploy:

```bash
cd ~/anti-fraud && mv app failed && mv previous app && cd app && dc up -d --build --wait
```

Start from an empty database, for example before a replay or a demo:

```bash
dc down && docker volume rm antifraud_db-data && dc up -d --wait
```

## Security notes

- PostgreSQL, the backend port and the console port listen only inside the server; only the gateway is public.
- The gateway rejects console requests from other sites, and the console's own checks still apply behind it.
- SSH is open only to the IP that created the instance. When your IP changes, edit the instance's security group.
- Secrets live only in `~/anti-fraud/.env` on the server.
