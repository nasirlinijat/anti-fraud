# Anti-Fraud

A real-time fraud scoring and review console for in-game purchases.

## Prerequisites

| Tool | Version |
|---|---|
| Git | Any recent version |
| Docker with the Compose plugin | Docker Engine running, Compose v2 |
| GNU Make | Any recent version |
| Go | 1.26.5 or newer |
| Node.js | 26 or newer, with npm |

Go is needed for `make run`, `make test`, and `make lint`. Node.js is needed for the frontend. The other commands run in Docker.

## Setup

1. Clone the repository:

   ```bash
   git clone https://github.com/nasirlinijat/anti-fraud.git
   cd anti-fraud
   ```

2. Create your local environment file. The default values work as they are:

   ```bash
   cp .env.example .env
   ```

3. Start PostgreSQL and the backend:

   ```bash
   make up
   ```

4. Apply the database migrations:

   ```bash
   make migrate
   ```

5. Check that the backend can reach the database:

   ```bash
   curl http://localhost:8080/ready
   ```

   The response should be `{"status":"ready"}`.

6. Score a purchase. The API key is `API_KEY` from `.env`:

   ```bash
   curl -X POST http://localhost:8080/score \
     -H "X-API-Key: local-dev-key" \
     -d '{"transaction_id":"demo-1","event_time":"2026-11-08T10:00:00Z","amount_minor":1999,"player_id":"player-1","card_token":"card-1"}'
   ```

   The response contains the outcome (`allow`, `review`, or `block`), the rule score, and the rules that fired.

7. Stop everything when you are done. Your database data is kept:

   ```bash
   make down
   ```

## Commands

| Command | What it does |
|---|---|
| `make up` | Builds and starts PostgreSQL and the backend in Docker |
| `make down` | Stops the containers and keeps the database data |
| `make run` | Starts PostgreSQL in Docker and runs the backend directly with Go |
| `make test` | Starts PostgreSQL in Docker, applies migrations, and runs the tests |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make migrate` | Applies all pending database migrations |
| `make generate` | Regenerates the database code from the SQL queries |
| `make docs` | Shows the API spec in Swagger UI at http://localhost:8090 |
| `make simulate` | Sends seeded demo purchases to the running backend (see below) |

## Demo Traffic

The simulator sends generated purchases to `POST /score` in the same format as real clients. Start the backend with `make up` or `make run`, apply the migrations, then run:

```bash
make simulate
```

It sends 500 purchases, 20 per second, and prints how many of each pattern were allowed, reviewed, or blocked. It reads `API_KEY` and `PORT` from `.env`. Pass options with `ARGS`, for example:

```bash
make simulate ARGS="-seed 7 -count 2000 -rate 50 -suspicious 0.2"
```

Without `make`, for example in Windows PowerShell, run it from `backend` with the same values as `.env`:

```powershell
$env:API_KEY="local-dev-key"; $env:PORT="8080"; go run ./cmd/simulator -count 1000 -rate 50
```

| Option | Default | Meaning |
|---|---|---|
| `-seed` | 1 | Random seed. The same seed and start give the same purchases |
| `-start` | now | Event time of the first purchase, in RFC 3339 |
| `-count` | 500 | Number of purchases to send |
| `-rate` | 20 | Purchases sent per second, 0 for as fast as possible |
| `-suspicious` | 0.1 | Share of traffic events that start a suspicious pattern |
| `-players` | 300 | Number of regular players |
| `-gap` | 20s | Mean simulated time between traffic events |
| `-run` | built from seed and start | Prefix of all generated IDs |
| `-url` | `http://localhost:$PORT` | Base URL of the backend |

Event times are simulated, so a day of traffic can be sent in a few minutes. Every ID starts with the run prefix, so a new run does not clash with earlier ones. Sending the same run again fails with 409, because those transaction IDs are already scored.

With the default ruleset the patterns give these outcomes:

| Pattern | What happens | Outcome |
|---|---|---|
| `normal` | A regular player buys with their own card from their own country | allow |
| `card_testing` | A new account tries 7 to 9 small purchases on one card from another country | allow, then block from the sixth purchase (velocity) |
| `account_takeover` | An old account buys for a large amount from another country and a new device | review |
| `new_account_spend` | An account created a few hours ago buys for a large amount | review |

The pattern is never sent to the API. It is only used for the summary.

## API Documentation

The score and review queue APIs are described in `openapi.yaml`. It documents their requests, responses, and shared API key header.

To read it in Swagger UI, run:

```bash
make docs
```

Then open http://localhost:8090 and stop the server with Ctrl+C. To use another port, set `SWAGGER_PORT` in `.env`. Swagger UI only shows the API. Its "Try it out" button cannot call the backend from the browser, because the backend does not allow cross-origin requests. Use `curl` as in the setup steps instead.

When a documented API request or response changes, update `openapi.yaml` in the same pull request.

## Frontend

The frontend is a React and TypeScript app in `frontend/`, built with [Vite](https://vite.dev).

1. Start the backend with `make up` or `make run`.
2. Install the dependencies once:

   ```bash
   cd frontend
   npm install
   ```

3. Start the development server:

   ```bash
   npm run dev
   ```

4. Open http://localhost:5173. The top bar shows whether the backend is ready.

The development server forwards API requests to the backend, so the browser needs no CORS setup. It reads `PORT` and `API_KEY` from the repository's `.env` file and adds the key to the tester's `POST /score` requests. For `GET /reviews`, it forwards the key entered on the Review queue page. To use another backend, start the server with `BACKEND_URL=http://localhost:<port> npm run dev`.

| Page | What it does |
|---|---|
| Tester (`#/tester`) | A form with every score request field. It sends the purchase to `POST /score` and shows the outcome, the rule score, the model score and the fired rules with their points. Validation errors from the API are shown next to their fields. The buttons above the form fill in a normal purchase, a suspicious one, or nothing. |
| Review queue (`#/reviews`) | Enter the local `API_KEY` to browse purchases awaiting review, oldest queued first. The current page refreshes every five seconds. |

The tester is a local development tool: it only works through the development server, which holds the API key.

| Command | What it does |
|---|---|
| `npm run dev` | Starts the development server with hot reload |
| `npm run check` | Runs the linter (oxlint) and the TypeScript type check |
| `npm run build` | Type checks and builds the production files into `frontend/dist/` |

## Database Queries

Database code is generated from SQL files with [sqlc](https://sqlc.dev). Do not edit the generated files in `backend/internal/db/` by hand.

To add a query:

1. Write it in a `.sql` file in `backend/queries/`, with a name line above it:

   ```sql
   -- name: GetActiveRuleset :one
   SELECT version, review_threshold, block_threshold
   FROM rulesets
   WHERE is_active;
   ```

   `:one` returns one row, `:many` returns a list, and `:exec` returns nothing.

2. Run `make generate`.
3. Commit the `.sql` file together with the generated code.

sqlc reads the table definitions from `backend/migrations/`, so a query on a new table needs its migration first.

## Troubleshooting

| Problem | Fix |
|---|---|
| `set POSTGRES_USER in .env`, or a similar message | Run `cp .env.example .env` |
| `set API_KEY in .env`, or `API_KEY is not set` | Add `API_KEY=local-dev-key` to your `.env`, as in `.env.example` |
| `make run` reports that the port is already in use | Run `make down` to stop the backend container started by `make up` |
| Port `5432` or `8080` is used by another program | Change `POSTGRES_PORT` or `PORT` in `.env`, and update the port in `DATABASE_URL` to match `POSTGRES_PORT` |
| You want an empty database | Run `docker compose down -v`, which deletes the local database data, then `make up` and `make migrate` |
