# Chaos Test: No Scored Purchase Is Lost or Scored Twice

## Summary

For 6 minutes, purchases arrived at 10 per second while the backend was restarted, then killed without warning, PostgreSQL was restarted, and the HTTPS gateway was stopped for 30 seconds. **All 3,462 purchases were scored and saved exactly once. Every client retry of a transaction ID got the saved decision back, never a second one.** One wrong status code was found and fixed: during the database restart, clients got `500` instead of `503`.

| Check | Result |
| :--- | ---: |
| Purchases sent and confirmed by the clients | 3,462 / 3,462 |
| Purchases saved in the database | 3,462 |
| Purchases without a decision | 0 |
| Purchases with more than one decision | 0 |
| Transaction IDs saved more than once | 0 |
| Decisions without a purchase | 0 |
| Missing purchase numbers (0 to 3,461) | 0 |
| Clients that gave up | 0 |
| Answered purchases sent again on purpose | 333 |
| Second answers that differed from the first | 0 |
| Failed requests that were retried | 1,450 of 5,245 |
| Response time of successful requests (median / p95) | 7.3 ms / 13.1 ms |

## What the system relies on

| Mechanism | Protects against |
| :--- | :--- |
| `purchases.transaction_id` is unique | the same purchase being saved twice by retries or parallel requests |
| `decisions.purchase_id` is the primary key | a purchase getting two decisions |
| The purchase, its decision and the feature counts are saved in **one transaction** | a crash between the purchase and the decision leaving half a record |
| A retry with a saved transaction ID returns the saved decision (AF-109 `FindSavedDecision`); a different purchase with that ID gets `409` | clients retrying after a timeout getting a new, possibly different decision |
| `restart: unless-stopped` and the 30 s stop grace period in Docker Compose | a stopped backend staying down; requests being cut off on a normal restart |
| pgx connection pool reconnects on its own | the backend needing a restart after PostgreSQL comes back |

## Setup

| | |
| :--- | :--- |
| Target | The AWS server, `https://api.3-73-9-202.sslip.io/score`: Caddy (TLS) → Go API → PostgreSQL 18 |
| Deployed code | `main` at `402b354` plus the AF-101 Datadog files |
| Traffic | `scripts/k6/chaos.js`, k6 1.3.0 in a container **on the server**, 10 new purchases per second for 6 minutes, replaying 10,000 purchases from the project simulator (normal players and fraud patterns) |
| Client behaviour | Like a payment provider: on any failure (no connection, `5xx`, 10 s timeout) it retries **the same transaction ID** with exponential backoff and jitter (0.2 s doubling to 5 s) for up to 3 minutes. 10% of answered purchases are sent once more to compare the answers |
| Faults | A shell script on the server ran `docker restart`, `docker kill`, `docker start` and `docker stop` at fixed times, so the timing did not depend on the laptop's network |
| Outage measurement | A loop on the server called `GET /ready` through the gateway every second |
| Safety | Backup `antifraud-20261112T073820Z.sql.gz` first; all test rows deleted afterwards (see Cleanup) |
| Date | 2026-11-12, 07:38–07:45 UTC |

## Fault timeline and what happened

| Time (UTC) | Fault | `/ready` during the fault | Effect on traffic |
| :--- | :--- | :--- | :--- |
| 07:38:52 | traffic starts | 200 | 100 purchases saved per 10 s |
| 07:39:52 | **backend restart** (`docker restart`: SIGTERM, then start) | one `502` at 07:39:53 | none visible: the restart took about 1 second, the few refused requests were retried at once |
| 07:40:43 | **backend killed** (`docker kill`, SIGKILL, no grace), started again after 5 s | `502` from 07:40:44 to 07:40:48 | 67 saved in that 10 s window, then 128 in the next as the retries arrived |
| 07:41:38 | **PostgreSQL restart** (`docker restart`) | one `503` at 07:41:39 (the API correctly reported the database as unreachable) | 13 requests answered `500` (see finding 1), all retried successfully; 97 saved instead of 100 |
| 07:42:29 | **gateway stopped** (`docker stop` Caddy) for 30 s | no connection (`000`) from 07:42:30 to 07:43:00 | nothing saved for 30 s, then 270 in the first 10 s after it came back |
| 07:44:52 | traffic ends | 200 | — |

What the gateway logged for `/score` during the run: 3,795 × `200`, 440 × `502` (backend down: connection refused, and while the container was gone even its DNS name `backend` did not resolve), 13 × `500`. The requests sent while the gateway itself was stopped never reached any server; k6 counted them as connection failures (about 1,000 of the 1,450 failures, because several hundred purchases were retrying at once).

