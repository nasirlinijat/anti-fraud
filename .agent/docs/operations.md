# Operations

## Tracing

The backend can send OpenTelemetry traces to the Datadog agent over OTLP gRPC. Each `POST /score` is one trace with a span for the HTTP request (named after the route, with the request ID), one span per database query (`postgresql.query`, with the SQL text but never the query values), and spans for the model (`score.model`, with the model version and score) and the rules (`score.rules`, with the outcome and points). Every log line written during a traced request has `dd.trace_id` and `dd.span_id`, so Datadog links the log to its trace. `/health` and `/ready` are not traced.

| Variable | Default | What it does |
|---|---|---|
| `TRACING_ENDPOINT` | empty | `host:port` of the OTLP gRPC receiver, for example `datadog:4317`. Empty turns tracing off completely: no spans, no extra work per request |
| `TRACING_SAMPLE_RATIO` | `1` | Share of requests traced, from `0` to `1` |

Measured cost with tracing on: about 0.013 ms for the request span and 0.0015 ms per database query span, about 0.035 ms for a whole score request.

On the server, `deploy/compose.tracing.yml` turns on the agent's APM and OTLP receiver and points the backend at it. `deploy/deploy.sh` adds it together with `deploy/compose.datadog.yml` whenever `DD_API_KEY` is set, unless `.env` has `DD_APM_ENABLED=false`. By hand, the command is:

```bash
docker compose -p antifraud --env-file ../.env -f docker-compose.yml -f deploy/compose.prod.yml -f deploy/compose.datadog.yml -f deploy/compose.tracing.yml up -d --wait
```

Traces then appear in Datadog APM under the service `anti-fraud-backend`.

## Score limits

`POST /score` protects itself from overload in two ways:

| Variable | Default | What it does |
|---|---|---|
| `SCORE_RATE_LIMIT` | `100` | Requests per second allowed for each API key, refilled continuously (a token bucket). `0` turns the limit off, for example for a load test |
| `SCORE_RATE_BURST` | `200` | How many requests a key can send at once before the per-second rate applies |
| `SCORE_TIMEOUT` | `2s` | Deadline for one score request, passed to every database query. Any Go duration, such as `500ms` |

Over the limit the API answers `429 Too Many Requests` with a `Retry-After` header in seconds. Only requests with a valid key count, so wrong keys cannot use up the limit. When the deadline passes, the API answers `503 Service Unavailable` with `Retry-After: 1`, and nothing is saved: the purchase, its decision and its feature counts are written in one transaction that is rolled back. A retry with the same transaction ID then scores normally.

## Database pool

The backend keeps a pool of PostgreSQL connections for `DATABASE_URL` and logs its size at startup (`"msg":"database pool","max_conns":4`).

| Variable | Default | What it does |
|---|---|---|
| `DB_MAX_CONNS` | empty | Most connections the pool opens. Empty uses pgx's default, the larger of 4 and the number of CPUs (4 on the t3.small). It must be a whole number, 1 or more, or the backend does not start. It replaces `pool_max_conns` in `DATABASE_URL` when both are set |

PostgreSQL accepts at most `max_connections` connections (100 by default, 3 of them kept for superusers). Every backend replica opens up to `DB_MAX_CONNS` connections, plus the small pool for `LABELS_DATABASE_URL` when simulations or quality reports are on, so keep

```
replicas × (DB_MAX_CONNS + labels pool) + other clients < max_connections − 3
```

where other clients are the Datadog agent's PostgreSQL check, migrations, backups and command-line tools. A bigger pool only helps while the database has CPU to spare: in the load test ([LOAD_TEST.md](../../LOAD_TEST.md)) the limit was database CPU, not the number of connections.

## Blocklist cache

Stage 1 of scoring matches cards, player IDs, devices, and IP addresses against a mirror of `blocklist_entries` kept in Redis.

