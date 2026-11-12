# AGENTS.md

This file provides guidance to AI coding assistants working in this repository.

## 1. Project Overview

Anti-Fraud is a real-time fraud scoring and review console for in-game purchases. It is a simplified version of the rule, review, and blocklist workflow that payment anti-fraud products use, inspired by the capabilities Xsolla publishes for its Anti-fraud product. Xsolla's actual rules are not public, so do not describe this project as "how Xsolla's anti-fraud works".

Every purchase is scored in three stages and receives one of three outcomes: `allow`, `review`, or `block`.

| Stage | Check | Possible outcome |
|---|---|---|
| 1. Blocklist | Card, device, player, or IP address is blocked | Block |
| 2. Rules | Weighted rules add points; the total is compared to review and block thresholds | Allow, Review, or Block |
| 3. ML model | Fraud risk score, used as a second opinion | Can clear a review or add one. **Never blocks** |

Purchases marked for review are decided by analysts in the review console.

| Item | Value |
|---|---|
| Deadline | 2026-11-17 |
| Jira | Project `AF` on nasirlinijatdev.atlassian.net (Scrum, one-week sprints) |
| Repository | `nasirlinijat/anti-fraud` (private) |

## 2. Technology Stack

| Component | Technology | Location | Status |
|---|---|---|---|
| Backend | Go, standard library `net/http`, pgx | `backend/` | Initialized |
| Database | PostgreSQL | Docker Compose | Initialized |
| Cache | Redis | Docker Compose | Initialized |
| Migrations | golang-migrate, run from its Docker image | `backend/migrations/` | Initialized |
| Frontend | React, TypeScript, Vite | `frontend/` | Initialized |
| ML training | Python, LightGBM | `ml/` | Not started |
| ML serving | LightGBM model file loaded by the Go service | `backend/` | Not started |
| Local setup | Docker Compose | Repository root | Initialized |

## 3. Architecture

The backend, frontend, and ML code live in one repository. The system is a **modular monolith**: one Go service and one PostgreSQL database, with the code split into packages by feature and deployed as a single binary. Python runs only offline to prepare data and train the model.

### 3.1 Decisions

| Area | Decision |
|---|---|
| Backend shape | Modular monolith in Go |
| Code layout | Layers: transport (HTTP) → services (business logic) → storage (SQL), with domain types in models and a pure scoring core that has no SQL or HTTP |
| Database access | Raw SQL run with the pgx driver, kept as constants in the storage layer |
| Schema changes | Versioned SQL migration files applied with golang-migrate |
| Rule configuration | Versioned rulesets in the database. A stored version never changes: a change adds a new version and makes it the only active one, in one transaction |
| Frontend | React with TypeScript. The console opens on a sign-in screen, sends the session token with every request, and shows each role only its pages: the review queue for reviewers, every page for admins. The proxy adds the service key only to `POST /score` |
| Model role | Assist only: it can clear a review or add one, but never produces a block |
| Model serving | Trained in Python, exported to a file, and run inside the Go service |
| Model data | IEEE-CIS converted to the API format and replayed through scoring (section 3.3) |
| Demo traffic | A seeded simulator that sends purchases in the same API format |
| Caching | Redis mirror of the blocklist only, refreshed after each committed change, with PostgreSQL fallback. All other queries hit PostgreSQL |
| Console updates | Polling every few seconds, no WebSockets |
| API authentication | Service key for scoring. Console endpoints accept a signed-in session: reviewers can use the review endpoints, admins every console endpoint. Until the console signs in, they also accept the service key for configuration reads, individual analyst keys for reviews, and individual operator keys for configuration writes. The processor's chargeback webhook is authenticated by an HMAC-SHA256 signature with a shared secret and a timestamp, not by a key |

### 3.2 Backend Layout

