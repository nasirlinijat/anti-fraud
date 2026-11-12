# Load Test: Score API on the AWS Server

## Summary

The deployed server scores **about 75 purchases per second with a p95 under 30 ms** and no errors. Around **100 per second** the p95 passes 200 ms. The limit is PostgreSQL CPU, and most of it goes to one model input: the card issuer history, which counts distinct values over every earlier purchase of the same issuer and gets slower as data grows. No request failed in any run from the server; overload shows up as waiting, not as errors.

| Run | Where k6 ran | Load | Requests | Req/s | p50 | p95 | p99 | Errors | Server p95 |
| :--- | :--- | :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | Team laptop on university Wi-Fi | 20/s for 3 min | 3,377 | 18.2 | 86.8 ms | 19.21 s | 19.26 s | 5.09% | 10.5 ms |
| 1 | AWS server | **steady** 20/s for 3 min | 3,601 | 20.0 | 8.4 ms | **17.9 ms** | 34.0 ms | **0%** | 10.2 ms |
| 2 | AWS server | **ramp** to 200/s, stops at p95 > 200 ms | 14,786 | 63.2 | 9.6 ms | 328 ms | 722 ms | **0%** | 292.5 ms |
| 3 | AWS server | **spike** 10 → 150/s for 30 s | 4,815 | 48.1 | 3.05 s | 7.04 s | 7.32 s | **0%** | 6.47 s |

*p50/p95/p99* are measured by k6 through HTTPS and Caddy. *Server p95* is the scoring time the backend saved with each decision (`decisions.scoring_latency_us`).

## Setup

| | |
| :--- | :--- |
| Target | `https://api.3-73-9-202.sslip.io/score`: Caddy (TLS) → Go API → PostgreSQL 18, Docker Compose |
| Server | AWS EC2 t3.small, eu-central-1: 2 vCPU, 2 GB RAM |
| Deployed code | `main` at `402b354` with the Datadog agent (AF-101). AF-109 to AF-117 were not deployed, so the AF-116 rate limit was **off** |
| Model | `lgbm-game-v5`, 1,414 trees, 54 features, assist mode |
| Tool | k6 1.3.0 (`grafana/k6` image), script `scripts/k6/score.js` |
| Traffic | 10,000 purchases from the project simulator (seed 7): 83% normal players, the rest 17 special patterns (card testing, account takeover, stolen cards, new big spenders, travellers and more). Every run uses its own ID prefix (`k6-<scenario>-<time>`), so runs do not share history. Event times are set to "now". The private fraud labels are never sent |
| Database before | 265 purchases, 34 KB compressed backup taken first |
| Date | 2026-11-12, 06:41–06:59 UTC, outside demo times, agreed with the team |

### Scenarios

| Scenario | k6 executor | What it does |
| :--- | :--- | :--- |
| steady | `constant-arrival-rate` | A fixed number of new requests per second (`RATE`, `DURATION`), whatever the response time |
| ramp | `ramping-arrival-rate` | Steps from 25 to 200 per second (`MAX_RATE`, `STEPS`, `STEP`); **stops by itself** when p95 > 200 ms or errors > 1% for 30 s |
| spike | `ramping-arrival-rate` | 10/s, a jump to 150/s within 5 s for 30 s, then back to 10/s |

"Arrival rate" means k6 starts new requests on a clock, like real clients, instead of waiting for the previous answer. A slow server therefore builds a queue, which is what happens in production too.

## Findings

### 1. The laptop's network, not the server, failed run 0

From the laptop, 5% of requests failed with `A connection attempt failed because the connected party did not properly respond`, and opening a connection took up to 15 s. The server log shows only `200` answers, and the 3,206 requests that arrived were scored with a p95 of **10.5 ms**. The laptop was on university Wi-Fi, whose firewall limits how many connections one device may open; when answers stopped coming back, k6 opened up to 274 connections, and the network started dropping them.

**Fix:** k6 runs **on the AWS server** and calls the public HTTPS address, so requests still go through TLS and Caddy without a home internet connection in between. The API key also never leaves the server this way. k6 shares the server's 2 CPUs (40–60% of one CPU during the runs), so the real capacity is a little higher than measured here.

### 2. Capacity: about 75 per second, limit near 100

Server-side scoring time during the ramp, per 20 seconds:

| Requests per second | Server p95 |
| ---: | ---: |
| 25–50 | 9–12 ms |
| 65 | 16 ms |
| 75 | 23–30 ms |
| 96–103 | 120–183 ms |
| above that | over 1 s, test stopped |

At 20 per second the busiest part was PostgreSQL at 28% of one CPU. During the ramp PostgreSQL reached **143%** (of 200% for two CPUs), the load average reached 5.1, while the Go API used only 27% and Caddy 30%. Memory was never a problem: PostgreSQL grew from 34 MB to 160 MB, the API stayed under 35 MB.

### 3. Bottleneck: the card issuer history query

`EXPLAIN (ANALYZE, BUFFERS)` on the production database after the ramp:

| Query | Rows read | Time |
| :--- | ---: | ---: |
| Issuer history for a missing issuer (`''`) | 6,220 (index-only scan) | **12.9 ms** |
| Player's earlier model scores (busiest player, 68 purchases) | 68 + 68 index lookups | 0.9 ms |

