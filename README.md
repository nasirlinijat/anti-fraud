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

6. Stop everything when you are done. Your database data is kept:

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

4. Open http://localhost:5173. The page shows whether the backend is ready.

The development server forwards backend requests to `http://localhost:8080`, so the browser needs no CORS setup. If the backend runs on another port, start the server with `BACKEND_URL=http://localhost:<port> npm run dev`.

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
| `make run` reports that the port is already in use | Run `make down` to stop the backend container started by `make up` |
| Port `5432` or `8080` is used by another program | Change `POSTGRES_PORT` or `PORT` in `.env`, and update the port in `DATABASE_URL` to match `POSTGRES_PORT` |
| You want an empty database | Run `docker compose down -v`, which deletes the local database data, then `make up` and `make migrate` |
