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

Set `FRONTEND_PORT` in `.env` to change the console port, then rerun the startup command. The console and the direct backend port listen on loopback. The proxy adds `API_KEY` to scoring, statistics, and configuration reads. Review requests and configuration writes use the individual key entered in the console.

## Deployment

The production setup runs on one AWS EC2 server with Docker Compose and HTTPS. See [deploy/README.md](deploy/README.md).

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
| `-missing` | 0.25 | Share of players whose client leaves out each group of optional fields: IP, device ID, device, account creation time, billing, card details, and email. A player misses the same groups on every purchase, whatever the scenario. Use 0 to send every field |
| `-run` | seed and start for backfill, unique for live | Prefix of all generated IDs |
| `-url` | `http://localhost:$PORT` | Base URL of the backend |
| `-dump` | none | Write the purchases with their scenario and fraud label to this JSON Lines file instead of sending them. Needs a positive count and no API key. Used to train the game model, see `ml/README.md` |

Backfill simulates event times, so a day of traffic can be sent in a few minutes. Every ID starts with the run prefix. Repeating a backfill with the same prefix, seed, start time, and settings returns saved decisions for matching purchases. Reusing an ID with different fields, including a different event time, returns 409. Use a new run prefix to send new purchases.

The scenario descriptions below are for a player whose client sends every field. A rule that needs a missing field does not fire.

Each special scenario is equally likely when an event starts one. Some scenarios generate several purchases, so the share of special purchases can differ from the share of special events.

| Pattern | What happens |
|---|---|
| `normal` | A regular player buys with their own card from their own country: mostly an ordinary amount, sometimes a larger bundle |
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
| `expat` | A returning player who lives abroad buys with a card from their home country |
| `new_big_spender` | A player with a new account makes a large legitimate purchase from home |
| `family_card` | A relative uses a regular player's card from the same home connection |
| `session_shopper` | A player with an older account makes two to four small purchases with their own card, minutes apart on the simulated timeline |
| `slow_card_testing` | Fraud tries three to five cards with one small purchase each from an older account in the cards' country, minutes apart on the simulated timeline, and can pass the rules |
| `quiet_takeover` | Fraud uses an established account from a new phone in the player's own country for a mid-sized amount, and can pass the rules |

The simulator keeps pattern names local and prints the score API's actual outcomes for every pattern, including rows with zero purchases. Decisions depend on the active ruleset, blocklist, model, and purchase history. For example, card testing can start with allowed purchases before the velocity rule contributes to a block. Compose runs with the model enabled, so a purchase that passes the rules can still be sent to review.

## Scoring load test

Run this from the repository root with a local Docker engine, Go, and curl:

```bash
./scripts/scorebench.sh
```

The script builds the API image and starts a disposable PostgreSQL 18 instance and API on loopback ports. It applies the migrations, loads 500,000 synthetic purchases and matching decisions spanning 120 days, and initializes all six feature-count groups. The API uses the restricted database role. A separate operator key publishes ruleset version 2 with the seeded settings plus device card count and new device spike enabled, preserving the model settings in assist mode. Scoring uses the service key and the Compose model, `lgbm-ieee-v5-combined`.

Seed 42 selects returning players, cards, and devices for 100 warm-up requests and 1,000 timed requests. The timed mix is 800 routine purchases, 100 on shared devices, and 100 large purchases on unseen devices. Across those groups, 100 requests have a missing issuer and 100 use a common issuer. The client sends one request at a time in event order and measures through the end of the HTTP response body. The script checks that the timed decisions were saved, verifies both new rules fired, and requires p95 below 50 ms. It removes the benchmark containers, database volume, and network at exit.

### Local results

Measured on November 11, 2026, on an Apple M4 with 10 CPU cores and 16 GiB RAM, macOS 26.6.2, Docker Engine 29.8.1 with 10 CPUs and 7.75 GiB allocated, and PostgreSQL 18.6. The baseline used `99d3398`. The covering-index change first reduced p95 to 40.76 ms and 39.21 ms on that same base. The final runs below include `main` at `35cc6c5`, migration `000020`, the restricted API database role, and operator authentication.