`IssuerHistorySQL` (in `internal/features/store.go`) counts distinct players, billing regions and email domains over **all earlier purchases with the same issuer**. A missing issuer is grouped as `''`, and a quarter of the simulated players leave the issuer out, so these requests read every one of thousands of rows. The query uses its index, so this is not a missing index: the work itself grows with the number of saved purchases. The spike shows it: by then the database had grown from about 4,000 to 27,000 rows, and the server completed only about 50 purchases per second instead of about 100 in the ramp.

The local benchmark (`scripts/scorebench.sh`, README) had already ranked the same query first by total time. The load test shows what it means on the real server.

### 4. The spike: no errors, but a 7-second queue, and full recovery

At 150 per second, more requests arrived than the server could finish. None failed; they waited: p50 3 s, p95 7 s. k6 dropped 1,084 requests it could not start within its limit of 730 parallel users. Within 10 seconds after the spike the server p95 was back at about 20 ms, and PostgreSQL and the API returned to idle. Nothing restarted.

Waiting 7 seconds is worse for a payment flow than a fast refusal. The AF-116 change (not deployed yet) answers `429` with `Retry-After` above `SCORE_RATE_LIMIT` (default 100 per second) and `503` after a 2-second deadline. On this server the safe limit is about **80 per second** (`SCORE_RATE_LIMIT=80`).

### 5. Database connection pool

The backend creates its pool with pgxpool defaults, which on this 2-CPU server is **4 connections**. At most 8 connections were open on the whole database (including the Datadog agent). With 2 CPUs, more parallel queries would not have helped, because PostgreSQL CPU was the limit, so the pool size is not changed. It matters if the server gets more CPUs.

### 6. What the Datadog dashboard showed

The "Anti-fraud production" dashboard for 06:40–07:05 UTC (10:40–11:05 in the dashboard's UTC+4) agrees with the measurements above:

| Widget | What it showed |
| :--- | :--- |
| Decisions per minute | Peak of about **5,500 per minute (≈ 92/s)** at the top of the ramp (06:54 UTC); a second, lower block during the spike |
| Scoring latency | Flat and low through steady and most of the ramp, then rising to about **6.5 s** during the spike |
| Average response time (gateway) | Peak **5.7 s** during the spike, back to normal right after |
| Requests by status code | Almost only `200`; **no `5xx`** in the window ("Server errors" shows no data) |
| CPU by container | PostgreSQL up to about **1 full core**; the Go API about 0.2 core |
| Server CPU | Up to about 70% averaged over both CPUs (Datadog averages over 15 s, so the short peaks from `docker stats` look lower) |
| Memory | 40–60% of the server's memory always free; PostgreSQL grew to about 250 MB with its cache |
| Disk in use | Flat at 52% |

One side finding: the **"Backend and database ready"** widget showed no value for the `http.can_connect` check during the whole window. Either that check does not report or the widget asks for the wrong name. That belongs to the Datadog setup (AF-101/AF-102), not to this test.

## Fixes made

| Problem | Fix | Status |
| :--- | :--- | :--- |
| The laptop's network failed before the server did | Run k6 on the server against the public HTTPS address | Done, runs 1–3 |
| Overload makes clients wait seconds instead of failing fast | Rate limit with `429` and a 2 s deadline with `503` (AF-116) | Built and tested, waits for deployment; recommended `SCORE_RATE_LIMIT=80` here |
| Issuer history grows with data and limits capacity | Keep running issuer totals, like `feature_counts` already does for all purchases and email domains, or cap the history window | **Not done**: it changes how a model input is computed, so the model must be checked or retrained with it. Proposed as a follow-up ticket for the team and the ML owner |

## Limits of this test

- The server measured itself while also running k6 (40–60% of one CPU). A separate load machine in the same AWS region would give slightly higher numbers.
- The deployed code is `main`, not the AF-109 to AF-117 branches. Their changes (layers, logging, rate limit) were not under load here.
- The database started almost empty (265 purchases). Real data grows, and the issuer query slows down with it, so the capacity will go **down** over time until finding 3 is fixed.
- One t3.small instance with burstable CPU. It had enough saved CPU credits for these 18 minutes; a long run at full CPU would be slowed to the t3.small baseline.
- The numbers in this report come from k6, the saved decisions, `docker stats` samples every 4–5 seconds, and the Datadog dashboard (finding 6), which is linked below.

## Cleanup

All test purchases (26,411 across runs 0–3) and their decisions were deleted in one transaction afterwards. The transaction first checked that no real purchase had arrived during the test (still 265) and that no analyst had decided a test case, then restored `feature_counts` to its earlier values (265 purchases, 113 players), and `VACUUM (ANALYZE)` refreshed the table statistics. The database ended with exactly the 265 purchases and decisions it had before.

Datadog dashboard for the test window: <https://app.datadoghq.eu/dashboard/h4s-58j-z53/anti-fraud-production?from_ts=1794465600000&to_ts=1794467100000&live=false>

## How to run it

```bash
cd backend
go run ./cmd/simulator -dump ../scripts/k6/purchases.jsonl -count 10000 -seed 7 -run k6
cd ../scripts/k6
BASE_URL=http://localhost:8080 API_KEY=local-dev-key SCENARIO=steady RATE=20 DURATION=2m k6 run score.js
```

`SCENARIO` is `steady`, `ramp` or `spike`. Other settings: `RATE`, `DURATION` (steady); `MAX_RATE`, `STEPS`, `STEP` (ramp); `BASE_RATE`, `SPIKE_RATE` (spike); `RUN` (the ID prefix, default `k6-<time>`). Against the AWS server, agree a time with the team, take a backup, run k6 on the server, and delete the `k6-` rows afterwards.