## Findings and fixes

### 1. A database restart answered `500 internal error` → now `503` with `Retry-After`

During the PostgreSQL restart, the backend could not open connections for about a second. The score handler treated that like any unknown error: `500 {"error":"internal error"}`. The client in this test retries every failure, but a real payment provider may treat `500` as a bug on our side and stop, while `503` means "temporarily unavailable, try again".

The failing step was **connecting**, so nothing had reached the database and nothing was saved. pgx reports exactly this: a failed connect is a `*pgconn.ConnectError`, and `pgconn.SafeToRetry(err)` is true when the failing statement was never sent.

**Fix** (on branch `af-119-chaos-test`, not deployed yet):

```go
// internal/platform/database.go
func DatabaseUnavailable(err error) bool {
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr) || pgconn.SafeToRetry(err)
}
```

```go
// internal/transport/score_handler.go
case platform.DatabaseUnavailable(err):
	w.Header().Set("Retry-After", "1")
	platform.WriteError(w, http.StatusServiceUnavailable, "the database is unavailable, nothing was saved; retry later")
```

`TestScoreWithRealStoresAndNoDatabaseReturns503` wires the real stores and service to a database that cannot be reached and checks for `503`. With the new rule switched off, the same test fails with exactly what production returned: `500 {"error":"internal error"}`.

An error **after** a statement was sent (for example the connection dropping during `COMMIT`) is still `500`, because then the server cannot be sure what was saved. The client's retry with the same transaction ID resolves it safely either way.

### 2. Long outages make clients pile up

During the 30-second gateway outage, about 300 purchases were retrying at the same time. k6 had to start more virtual users than it had ready and **did not send 138 planned purchases** in time ("dropped iterations"). These purchases were never sent, so nothing was lost, but it shows the shape of an outage: when the gateway comes back, everything waiting arrives at once (270 purchases in the first 10 seconds instead of 100). This is what the AF-116 rate limit is for; with it deployed, the catch-up burst is spread out with `429` and `Retry-After` instead of hitting the database all at once.

### 3. The normal restart is invisible

`docker restart` sends SIGTERM, the backend's `http.Server.Shutdown` finishes the requests in flight within the 30 s grace period, and the new process was ready in about a second. Only one `/ready` probe saw a `502`. A deploy with `deploy/deploy.sh` restarts the backend the same way.

## Cleanup

All 3,462 test purchases (prefix `k6-chaos-073850-`) and their decisions were deleted in one transaction afterwards. The transaction first checked that no real purchase had arrived during the test (still 265) and that no analyst had decided a test case, restored `feature_counts` to its earlier values (265 purchases, 113 players), and `VACUUM (ANALYZE)` refreshed the statistics. `/health` and `/ready` answered `200` afterwards.

## Known limits

- The deployed code is `main`; finding 1's fix and the AF-116 rate limit are on the branches and were not under chaos here.
- Faults were container restarts on one server. A whole-server reboot, a full disk, or a lost AWS volume were not tested; the nightly backup (`deploy/backup.sh`) covers data loss, not downtime.
- PostgreSQL restarted cleanly. A crash in the middle of a write relies on PostgreSQL's write-ahead log, which this test did not force.
- k6 ran on the same server, so its retries were not slowed by a real internet connection.

## How to run it again

On the server, with a backup taken first:

```bash
cd ~/k6
set -a; . ~/anti-fraud/.env; set +a
docker run -d --name k6-chaos --network host -v ~/k6:/k6 -w /k6 -e API_KEY \
  -e BASE_URL=https://api.3-73-9-202.sslip.io -e RUN=k6-chaos -e RATE=10 -e DURATION=6m \
  grafana/k6:1.3.0 run --summary-export /k6/k6-chaos-summary.json chaos.js
```

Then, in another shell on the server, cause the faults about a minute apart:

```bash
docker restart antifraud-backend-1
docker kill antifraud-backend-1; sleep 5; docker start antifraud-backend-1
docker restart antifraud-db-1
docker stop antifraud-edge-1; sleep 30; docker start antifraud-edge-1
```

`docker logs k6-chaos` shows the result; the thresholds fail the run if any purchase was given up or any retry changed its answer. Then run the SQL checks (purchases without a decision, more than one decision, duplicated IDs, gaps) for the `k6-chaos-` prefix, and delete those rows. `RATE`, `DURATION`, `GIVE_UP_AFTER_MS` and `RECHECK_SHARE` can be changed with `-e`.
