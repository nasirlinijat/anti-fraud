# Anti-Fraud

A real-time fraud scoring and review console for in-game purchases.

## Docker setup

From the repository root, use Docker with the Compose plugin to run the console.

1. Create the local environment file:

   ```bash
   cp .env.example .env
   ```

2. Build and start the stack. Compose applies pending database migrations before starting the backend:

   ```bash
   docker compose up -d --build --wait
   ```

3. Open [the console](http://127.0.0.1:3000). Select **Tester**, fill in the normal purchase example, and submit it to see the score. Open **Dashboard** to see the saved decision. Configure an individual analyst key to use **Review queue**.

4. Stop the stack and keep the database data:

   ```bash
   docker compose down
   ```

Set `FRONTEND_PORT` in `.env` to change the console port, then rerun the startup command. The console and the direct backend port listen on loopback. The proxy adds `API_KEY` to score, stats, blocklist, and ruleset requests. Review requests use the analyst key entered in the console.

## Development commands

The Make targets require GNU Make. Install Go 1.26.5 or newer to run the backend, tests, or simulator on the host.

To develop the backend on the host, run `make migrate` to prepare the database, then `make run`. The API is available at `http://127.0.0.1:8080` by default.

| Command | What it does |
|---|---|
| `make up` | Builds and starts the Docker stack, including migrations and the console |
| `make down` | Stops the containers and keeps the database data |
| `make run` | Starts PostgreSQL in Docker and runs the backend directly with Go |
| `make test` | Starts PostgreSQL in Docker, applies migrations, and runs the tests |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make migrate` | Applies all pending database migrations |
| `make generate` | Regenerates the database code from the SQL queries |
| `make docs` | Shows the API spec in Swagger UI at http://localhost:8090 |
| `make simulate` | Sends seeded demo purchases to the running backend (see below) |

## Demo Traffic

The simulator sends generated purchases to `POST /score` in the same format as real clients. With Go and GNU Make installed and the backend running, run:

```bash
make simulate
```

It sends 500 purchases, 20 per second, and prints how many of each pattern were allowed, reviewed, or blocked. It reads `API_KEY` and `PORT` from `.env`. Pass options with `ARGS`, for example:

```bash
make simulate ARGS="-seed 7 -count 2000 -rate 50 -suspicious 0.2"
```

For a live demo, open the dashboard's **15 minutes** view and run:

```bash
make simulate ARGS="-count 0 -rate 5 -seed 1"
```

Live mode timestamps each purchase as it is sent. Press Ctrl+C to stop and see the totals by pattern. The seed controls the purchase sequence, while timestamps and the default run prefix vary by launch. Changing the send rate can change which time-based rules fire.

If you need the simulator's exit status after Ctrl+C, build and run it directly from `backend` with `API_KEY` and `PORT` set. The binary exits successfully on interrupt if there were no send failures. Make and `go run` can report an interrupt after printing the summary.

```bash
go build -o ./bin/simulator ./cmd/simulator
./bin/simulator -count 0 -rate 5 -seed 1
```

Without `make`, for example in Windows PowerShell, run it from `backend` with the same values as `.env`:

```powershell
$env:API_KEY="local-dev-key"; $env:PORT="8080"; go run ./cmd/simulator -count 1000 -rate 50
```

| Option | Default | Meaning |
|---|---|---|
| `-seed` | 1 | Random seed for the purchase sequence |
| `-start` | the last backfill purchase happens now | Backfill start in RFC 3339. The score API rejects event times more than 5 minutes in the future. Use only with a positive count |
| `-count` | 500 | Number of purchases to send. Use 0 for live mode until Ctrl+C |
| `-rate` | 20 | Purchases sent per second, 0 for as fast as possible |
| `-suspicious` | 0.1 | Share of events that start a special scenario, including legitimate edge cases and fraud. Use 0 for normal purchases only or 1 for special scenarios only |
| `-players` | 300 | Number of regular players |
| `-gap` | 20s | Mean gap on the simulated timeline used to order purchases. The send rate controls live pacing |
| `-run` | seed and start for backfill, unique for live | Prefix of all generated IDs |
| `-url` | `http://localhost:$PORT` | Base URL of the backend |

Backfill simulates event times, so a day of traffic can be sent in a few minutes. Every ID starts with the run prefix. Reusing a run prefix with the same seed and settings can return 409 for transaction IDs that were already scored.

Each special scenario is equally likely when an event starts one. Some scenarios generate several purchases, so the share of special purchases can differ from the share of special events.

| Pattern | What happens |
|---|---|
| `normal` | A regular player buys with their own card from their own country |
| `card_testing` | A new account tries 7 to 9 small purchases on one card from another country |
| `account_takeover` | An established account is used for a large purchase from another country and a new device |
| `new_account_spend` | An account created a few hours ago buys for a large amount |
| `traveler` | A player uses their usual card and device while abroad |
| `new_phone` | A player uses their usual card from home on a new phone |
| `internet_cafe` | Two or three players use their own cards on the same public computer and connection |
| `burst_buyer` | A player makes four to seven small purchases in quick succession |
| `new_casual_player` | A player with a new account makes a small purchase from home |
| `returning_player` | A player who was inactive before the run returns for an ordinary purchase |
| `stolen_card` | A different player makes one purchase with a regular player's card |
| `low_and_slow` | Fraud looks like one ordinary purchase from an older account and can pass the rules |

The simulator keeps pattern names local and prints the score API's actual outcomes for every pattern, including rows with zero purchases. Decisions depend on the active ruleset, blocklist, model, and purchase history. For example, card testing can start with allowed purchases before the velocity rule contributes to a block. Compose runs with the model enabled, so a purchase that passes the rules can still be sent to review.

## Ruleset API

Use `GET /rulesets/active` to read the current version. To change settings, send the full configuration without its `version` field to `POST /rulesets`. Both endpoints require the `X-API-Key` header. Publishing returns the new version, which is used for subsequent scores. Earlier decisions keep their original version. A rule omitted from the configuration is disabled in the new version.

## API Documentation

The scoring, dashboard, blocklist, review queue, and ruleset APIs are described in `openapi.yaml`. It documents their requests, responses, and credentials.

To read it in Swagger UI, run:

```bash
make docs
```

Then open http://localhost:8090 and stop the server with Ctrl+C. To use another port, set `SWAGGER_PORT` in `.env`. Swagger UI only shows the API. Its "Try it out" button cannot call the backend from the browser, because the backend does not allow cross-origin requests. Use `curl` as in the setup steps instead.

When a documented API request or response changes, update `openapi.yaml` in the same pull request.

## Frontend development

The frontend is a React and TypeScript app in `frontend/`, built with [Vite](https://vite.dev). Install Node.js 26 or newer with npm for local development.

1. Start the API and its dependencies with `docker compose up -d --build backend`, or use the host backend workflow above.
2. Install the dependencies once:

   ```bash
   cd frontend
   npm ci
   ```

3. Start the development server:

   ```bash
   npm run dev
   ```

4. Open http://localhost:5173. The top bar shows whether the backend is ready.

The development server forwards API requests to the backend, so the browser needs no CORS setup. It reads `PORT` and `API_KEY` from the repository's `.env` file and adds the key to score, stats, blocklist, and ruleset requests. For review requests, it forwards the analyst key entered on the Review queue page. To use another backend, start the server with `BACKEND_URL=http://localhost:<port> npm run dev`.

| Command | What it does |
|---|---|
| `npm run dev` | Starts the development server with hot reload |
| `npm run check` | Runs the linter (oxlint) and the TypeScript type check |
| `npm run build` | Type checks and builds the production files into `frontend/dist/` |

## Console pages

| Page | What it does |
|---|---|
| Dashboard (`#/dashboard`) | Shows counts and rates by outcome, p95 scoring latency, blocked value in minor units, and a stacked decision timeline with a table view. Choose the last 15 minutes, hour, day, week, or all time. The dashboard also shows the current review queue, most fired rules, and model effects. It reads `GET /stats` every 5 seconds. Send traffic with the Tester page or `make simulate` to fill it. |
| Blocklist (`#/blocklist`) | Lists the blocked card tokens, device IDs, player IDs and IP addresses with their reasons, adds new entries and removes them. A new entry applies to the next scored purchase. Invalid values, a missing reason and duplicates are shown next to their fields. |
| Tester (`#/tester`) | A form with every score request field. It sends the purchase to `POST /score` and shows the outcome, the rule score, the model score and the fired rules with their points. Validation errors from the API are shown next to their fields. The buttons above the form fill in a normal purchase, a suspicious one, or nothing. |
| Review queue (`#/reviews`) | Enter your analyst key to browse and approve or reject purchases awaiting review. The current page refreshes every five seconds. |
| Rulesets (`#/rulesets`) | Shows the active version and lets you edit decision thresholds, rule weights, and applicable limits. Publishing saves a new version and shows its version number. |

### Review access

Generate a distinct key for each analyst with Docker:

```bash
docker run --rm node:26-bookworm-slim node -e "console.log(require('node:crypto').randomBytes(32).toString('hex'))"
```

Replace the placeholders in `REVIEW_ANALYST_KEYS` in your local `.env` and give each analyst their own key.

```dotenv
REVIEW_ANALYST_KEYS=[{"analyst_id":"analyst-one","key":"<generated-key-one>"},{"analyst_id":"analyst-two","key":"<generated-key-two>"}]
```

Run `docker compose up -d --wait` to apply the updated environment, or restart the host backend if you use `make run`. Enter the individual key on **Review queue**. Keep the configured keys out of Git. Each analyst key must be distinct from `API_KEY`. Leaving `REVIEW_ANALYST_KEYS` empty disables review access while the other APIs remain available.

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
| Port `3000`, `5432`, or `8080` is used by another program | Change `FRONTEND_PORT`, `POSTGRES_PORT`, or `PORT` in `.env`. For the host backend, also update `DATABASE_URL` to match `POSTGRES_PORT` |
| Compose startup fails | Check `docker compose ps -a` and `docker compose logs migrate backend frontend` |
| You want an empty database | Run `docker compose down -v`, which deletes the local database data, then `docker compose up -d --build --wait` |
