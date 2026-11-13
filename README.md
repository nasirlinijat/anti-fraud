# Anti-Fraud

A real-time fraud scoring and review console for in-game purchases. Each purchase is checked against a blocklist, weighted rules, and a LightGBM model, and gets one of three outcomes: `allow`, `review`, or `block`. Analysts decide the purchases marked for review in the console.

## Quick start

You need Docker with the Compose plugin, and Go 1.26.11 or newer to create the first account.

```bash
cp .env.example .env
docker compose up -d --build --wait
```

Compose applies the database migrations before it starts the backend. Next, create the first admin account. Run this from `backend` with `DATABASE_URL` from `.env` set. The command asks for a password of 12 to 72 bytes:

```bash
go run ./cmd/create-account -username alice
```

Open the console at http://127.0.0.1:3000 and sign in. On **Tester**, fill in the normal purchase example and submit it. Then open **Dashboard** to see the saved decision.

To stop the stack and keep the data, run `docker compose down`.

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
