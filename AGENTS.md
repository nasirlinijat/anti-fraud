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
| Code layout | Layers: transport (HTTP) → services (business logic) → storage (SQL), with domain types in models and a pure scoring core that has no SQL or HTTP. Features not moved yet stay package by feature |
| Database access | Raw SQL run with the pgx driver, kept as constants in each feature package |
| Schema changes | Versioned SQL migration files applied with golang-migrate |
| Rule configuration | Versioned rulesets in the database. A stored version never changes: a change adds a new version and makes it the only active one, in one transaction |
| Frontend | React with TypeScript |
| Model role | Assist only: it can clear a review or add one, but never produces a block |
| Model serving | Trained in Python, exported to a file, and run inside the Go service |
| Model data | IEEE-CIS converted to the API format and replayed through scoring (section 3.3) |
| Demo traffic | A seeded simulator that sends purchases in the same API format |
| Caching | None. Indexed PostgreSQL queries only, no Redis |
| Console updates | Polling every few seconds, no WebSockets |
| API authentication | Service key for scoring and configuration reads, individual analyst keys for reviews, and individual operator keys for configuration writes |

### 3.2 Backend Layout

```
backend/
├── cmd/api/main.go     Wiring only: stores → services → handlers
└── internal/
    ├── transport/      HTTP handlers: decode, validate, map results to status codes
    ├── services/score/ Score flow: saved decision, blocklist, model inputs, model, rules, save
    ├── storage/        SQL for purchases and decisions
    ├── models/         Domain types shared by the layers (Purchase, Decision)
    ├── scoring/        Pure rule evaluation, no SQL or HTTP
    ├── rulesets/       Rule configuration
    ├── reviews/        Review queue and decisions
    ├── blocklist/      Blocklist entries
    ├── stats/          Dashboard queries
    ├── simulator/      Demo traffic generator
    ├── lgbm/           Model loader and prediction
    ├── features/       Model inputs from the request and purchase history
    ├── queryplans/     Index and load checks for the database queries
    └── platform/       Config, database pool, error helper
```

Each feature registers its own routes, so `main.go` contains only wiring.

Dependencies point one way: `transport` → `services` → `storage`. A handler only does HTTP and calls a service through a small interface; a service holds the business rules and depends only on small interfaces it defines (for example `Rulesets`, `Blocklist`, `Purchases` and `Model` in `services/score`), which the stores implement; a store only runs SQL. Services never import `transport`, and stores never import `services`. Scoring follows this layout; the other features move to it one at a time.

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
| `make up` | Builds and starts PostgreSQL and the backend in Docker |
| `make down` | Stops the containers and keeps the database data |
| `make run` | Starts PostgreSQL in Docker and runs the backend directly with Go |
| `make test` | Starts PostgreSQL in Docker, applies migrations, and runs the tests |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make migrate` | Applies all pending database migrations |
| `make docs` | Shows `openapi.yaml` in Swagger UI at http://localhost:8090 |

The server listens on the port given by the `PORT` environment variable (default `8080`). `DATABASE_URL` and `API_KEY` are required, and the server exits with an error if either is missing. `REVIEW_ANALYST_KEYS` is optional: a JSON array of `analyst_id` and `key` entries, each key different from `API_KEY` and from the others. While it is empty, the review endpoints return 503.

| Endpoint | Response |
|---|---|
| `GET /health` | 200 while the process is running |
| `GET /ready` | 200 when the database is reachable, 503 when it is not |
| `POST /score` | Scores a purchase and returns its saved decision. Requires the `X-API-Key` header. A matching retry returns the saved decision with 200. Returns 401 for a missing or wrong key, 400 for an invalid request, and 409 for a saved transaction ID with different purchase fields |
| `GET /reviews` | Lists purchases awaiting review by descending rule score or model risk, with a `next_cursor` for the next page. Requires an analyst key in the `X-API-Key` header. Returns 401 for a missing or wrong key and 503 when no analyst keys are configured |
| `GET /reviews/{purchase_id}` | Returns the evidence behind one purchase awaiting review: rule score, fired rules, ruleset version, model score and version, recent player purchases, and device activity. Same authentication. Returns 404 for a purchase that is not under review or was already decided |
| `POST /reviews/{purchase_id}/decision` | Approves or rejects a purchase and saves the analyst and time. Same authentication. Returns 409 for a purchase that was already decided |

`make test` defaults `TEST_DATABASE_URL` to the local database owner connection for test fixtures. The backend runs as the restricted `antifraud_api` role. A direct `go test` skips the PostgreSQL tests unless `TEST_DATABASE_URL` is set. With it set, pass `-p 1`: test packages share one database, and packages running in parallel can deadlock on each other's rows.

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