- **PostgreSQL is the source of truth.** All writes (operator additions and removals, and review rejection device blocks) commit to PostgreSQL first, then refresh the corresponding key in Redis.
- **Fail-safe fallback.** If Redis is down, unreachable, or its mirror is being rebuilt, scoring transparently falls back to the PostgreSQL blocklist query without interrupting requests or failing readiness checks.
- **Eviction safety.** Redis runs with `maxmemory-policy: noeviction` so entries are never silently dropped under memory pressure.
- **Cache flush and reload.** If the Redis cache is flushed (`redis-cli flushdb`), the missing `ready` key triggers an asynchronous reload from PostgreSQL on the next score request.
- **Configuration.** Set `REDIS_URL` to enable the cache (e.g. `redis://localhost:6379/0`). When unset or empty, the cache is disabled and scoring queries PostgreSQL directly.

## Scoring metrics

When `DOGSTATSD_ADDR` is set (`host:port`, for example `datadog:8125`), the backend sends DogStatsD metrics to the Datadog agent for every decision it saves. Retries that return an already saved decision are not counted again. `deploy/compose.datadog.yml` sets the address and lets the agent accept metrics from other containers, so production sends them whenever Datadog is deployed. Empty turns them off.

| Metric | Type | Tags |
|---|---|---|
| `antifraud.score.decisions` | count | `outcome`, `ruleset_version`, `model_version` (`none` without a model) |
| `antifraud.score.fired_rules` | count, one per fired rule | `rule`, `outcome`, `ruleset_version` |
| `antifraud.score.points` | histogram of rule points | `outcome`, `ruleset_version`, `model_version` |
| `antifraud.score.model_score` | histogram, only when a model scored | `outcome`, `ruleset_version`, `model_version` |
| `antifraud.score.latency_ms` | histogram | `outcome`, `ruleset_version`, `model_version` |

Every metric also has `service:anti-fraud-backend`. The agent turns each histogram into `.avg`, `.median`, `.max`, `.95percentile` and `.count`. Metrics go over UDP through a client that drops them when its buffer is full, so sending never slows down or fails a score request, and the backend works the same without an agent.

The "Fraud decisions" section of `deploy/datadog/dashboard.json` shows them: review rate, top fired rules, model score by outcome, p95 scoring latency by model version, and decisions by ruleset and model version. After deploying, run `deploy/datadog/setup.sh` on the server to update the dashboard.

## Logs

The backend writes one JSON object per line to standard output with `log/slog`, so Datadog reads every field as an attribute. Every line has `time`, `level`, `msg` and `service` (`anti-fraud-backend`). Each HTTP request is logged once:

```json
{"time":"2026-11-12T09:08:46Z","level":"INFO","msg":"http request","service":"anti-fraud-backend","http":{"method":"POST","route":"/score","status_code":200},"duration":134506800,"request_id":"bus-demo-1"}
```

- `duration` is in nanoseconds, and `http.route` is the route pattern, not the raw URL.
- Every request gets a request ID. A client can send its own in `X-Request-ID` (1 to 64 letters, digits, `-`, `_` or `.`); otherwise the backend makes one. It comes back in the `X-Request-ID` response header and is on every log line of that request, so one ID finds all lines of a failed request.
- Responses with status 500 or more are logged at `ERROR`. Successful `/health` and `/ready` checks are not logged.
- A panic in a handler is recovered: the client gets a 500 JSON error and the log gets the stack.
- API keys, card tokens and IP addresses are never logged; a test checks this.

The same middleware sets the security headers on every response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'` and `Referrer-Policy: no-referrer`. HTTPS and HSTS are added by the Caddy edge in production.

## Saved decision status

`GET /decision-status` uses SCORE_RATE_LIMIT, SCORE_RATE_BURST and SCORE_TIMEOUT with its own rate bucket, separate from POST /score. Invalid credentials do not consume tokens. Reads are indexed, uncached, and return 503 with Retry-After on deadline/database failure. Default polling guidance is five seconds per pending purchase.