```
backend/
├── cmd/
│   ├── api/main.go     Wiring only: stores → services → handlers
│   ├── export-inputs/  Training export: saved model inputs and chargeback labels
│   └── mock-bank/      Mock card networks: sends chargebacks for a simulator run
└── internal/
    ├── transport/      HTTP handlers: decode, validate, map results to status codes
    ├── services/
    │   ├── score/      Score flow: saved decision, IP lookup, blocklist, model inputs, model, rules, save
    │   ├── review/     Review rules: queue pages, claims, one decision per purchase, reject can blocklist the device
    │   ├── account/    Accounts: username and password checks, bcrypt hashing
    │   ├── auth/       Sign-in: password check, attempt limit, sessions stored by token hash
    │   ├── accountevent/ Login and password-reset events, recorded once per event ID
    │   ├── ruleset/    Ruleset validation and publishing with the model report thresholds
    │   ├── blocklist/  Blocklist entry validation
    │   ├── stats/      Dashboard periods
    │   ├── chargeback/ Chargeback webhook: validation, one record per chargeback ID, never read by scoring
    │   └── simulation/ Simulation runs: idempotent starts, one worker, checkpoints, recovery
    ├── storage/        All SQL (purchases, decisions, reviews, rulesets, blocklist, stats, simulation runs, accounts, account events, chargebacks) and the Redis blocklist mirror
    ├── models/         Domain types shared by the layers
    ├── scoring/        Pure rule evaluation, no SQL or HTTP
    ├── labels/         Private fraud labels, kept apart from everything scoring uses
    ├── mockbank/       Mock card networks: plans chargebacks for simulator runs and posts them to the signed webhook
    ├── trainingdata/   Training export of saved model inputs and chargeback labels
    ├── simulator/      Demo traffic generator
    ├── lgbm/           Model loader and prediction
    ├── features/       Model inputs from the request and purchase history
    ├── ipintel/        IP intelligence provider interface and the mock that classifies documentation ranges
    ├── queryplans/     Index and load checks for the database queries
    └── platform/       Config, database pool, error helper
```

Each feature registers its own routes, so `main.go` contains only wiring.

Dependencies point one way: `transport` → `services` → `storage`. A handler only does HTTP and calls a service through a small interface; a service holds the business rules and depends only on small interfaces it defines (for example `Rulesets`, `Blocklist`, `Purchases` and `Model` in `services/score`), which the stores implement; a store only runs SQL. Services never import `transport`, and stores never import `services`. Steps that must be atomic, such as publishing a ruleset or rejecting a review and blocklisting its device, stay in one store transaction. Fraud labels stay in `labels`, outside `storage`, so the scoring path cannot reach them; `labels.SimulationStore` adds them to simulation runs when the evaluation database is configured. Chargebacks are labels too: only the webhook store (`storage/chargeback_store.go`), the training export and the mock bank handle them, and `services/chargeback/boundary_test.go` fails when any other backend code mentions them.

### 3.3 Model Data Pipeline

1. A script downloads IEEE-CIS from Kaggle. The raw and converted data are never committed to Git.
2. Each row is converted into a score request in the API format: `TransactionAmt` becomes the amount, `TransactionDT` the event time, `card1`–`card6` a stand-in card token, `card1` + `addr1` + `D1` a stand-in player ID, and `card4`, `card6`, `P_emaildomain`, and `DeviceType` map to their matching fields.
3. The converted rows are replayed through the scoring flow, so the history-based model inputs (such as card velocity) are computed by the same Go and SQL code that serves live traffic, and saved on each decision.
4. Fraud labels are stored in a separate table that scoring code never reads.
5. The saved model inputs and labels are exported, and LightGBM is trained in Python.

IEEE-CIS has no IP country, device ID, or account creation time, so these API fields are optional. Rules that need a missing field do not fire. The anonymized IEEE-CIS columns (`C`, `D`, `M`, `V`, `id_*`) are not used as model inputs.

## 4. Repository Structure

```
anti-fraud/
├── AGENTS.md                 This file
├── CLAUDE.md                 Imports this file for Claude Code
├── README.md                 Prerequisites and local setup
├── Makefile                  Development commands
├── openapi.yaml              Hand-written OpenAPI spec of the score API
├── docker-compose.yml        Local PostgreSQL, backend, migration runner, and Swagger UI
├── .env.example              Required environment variables
├── .agent/
│   └── skills/               Project skills (see section 6)
├── .github/workflows/ci.yml  CI pipeline
├── backend/
│   ├── Dockerfile            Multi-stage backend image build
│   ├── go.mod                Module github.com/nasirlinijat/anti-fraud/backend
│   ├── cmd/api/main.go       HTTP server entry point
│   ├── migrations/           Versioned SQL migrations
│   └── internal/             Feature packages (see section 3.2)
├── frontend/                 React app built with Vite
└── ml/                       Data preparation and model training (not started)
```

## 5. Development

Local setup is described in `README.md`. Create the environment file once with `cp .env.example .env`, then use the Makefile:

