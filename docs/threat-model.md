# Anti-Fraud threat model

This review covers the Docker Compose stack, its AWS gateway, and the Vite development server. It uses the six categories in [Microsoft's STRIDE model](https://learn.microsoft.com/azure/security/develop/threat-modeling-tool-threats).

## Trust boundaries

- The browser sends console requests to Caddy or Vite. The proxy supplies the service key for scoring, statistics, rulesets, and blocklist requests. Review requests carry the analyst's individual key. Configuration writes carry the operator's individual key.
- Clients can also call the Go API directly on its published loopback port. The API validates credentials and input independently of the proxy.
- The API connects to PostgreSQL over the Compose network or a local connection during host development. The migration runner uses the database administrator. The API connects as the restricted `antifraud_api` role.
- Protected assets include service and analyst keys, purchase and device history, model scores, rulesets, blocklist entries, and the analyst's decision record.

## Review

| Threat | Go API | Caddy and Vite | PostgreSQL | React console |
|---|---|---|---|---|
| Spoofing | The service key and individual analyst keys are separate. The API checks a single credential header and derives the analyst ID from the configured key. | Caddy checks the host, origin, and cross-site write metadata. Vite checks the host and origin of protected routes before forwarding credentials. | PostgreSQL requires its own credential. The host port binds to loopback. | The analyst enters an individual key. The browser sends it with review requests and holds it in memory. |
| Tampering | Write endpoints validate fields and reject trailing JSON. Scoring retries with different purchase data conflict. | The proxies set the service credential upstream and pass through the review credential. Their origin checks restrict browser requests before routing. | Parameterized queries, uniqueness constraints, and transactions protect saved decisions, ruleset publication, and review rejections that block a device. | React renders transaction values and error strings as text. The decision dialog identifies the transaction and requires confirmation. |
| Repudiation | Reviews record the authenticated analyst and server time. Configuration writes record the authenticated actor, target, action, and server time in the same transaction. | The proxy routes requests under the applicable credential. Actor attribution comes from the backend. | Each review has at most one resolution. The API can insert audit events and lacks permission to read, update, or delete them. | The console shows the saved decision or an error, and refreshes to reconcile an uncertain outcome. |
| Information disclosure | Protected responses use `Cache-Control: no-store`. JSON responses use `nosniff`. Server errors return generic messages. | Both proxies keep the configured service key server-side. Caddy strips the service key from upstream response headers. | Review queries project case evidence and device activity counts. The database retains the underlying tokens and history. | The analyst key stays in component state. The console renders the evidence returned by the API. |
| Denial of service | The HTTP server bounds header size and connection times. JSON write handlers limit request bodies, and review reads are paginated. | Compose publishes the console on loopback. Both proxies reject requests that fail their local browser checks. | The connection pool bounds concurrent database connections. Indexes support review and scoring queries. | Requests time out. Polling avoids overlapping reads and supports cancellation. |
| Elevation of privilege | Analyst credentials authorize review endpoints. The service credential authorizes scoring, statistics, and configuration reads. Configuration writes require an operator key. A reviewer can add a device block only by rejecting its pending case. | The proxy supplies the service key for its configured API routes. Configuration writes forward the operator key. The AWS gateway requires HTTPS and team authentication for the console. | Database grants apply to the configured role. The API role has explicit table privileges and lacks schema creation and migration permissions. | The API enforces permissions. Hiding a control in the browser has no effect on authorization. |

## Verification

- PostgreSQL integration tests verify that the API role can change configuration through the application and cannot read or alter audit records or create application tables.
- The backend tests cover duplicate credential headers, protected response headers, invalid and oversized blocklist bodies, distinct analyst keys, and review resolution.
- The Vite proxy integration test checks same-origin service access, analyst key forwarding, and rejection of requests with a foreign origin or host before they reach the backend.

## Operational follow-ups

- Limit administrator and log access, and define retention for purchase, device, and audit history.
- Add gateway rate limits and workload monitoring before serving untrusted traffic. Body size and connection limits bound individual requests.

## Simulation control

Simulation routes are absent unless `SIMULATION_ENABLED=true`. Starting and
stopping traffic requires an individual operator key; status and history use the
service key. Console proxies preserve these credentials and same-origin checks.
The public API hostname does not forward simulation routes. The controller uses
a fixed loopback scoring URL, bounded rate and backfill size, a database active-run
constraint and an advisory lock. History records the initiating and stopping
operators. An in-flight transaction marker allows recovery after a score commits
but before progress is checkpointed. An unresolved marker is shown explicitly;
recovery interrupts unfinished runs and does not resume traffic automatically.