| Run | HTTP p50 | HTTP p95 | HTTP p99 |
|---|---:|---:|---:|
| Baseline | 19.05 ms | 94.13 ms | 121.46 ms |
| Final 1 | 7.25 ms | 38.37 ms | 40.57 ms |
| Final 2 | 8.27 ms | 41.39 ms | 66.62 ms |

Both final runs saved all 1,000 decisions. Each new rule fired 100 times per run. `pg_stat_statements` ranked issuer history first by mean and total execution time. Its mean was 28.09 ms in the baseline and 10.30 ms and 11.47 ms in the final runs. `EXPLAIN (ANALYZE, BUFFERS)` showed index-only scans for the typical, missing, and common issuer samples. The player, card, model history, device-card count, and trusted-purchase history plans used their indexes with no sequential scans. The issuer query still counts distinct values across both indexes. Rows with wide text use the index with the original keys.

## Ruleset API

Use `GET /rulesets/active` to read the current version, `GET /rulesets` to list published versions newest first with their note and creation time, and `GET /rulesets/{version}` to read a specific version. To change settings, send the full configuration (and an optional `note`) to `POST /rulesets`. Ruleset reads use the service key. Publishing requires your individual operator key in the `X-API-Key` header. Publishing returns the new version, which is used for subsequent scores. Earlier decisions keep their original version. A rule omitted from the configuration is disabled in the new version.

Device card count starts off. To enable it, publish a new ruleset with `device_card_count` enabled. The Rulesets page suggests 5 distinct cards in 60 minutes, including the current card, and a weight of 20 points. Before rolling back to a version of the service that predates this rule, publish a new ruleset that omits `device_card_count`.

New device spike also starts off. The Rulesets page suggests at least 3 trusted purchases in 90 days, a minimum amount of 1,000 minor units, a minimum of 3 times the trusted average, and a weight of 50 points. Trusted history includes allowed purchases and approvals recorded by the time of scoring. Before rolling back to a service version that predates this rule, publish a new ruleset that omits `new_device_spike` and keep the migration that stores its settings for historical versions.

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

The development server forwards same-origin API requests to the backend. It reads `PORT` and `API_KEY` from the repository's `.env` file and adds the service key to scoring, statistics, and configuration reads. It forwards individual analyst keys for reviews and individual operator keys for configuration writes. To use another backend, start the server with `BACKEND_URL=http://localhost:<port> npm run dev`.

| Command | What it does |
|---|---|
| `npm run dev` | Starts the development server with hot reload |
| `npm run check` | Runs the linter (oxlint), TypeScript type check, and proxy tests |
| `npm run build` | Type checks and builds the production files into `frontend/dist/` |

Browser checks and the XUI design decisions are documented in [frontend/docs/af-121-design.md](frontend/docs/af-121-design.md).

## Console pages

| Page | What it does |
|---|---|
| Dashboard (`#/dashboard`) | Shows counts and rates by outcome, p95 scoring latency, blocked value in minor units, and a stacked decision timeline with a table view. Choose the last 15 minutes, hour, day, week, or all time. The dashboard also shows the current review queue, most fired rules, and model effects. It reads `GET /stats` every 5 seconds. Send traffic with the Tester page or `make simulate` to fill it. |
| Blocklist (`#/blocklist`) | Lists the blocked card tokens, device IDs, player IDs and IP addresses with their reasons, adds new entries and removes them. A new entry applies to the next scored purchase. Invalid values, a missing reason and duplicates are shown next to their fields. |
| Tester (`#/tester`) | A form with every score request field. It sends the purchase to `POST /score` and shows the outcome, the rule score, the model score and the fired rules with their points. Validation errors from the API are shown next to their fields. The buttons above the form fill in a normal purchase, a suspicious one, or nothing. |
| Simulator (`#/simulator`) | Starts live or backfill simulation runs, follows decisions every three seconds and shows recent runs. Reads need no entered key; start and stop require an operator key. Runs continue across navigation, and history survives backend restarts. |
| Review queue (`#/reviews`) | Enter your analyst key to browse and decide purchases awaiting review, or open a case to inspect its evidence. The queue shows the highest rule score first and refreshes every three seconds. With a model loaded, you can sort by model risk. |
| Rulesets (`#/rulesets`) | Shows the active version and lets you edit decision thresholds, rule weights, and applicable limits. Publishing saves a new version with an optional note and shows its version number. Selecting an earlier version in the history shows its settings read-only. |

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

