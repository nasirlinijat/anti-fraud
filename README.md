# Anti-Fraud

A real-time fraud scoring and review console for in-game purchases.

## Prerequisites

| Tool | Version |
|---|---|
| Git | Any recent version |
| Docker with the Compose plugin | Docker Engine running, Compose v2 |
| GNU Make | Any recent version |
| Go | 1.26.5 or newer |

Go is needed for `make run`, `make test`, and `make lint`. The other commands run in Docker.

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
| `make test` | Starts PostgreSQL in Docker and runs the tests |
| `make lint` | Checks formatting with `gofmt` and runs `go vet` |
| `make migrate` | Applies all pending database migrations |

## Troubleshooting

| Problem | Fix |
|---|---|
| `set POSTGRES_USER in .env`, or a similar message | Run `cp .env.example .env` |
| `make run` reports that the port is already in use | Run `make down` to stop the backend container started by `make up` |
| Port `5432` or `8080` is used by another program | Change `POSTGRES_PORT` or `PORT` in `.env`, and update the port in `DATABASE_URL` to match `POSTGRES_PORT` |
| You want an empty database | Run `docker compose down -v`, which deletes the local database data, then `make up` and `make migrate` |
