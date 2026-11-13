# Anti-Fraud

A real-time fraud scoring and review console for in-game purchases. Each purchase is checked against a blocklist, weighted rules, and a LightGBM model, and gets one of three outcomes: `allow`, `review`, or `block`. Analysts decide the purchases marked for review in the console.

## Quick start

You need Docker with the Compose plugin, GNU Make, and Go 1.26.11 or newer. The demo setup also uses OpenSSL to generate local keys. Start Docker before running these commands from the repository root.

```bash
cp .env.example .env
make up
```

Compose applies the migrations and grants before starting the API and console. Create an admin account with a password of 12 to 72 bytes. This command reads the default database connection from the copied environment file:

```bash
(cd backend && DATABASE_URL="$(sed -n 's/^DATABASE_URL=//p' ../.env)" go run ./cmd/create-account -username alice)
```

Open the console at http://127.0.0.1:3000 and sign in. On **Tester**, submit the normal purchase example. Then open **Dashboard** to see its decision.

### Populate the demo

To enable seeding, set the following variables in your shell and apply them to the running stack. The generated analyst and operator keys are for this local session.

```bash
export SIMULATION_ENABLED=true
export SEED_ANALYST_KEY="$(openssl rand -hex 32)"
export RESET_OPERATOR_KEY="$(openssl rand -hex 32)"
export REVIEW_ANALYST_KEYS="$(printf '[{"analyst_id":"demo-seed","key":"%s"}]' "$SEED_ANALYST_KEY")"
export OPERATOR_API_KEYS="$(printf '[{"operator_id":"demo-seed","key":"%s"}]' "$RESET_OPERATOR_KEY")"
docker compose up -d --wait
make seed
```

`make seed` clears demonstration data in the running local stack, including purchases, reviews, blocklist entries, and simulation runs. Pause other ingestion and review work before running it. Console accounts and ruleset versions are retained. It then scores seven days of traffic with seed 42 and resolves all but the newest 10 reviews, if that many are available. Use **Dashboard → All time** for totals and **Review queue** for the remaining purchases. Run `make seed` again with the same model and ruleset to reproduce the outcome counts, blocked value, top rules, model effects, and queue size. Scoring latency and decision timestamps are measured on each run.

To stop the stack and keep its data, run `make down`. See [the clean-start checklist](.agent/docs/testing.md#clean-demo-start) for the timed walkthrough.

## Development

The Make targets need GNU Make. The frontend needs Node.js 26 or newer.

| Command | What it does |
|---|---|
| `make up` | Builds and starts the Docker stack, including migrations and the console |
| `make down` | Stops the containers and keeps the database data |
| `make run` | Starts PostgreSQL and Redis in Docker and runs the backend directly with Go on http://127.0.0.1:8080 |
| `make test` | Applies migrations, starts Redis in Docker, and runs the backend tests |
| `make test-integration` | Runs the integration and concurrency tests (needs Docker) |
| `make test-ml` | Runs the ML tests with `ml/.venv` |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make lint-frontend` | Runs the frontend lint, type check, and unit tests |
| `make migrate` | Applies all pending database migrations |
| `make docs` | Shows the API spec (`openapi.yaml`) in Swagger UI at http://localhost:8090 |
| `make seed` | Resets and populates the local demo with seven days of traffic and an open review queue |
| `make reset` | Resets demo data through the enabled simulation API; pause external traffic first |
| `make simulate` | Sends seeded demo purchases to the running backend |

To run the frontend with hot reload, start the backend first. Then run `npm ci` and `npm run dev` in `frontend/` and open http://localhost:5173.

## Documentation

| Topic | Where |
|---|---|
| API reference | [openapi.yaml](openapi.yaml) (`make docs`) |
| Production deployment | [deploy/README.md](deploy/README.md) |
| Model training | [ml/README.md](ml/README.md) |
| Load and chaos tests | [LOAD_TEST.md](LOAD_TEST.md), [CHAOS_TEST.md](CHAOS_TEST.md) |
| Threat model | [docs/threat-model.md](docs/threat-model.md) |
| Rules, simulator, accounts, operations, testing | [.agent/docs/](.agent/docs/README.md) |

## Troubleshooting

| Problem | Fix |
|---|---|
| `set POSTGRES_USER in .env`, or a similar message | Run `cp .env.example .env` |
| `set API_KEY in .env`, or `API_KEY is not set` | Add `API_KEY=local-dev-key` to your `.env`, as in `.env.example` |
| `make run` reports that the port is already in use | Run `make down` to stop the backend container started by `make up` |
| Port `3000`, `5432`, or `8080` is used by another program | Change `FRONTEND_PORT`, `POSTGRES_PORT`, or `PORT` in `.env`. For the host backend, also update `DATABASE_URL` to match `POSTGRES_PORT` |
| Compose startup fails | Check `docker compose ps -a` and `docker compose logs migrate backend frontend` |
| You want an empty database | Run `docker compose down -v`, which deletes the local database data, then `docker compose up -d --build --wait` |