| Command | What it does |
|---|---|
| `make up` | Builds and starts the whole stack in Docker: PostgreSQL, Redis, migrations, the backend, and the console |
| `make down` | Stops the containers and keeps the database data |
| `make db` | Starts only PostgreSQL in Docker |
| `make run` | Applies migrations, starts Redis in Docker, and runs the backend directly with Go |
| `make test` | Applies migrations, starts Redis in Docker, and runs the backend tests |
| `make test-integration` | Runs the integration and concurrency tests in `backend/test` |
| `make test-ml` | Runs the ML tests with `ml/.venv` |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make lint-frontend` | Runs the frontend lint, type check, and unit tests |
| `make migrate` | Applies all pending database migrations |
| `make docs` | Shows `openapi.yaml` in Swagger UI at http://localhost:8090 |
| `make simulate` | Sends seeded demo purchases to the running backend |

The server listens on the port given by the `PORT` environment variable (default `8080`). `DATABASE_URL` and `API_KEY` are required, and the server exits with an error if either is missing. `REDIS_URL` is optional: when empty, the blocklist cache is disabled and lookups query PostgreSQL directly. `REVIEW_ANALYST_KEYS` is optional: a JSON array of `analyst_id` and `key` entries, each key different from `API_KEY` and from the others. While it is empty, the review endpoints return 503. `CHARGEBACK_WEBHOOK_SECRET` is optional: the shared secret, at least 32 bytes and different from `API_KEY` and every analyst and operator key, that signs `POST /chargebacks`. While it is empty, that endpoint returns 503. `IP_INTEL_PROVIDER` selects the IP intelligence provider: `mock` (the default in `.env.example` and local Docker Compose; `deploy/compose.prod.yml` defaults to `off`) or `off`, `none`, or empty for no lookup. `IP_INTEL_MOCK_RANGES` optionally names a JSON file of ranges that replaces the mock's defaults; any other provider value or an invalid file stops the server.

| Endpoint | Response |
|---|---|
| `GET /health` | 200 while the process is running |
| `GET /ready` | 200 when the database is reachable, 503 when it is not |
| `POST /score` | Scores a purchase and returns its saved decision. Requires the `X-API-Key` header. The optional `cvv_result`, `avs_result`, and `three_ds_result` fields carry the payment gateway's verification results; they are rule inputs only, never model inputs. When `ip_address` is present and a provider is configured, the address is looked up for at most 40 ms before scoring; a failed or slow lookup is saved as unavailable and never fails the score, and the result is a rule input and evidence only, never a model input. A matching retry returns the saved decision with 200. Returns 401 for a missing or wrong key, 400 for an invalid request, and 409 for a saved transaction ID with different purchase fields |
| `POST /account-events` | Records a login or password reset of a player, with the event time and optional device ID and two-letter IP country. IDs are at most 256 bytes without control characters. The account takeover rules read only events before the purchase. Requires the `X-API-Key` header and has the `/score` time limit. Returns 201 for a new event, 200 for a matching retry, 400 naming the invalid field, 401 for a missing or wrong key, 409 for a saved event ID with different fields, and 503 when it times out or the database is unavailable |
| `POST /chargebacks` | Records a chargeback reported by the payment processor (mocked by `cmd/mock-bank`) as a training label. Authenticated by `X-Signature: sha256=<hex>`, an HMAC-SHA256 of `<X-Signature-Timestamp>.<raw body>` with `CHARGEBACK_WEBHOOK_SECRET`, and a timestamp within 5 minutes. Returns 202 for a new chargeback, 200 with the recorded one for a repeat of the same `chargeback_id`, 400 for an invalid request, 401 for a missing, wrong, or stale signature, 404 for an unknown transaction, 409 for the same ID with different content, and 503 while no secret is configured. Chargebacks are never read by scoring, model inputs, rules, or reviews |
| `POST /auth/login` | Signs in with a username and password and returns a session token that lasts 12 hours. Returns 401 for a wrong username or password or a disabled account, all with the same message, and 429 with `Retry-After` after 5 attempts for a username within 15 minutes |
| `GET /auth/me` | Returns the username and role of the session. Requires `Authorization: Bearer <token>`. Returns 401 for a missing, unknown, expired, or ended session |
| `POST /auth/logout` | Ends the session. Same authentication. Returns 204 |
| `GET /reviews` | Lists purchases awaiting review by descending rule score or model risk, with a `next_cursor` for the next page and who has claimed each purchase. `claimed=mine` lists only the purchases claimed by the signed-in account. Requires a reviewer or admin session in the `Authorization: Bearer` header, or an analyst key in the `X-API-Key` header. Returns 401 for a missing or wrong session or key, and 503 for a key when no analyst keys are configured |
| `GET /reviews/{purchase_id}` | Returns the evidence behind one purchase awaiting review: rule score, fired rules, ruleset version, model score and version, recent player purchases, device activity, the CVV, AVS, and 3-D Secure results the score request sent, and the player's account events in the 30 days before the purchase, at most 20, each with its recorded time and whether it was recorded after the decision, and the saved IP intelligence lookup (null when none was made). Same authentication. Returns 404 for a purchase that is not under review or was already decided |
| `GET /reviews/{purchase_id}/network` | Returns the players and cards linked to a purchase awaiting review through its player, card, and device within 30 days of the purchase, each with its worst status, at most 30 per link. Same authentication. Returns 404 like the case endpoint |
| `POST /reviews/{purchase_id}/claim` | Claims a purchase awaiting review for the signed-in reviewer or admin, so that only that account can decide it. Requires a session; an analyst key gets 403. Returns 201 for a new claim, 200 for one you already hold, and 409 when another account holds it or the purchase was already decided |
| `DELETE /reviews/{purchase_id}/claim` | Releases an open claim, returning the purchase to the shared queue. Requires an admin session; reviewers get 403. Returns 204, 404 for a purchase without an open claim, and 409 for one already decided |
| `POST /admin/accounts` | Creates a reviewer or admin account with a password of 12 to 72 bytes. Requires an admin session; reviewers get 403. Returns 201, 400 naming the invalid field, and 409 for a taken username |
| `GET /admin/accounts` | Lists every account with its username, role, disabled flag, and creation time, never the password hash. Same authentication |
| `POST /admin/accounts/{username}/disable` | Disables an account and ends all its sessions at once. Same authentication. Returns 404 for an unknown username and 409 for your own account |
| `GET /admin/assignments` | Lists each active reviewer, and any other account still holding an open claim, with its open claims (purchase ID, amount, rule score, model score, claim time) and the number of purchases it decided. Requires an admin session; reviewers get 403 and keys 401 |
| `POST /reviews/{purchase_id}/decision` | Approves or rejects a purchase and saves the analyst and time. Same authentication. A session must hold the claim on the purchase; an analyst key decides without one. Returns 409 for a purchase that was already decided or that the session has not claimed |

`make test` defaults `TEST_DATABASE_URL` to the local database owner connection and `TEST_REDIS_URL` to local Redis for test fixtures. The backend runs as the restricted `antifraud_api` role. A direct `go test` skips the PostgreSQL tests unless `TEST_DATABASE_URL` is set. With it set, pass `-p 1`: test packages share one database, and packages running in parallel can deadlock on each other's rows.

### 5.1 Database Migrations

The schema changes only through migration files in `backend/migrations/`, applied with [golang-migrate](https://github.com/golang-migrate/migrate). The tool runs from its Docker image, so it needs no local install. Apply all pending migrations to the local database:

```bash
make migrate
```

- Each change is a pair of files with the next sequence number: `NNNNNN_description.up.sql` and `NNNNNN_description.down.sql`.
- Never edit a migration that has been merged. Add a new one instead.

### 5.2 Continuous Integration

GitHub Actions runs on every pull request and on every push to `main`. The `backend` job checks formatting with `gofmt`, runs `go vet`, applies the migrations, provisions the restricted database role, then runs `go test` with the race detector against PostgreSQL, one package at a time, `go mod verify`, and `go build`. The job times out after 15 minutes. The `frontend` job runs `npm ci`, `npm run check` (oxlint and the TypeScript check) and `npm run build` on Node.js 24. The `ml` job installs `ml/requirements.txt` on Python 3.14 and runs `pytest`; the tests use synthetic purchases and the exported golden rows, never the IEEE-CIS dataset. Both time out after 10 minutes. A new push to a pull request cancels the run still in progress for it.

### 5.3 Database Queries

Queries are raw SQL run with [pgx](https://github.com/jackc/pgx). Each feature package keeps its SQL as constants next to the store code that runs it, with numbered parameters, and scans the results itself; there is no generated database code. Add queries on the scoring and dashboard paths to the index check in `backend/internal/queryplans/`.

## 6. Skills

Read the relevant skill before performing the matching task.

| Skill | File | Use before |
|---|---|---|
| Commit style | `.agent/skills/commit-style/SKILL.md` | Creating any git commit or branch |
| Jira task creator | `.agent/skills/jira-task-creator/SKILL.md` | Creating, editing, or planning any Jira work item |
| Clean code | `.agent/skills/clean-code/SKILL.md` | Writing, reviewing, or refactoring any Go, TypeScript, or Python code |

## 7. General Rules

1. Implement only what was requested. Do not scaffold or add unrequested components.
2. Never mention AI tools or assistants in code or documentation.
3. Model inputs must use only values available at scoring time: fields in the score request, plus history computed from earlier transactions. Never use IDs, card tokens, IP addresses, fraud labels, rule outcomes, review results, or anonymized IEEE-CIS columns.
4. Never fill a missing field with a value derived from the fraud label.
5. The model can never produce a `block`. Blocks come only from the blocklist or the rules.
6. Store money as integer minor units and all times in UTC.
7. Never commit the IEEE-CIS dataset or files derived from it.
