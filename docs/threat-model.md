# Anti-Fraud threat model

This review covers the Docker Compose stack, its AWS gateway, and the Vite development server. It uses the six categories in [Microsoft's STRIDE model](https://learn.microsoft.com/azure/security/develop/threat-modeling-tool-threats).

## Trust boundaries

- The browser sends its account session to Caddy or Vite. Console routes require sessions and enforce account roles. Only the score proxy adds the service key; both console proxies first verify the session with the backend.
- Clients can also call the Go API directly on its published loopback port. The API validates credentials and input independently of the proxy.
- The API connects to PostgreSQL over the Compose network or a local connection during host development. The migration runner uses the database administrator. The API connects as the restricted `antifraud_api` role.
- Protected assets include the service key and console sessions, purchase and device history, model scores, rulesets, blocklist entries, and the analyst's decision record.

## Review

| Threat | Go API | Caddy and Vite | PostgreSQL | React console |
|---|---|---|---|---|
| Spoofing | The service key authenticates scoring and account events. Console routes accept one Bearer session header and derive identity and role from its account. | Caddy checks the host, origin, and cross-site write metadata. Vite checks the host and origin of protected routes and validates the session before adding the scoring credential. | PostgreSQL requires its own credential. The host port binds to loopback. | The user signs in with a username and password. The browser sends the returned session token. |
| Tampering | Write endpoints validate fields and reject trailing JSON. Scoring retries with different purchase data conflict. | The proxies set the service credential upstream and pass through the review credential. Their origin checks restrict browser requests before routing. | Parameterized queries, uniqueness constraints, and transactions protect saved decisions, ruleset publication, and review rejections that block the purchase's device, card or player. | React renders transaction values and error strings as text. The decision dialog identifies the transaction and requires confirmation. |
| Repudiation | Reviews record the authenticated analyst and server time. Configuration writes record the authenticated actor, target, action, and server time in the same transaction. | The proxy routes requests under the applicable credential. Actor attribution comes from the backend. | Each review has at most one resolution. The API can insert audit events and lacks permission to read, update, or delete them. | The console shows the saved decision or an error, and refreshes to reconcile an uncertain outcome. |
| Information disclosure | Protected responses use `Cache-Control: no-store`. JSON responses use `nosniff`. Server errors return generic messages. | Both proxies keep the configured service key server-side. Caddy strips the service key from upstream response headers. | Review queries project case evidence and device activity counts. The database retains the underlying tokens and history. | Session tokens are retained in browser storage and cleared on sign-out or session rejection. The console renders the evidence returned by the API. |
| Denial of service | The HTTP server bounds header size and connection times. JSON write handlers limit request bodies, and review reads are paginated. | Compose publishes the console on loopback. Both proxies reject requests that fail their local browser checks. | The connection pool bounds concurrent database connections. Indexes support review and scoring queries. | Requests time out. Polling avoids overlapping reads and supports cancellation. |
| Elevation of privilege | Reviewers can use review routes and model information. Admin sessions manage accounts, configuration, statistics and simulations. Every decision requires the actor's claim. Reviewers can block only the rejected purchase's own device, card and player, atomically with rejection. | Console requests forward sessions. The production score proxy verifies the session before adding the service key. The edge uses HTTPS. | The API role has explicit table privileges and lacks schema creation and migration permissions. | The API enforces roles independently of visible controls. |

## Verification

- PostgreSQL integration tests verify that the API role can change configuration through the application and cannot read or alter audit records or create application tables.
- The backend tests cover duplicate credential headers, protected response headers, invalid and oversized blocklist bodies, distinct reviewer sessions, and review resolution.
- The Vite proxy integration test checks same-origin service access, reviewer session forwarding, and rejection of requests with a foreign origin or host before they reach the backend.

## Operational follow-ups

- Limit administrator and log access, and define retention for purchase, device, and audit history.
- `POST /score` has a per-key token bucket and a deadline (`SCORE_RATE_LIMIT`, `SCORE_RATE_BURST`, `SCORE_TIMEOUT`). Add gateway-wide rate limits for the other routes before serving untrusted traffic. Body size and connection limits bound individual requests.

## Automated security checks

CI runs these on every pull request and push to `main`. A finding fails the build.

| Check | Tool | What it looks at |
|---|---|---|
| Secrets | gitleaks 8.30.0 | The whole Git history, not only the current files |
| Go vulnerabilities | govulncheck 1.1.4 | Known vulnerabilities in the Go standard library and modules that the backend's code actually calls |
| Code issues | gosec 2.30.0 | Risky patterns in the backend source |
| Image | Trivy 0.76.1 | The built backend image (Debian packages and the Go binary); critical findings fail, high findings are printed |
| Console packages | npm audit | Every package in `frontend/package-lock.json`; high and critical findings fail |
| ML packages | pip-audit 2.10.1 | Every Python package the ML job installed, including indirect ones such as scipy; any known vulnerability fails |

The first run found 23 vulnerabilities in the Go 1.26.5 standard library (HTTP, TLS, X.509, XML, URL parsing). The backend now builds with Go 1.26.11, which fixes all of them, and govulncheck and Trivy report none. gosec findings about file paths and unchecked errors were fixed in the code. The first ML audit found CVE-2025-71176 in pytest 8.4.2 (unsafe temporary directories); the ML code now uses pytest 9.2.0.

### Accepted findings

These gosec rules are excluded in CI. Each one was reviewed; the reason is why it does not apply to this code.

| Rule | Where | Why it is accepted |
|---|---|---|
| G101 hardcoded credentials | `platform.APIKeyHeader = "X-API-Key"` | It matches the name of a constant, not a secret: the value is the header name. Real secrets are found by gitleaks, which checks values and the whole history. |
| G115 integer conversion overflow | `int` to `int32` when writing rulesets, decisions, the review page size and statistics limits | Every converted value is either read from an `int32` database column or validated before: thresholds and weights arrive as `int32`, the sum of enabled weights is limited to `MaxInt32`, page sizes and limits are small constants. |
| G118 goroutine with `context.Background` | the simulation worker and the shutdown in `cmd/api/shutdown.go` | Deliberate. A simulation run must outlive the HTTP request that started it and is stopped through its own cancel function. Shutdown needs a fresh deadline after the signal context has ended. |
| G404 weak random numbers | `internal/simulator` | The simulator must produce the same purchases for the same seed, so it uses seeded `math/rand/v2`. It generates demo traffic, never keys or tokens; request IDs and simulation IDs use `crypto/rand`. |

gitleaks ignores two fingerprints in `.gitleaksignore`: the documented local development key `local-dev-key` in an old README example, and a made-up key in a logging test that checks keys never reach the logs. Neither is a real credential.

## Simulation control

Simulation routes are absent unless `SIMULATION_ENABLED=true`. Starting and
stopping traffic requires an admin session; status and history also require an admin session. Console proxies preserve these credentials and same-origin checks.
The public API hostname does not forward simulation routes. The controller uses
a fixed loopback scoring URL, bounded rate and backfill size, a database active-run
constraint and an advisory lock. History records the initiating and stopping
operators. An in-flight transaction marker allows recovery after a score commits
but before progress is checkpointed. An unresolved marker is shown explicitly;
recovery interrupts unfinished runs and does not resume traffic automatically.

## Evaluation answer isolation

Simulator answers and replay imports use the separate `antifraud_labels`
connection. The scoring role has no access to the final or pending answer tables;
the evaluation role cannot insert purchases or change rule configuration. Final
answers are immutable, repeated matching writes are idempotent, and conflicts
stop ingestion. Private intents bridge the score-commit/label-write crash window.
Console labels and progress finalize in one transaction. Existing runs without
answer metadata remain unlabelled. Neither score requests nor public responses
include per-purchase answers.


Quality reporting reads the separate evaluation connection in read-only
transactions. Its admin-session endpoints expose per-run aggregates and run IDs,
never purchase-level answers. Source and run ID scope every evaluation. Scoring
credentials remain unable to read the answer tables. Optional evaluation failures
do not disable scoring; simulation dispatch still requires successful private
persistence preflight. Quality routes are exposed through the console proxy and
remain excluded from the public production API hostname.