## Security

Configure `OPERATOR_API_KEYS` in `.env` to allow ruleset publishing and blocklist changes. Each operator needs a unique key distinct from the service and analyst keys. Generate keys with `openssl rand -hex 32` and enter your key in the Rulesets or Blocklist page.

```dotenv
OPERATOR_API_KEYS=[{"operator_id":"operator-1","key":"replace-with-an-individual-key"}]
```

When upgrading an existing Compose installation, add a separate `RUNTIME_DB_PASSWORD` to `.env` before starting the stack. Keep the existing `POSTGRES_USER` and `POSTGRES_PASSWORD` for migrations and backups. If you run the backend on the host, update `DATABASE_URL` to use `antifraud_api` and the runtime password.

The [STRIDE threat model](docs/threat-model.md) describes the local deployment's trust boundaries, controls, and remaining requirements for shared access.

## Database Queries

The backend runs SQL directly with [pgx](https://github.com/jackc/pgx). Each feature package keeps its queries as string constants next to the store code that runs them, with numbered parameters (`$1`, `$2`, ...):

```go
const getActiveRuleset = `
SELECT version, review_threshold, block_threshold
FROM rulesets
WHERE is_active`

err := conn.QueryRow(ctx, getActiveRuleset).Scan(&rs.Version, &rs.ReviewThreshold, &rs.BlockThreshold)
```

Pass nil pointers for NULL values and scan nullable columns into pointers. A query on a new table needs its migration first. A query on the scoring or dashboard path should be added to the index check in `backend/internal/queryplans/`, which fails when PostgreSQL would answer it with a sequential scan.

## Troubleshooting

| Problem | Fix |
|---|---|
| `set POSTGRES_USER in .env`, or a similar message | Run `cp .env.example .env` |
| `set API_KEY in .env`, or `API_KEY is not set` | Add `API_KEY=local-dev-key` to your `.env`, as in `.env.example` |
| `make run` reports that the port is already in use | Run `make down` to stop the backend container started by `make up` |
| Port `3000`, `5432`, or `8080` is used by another program | Change `FRONTEND_PORT`, `POSTGRES_PORT`, or `PORT` in `.env`. For the host backend, also update `DATABASE_URL` to match `POSTGRES_PORT` |
| Compose startup fails | Check `docker compose ps -a` and `docker compose logs migrate backend frontend` |
| You want an empty database | Run `docker compose down -v`, which deletes the local database data, then `docker compose up -d --build --wait` |

## Console simulation API

Set `SIMULATION_ENABLED=true` and configure an individual operator key to enable
console-managed traffic. Restart the backend after changing the setting. It defaults
to false, including production. These routes are available through the console host;
the public production API hostname does not expose them.

`POST /simulations` uses an operator key and accepts, for example:

```json
{"request_id":"backfill-42-first","mode":"backfill","seed":42,"rate":5,"count":500,"scenarios":["normal","card_testing"]}
```

It returns 202 with the run ID and a Location header. For live traffic use
`mode: "live"` and omit count. Poll `GET /simulations/{id}` with the service key,
or `GET /simulations?limit=20` for history; pass its `next_cursor` as `before`
for the next page. `POST /simulations/{id}/stop` uses an operator key and drains
the current purchase. Stopping an already finished run is safe.

Matching start retries with the same operator and request_id return the original
run with 200, including after completion. Changed settings or another active run
return 409. Rates are 1–50 purchases/second; backfills allow up to 10,000 purchases
and default to 500. Seeds are nonnegative JavaScript-safe integers. Omitted
scenarios select all patterns. Normal plus special scenarios keeps the existing
10 percent special-event share; without normal, every event starts one of the
selected special scenarios. Bursts can produce unequal purchase shares.

History and counters are saved in PostgreSQL. `sent` counts saved decisions and
is the sum of allowed, reviewed and blocked. The worker records an in-flight
transaction before scoring and checkpoints its result afterwards. On recovery it
reconciles that transaction against saved decisions and marks unfinished runs
`interrupted`; it does not automatically continue their traffic. `pending: true`
means an outcome still cannot be confirmed. A new run needs a new request_id.
Database failures stop dispatch until recovery. Only console-managed runs share
the active-run guard; separately launched CLI simulators are not controlled.

A seed determines traffic given the same generator settings and time anchor.
Actual outcomes also depend on existing history, blocklists, rules and model.
Backfill should use a clean demonstration database when comparing results.
This API does not reset data or expose fraud labels; the controller persists evaluation answers privately.

## Private evaluation labels

Scored simulator runs now require `LABELS_DATABASE_URL` pointing to the **same
PostgreSQL database as the score API**, using the separate `antifraud_labels`
role. Set `LABELS_DB_PASSWORD` in `.env` and rerun `make migrate` to provision it.
Compose supplies the container connection; host CLI commands use the connection
in `.env.example`. Scoring-only installations can leave evaluation credentials
unset while `SIMULATION_ENABLED=false`. Never use the database owner for ingestion.

Both CLI and console simulations store private pending metadata before sending a
purchase, then its label after the decision is saved. Console label writes and
progress checkpoints commit together. A persistence failure stops traffic and
leaves pending metadata recoverable; score requests never contain scenario or
fraud answers. Dump-only runs need neither a database nor an API key.

After a crash, recover saved pending decisions without resending traffic (from
`backend`, with `LABELS_DATABASE_URL` set):

```bash
go run ./cmd/import-labels -run my-run -recover
```

Load the IEEE-CIS replay's existing labels file after replay has saved decisions:

```bash
go run ./cmd/import-labels -run ieee-replay-1 -source ieee-cis -file ../ml/output/replay/labels.csv
```

The CSV has `transaction_id,is_fraud`, where the flag is exactly 0 or 1. For
simulator replays, use `-source simulator` and the additional `scenario` column
now written by `replay.py --requests`. Repeating the same import is safe;
contradictory answers, different run assignments, duplicate file IDs, malformed
rows and unscored transactions fail the complete import without partial writes.
Keep run IDs stable for retries. Old console runs have no recoverable answer key
and remain unlabelled. No historical answers are guessed.

Raw and replay-derived label files stay in ignored `ml/data/` or `ml/output/` and
must never be committed. The public API and model inputs do not expose labels.

Pending simulator answers now include a private purchase snapshot, normalized to
scoring's string, IP and timestamp conventions. Recovery requires the saved
purchase to match that snapshot, not just its ID. Definitive HTTP rejections and
snapshot mismatches are quarantined. Legacy intents without snapshots are not
automatically labelled; verify their source before any explicit CSV import.


## Run decision quality

The Dashboard's Decision quality panel reports saved scoring outcomes against
finalized known answers. Choose **Simulator** for console, CLI or imported
simulator runs, or **IEEE-CIS replay** for imported dataset runs, then select a
run. The two sources remain separate even when they use the same run ID. The
panel refreshes every five seconds independently of the dashboard period.

Review and block count as flagged. Precision is the fraudulent share of flags,
recall is the share of fraud flagged, and false-positive rate is the flagged
share of legitimate purchases. A missing denominator is shown as Not applicable.
Fraud value flagged includes reviews; it does not mean money recovered or saved.
Analyst review decisions do not change these scoring-quality measurements.

`GET /stats/quality?source=simulator&run_id=<id>` returns the aggregate report.
`source=ieee-cis` selects replay labels. `GET /stats/quality/runs?source=simulator`
lists run IDs, with a bounded `limit` and exclusive `after` cursor for more.
Both use the service key and require a configured `LABELS_DATABASE_URL` with the
restricted evaluation role. Quality reads work with `SIMULATION_ENABLED=false`;
missing or unavailable evaluation access returns 503 without disabling scoring.
The console proxy forwards these routes; the public production API host does not.

Only finalized answers contribute to metrics. Pending and quarantined intents
are counted separately and never treated as legitimate purchases. Console runs
show their recorded progress; CLI and replay runs have unknown total coverage
because ingestion does not store an expected run size. No historical answers
are guessed. Labels finalized before purchase verification was introduced need
independent source verification; use fresh runs for acceptance comparisons.
