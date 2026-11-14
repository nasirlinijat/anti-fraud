# Development

## Frontend development

The frontend is a React and TypeScript app in `frontend/`, built with [Vite](https://vite.dev). Install Node.js 26 or newer with npm for local development.

1. Start the API and its dependencies with `docker compose up -d --build backend`, or use the host backend workflow (`make migrate`, then `make run`).
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

The development server forwards same-origin API requests to the backend. It reads `PORT` and `API_KEY` from the repository's `.env` file and adds the service key only to `POST /score`, after verifying the caller’s session with `GET /auth/me`. Missing or invalid sessions are rejected without scoring; an unavailable verification returns 503. Every other request carries the session token of the signed-in account. To use another backend, start the server with `BACKEND_URL=http://localhost:<port> npm run dev`.

| Command | What it does |
|---|---|
| `npm run dev` | Starts the development server with hot reload |
| `npm run check` | Runs the linter (oxlint), TypeScript type check, and proxy tests |
| `npm run build` | Type checks and builds the production files into `frontend/dist/` |

Browser checks and the XUI design decisions are documented in [frontend/docs/af-121-design.md](../../frontend/docs/af-121-design.md).

In the Docker stack, set `FRONTEND_PORT` in `.env` to change the console port. The console and the direct backend port listen on loopback. The proxy adds `API_KEY` only to `POST /score`, which the Tester page uses.

## Console pages

The console opens on a sign-in screen. Reviewers see only the review queue; admins see every page. The session is kept in the browser until you sign out or it expires after 12 hours.

| Page | What it does |
|---|---|
| Dashboard (`#/dashboard`) | Shows counts and rates by outcome, p95 scoring latency, blocked value in minor units, a scoring pipeline panel with the purchases each stage blocked, sent to review or let through, and a stacked decision timeline with a table view. Choose the last 15 minutes, hour, day, week, or all time. The dashboard also shows the current review queue, most fired rules, and model effects. It reads `GET /stats` every 5 seconds. Send traffic with the Tester page or `make simulate` to fill it. |
| Blocklist (`#/blocklist`) | Searches current card, device, player and IP entries by type and literal text in values or reasons. Search applies both filters and returns to page one; Clear filters restores the unfiltered first page. Bounded pages show matching counts and First/Previous/nearby/Next/Last controls. Adds and removes entries, with field validation and duplicate handling. A new entry applies to the next scored purchase. |
| Tester (`#/tester`) | A form with every score request field, including the payment verification results. It sends the purchase to `POST /score` and shows the outcome, the rule score, the model score and the fired rules with their points. Validation errors from the API are shown next to their fields. The buttons above the form fill in a normal purchase, a suspicious one, or nothing. |
| Simulator (`#/simulator`) | Starts live or backfill simulation runs, follows decisions every three seconds and shows recent runs. Admins can reveal finalized synthetic answers next to each saved scoring decision and filter for missed fraud or legitimate friction across the selected run. The blocked purchases panel shows why each purchase was blocked and links to its details. Runs continue across navigation, and history survives backend restarts. |
| Review queue (`#/reviews`) | Claim a purchase awaiting review, then approve or reject it; the reject confirmation offers to block the purchase's device, card and player as well. Or open a case to inspect its evidence, including the player's logins and password resets in the 30 days before the purchase. Events recorded after the decision are marked, because the rules could not have seen them. **My claims** lists only the purchases you hold. The queue shows the highest rule score first and refreshes every three seconds. The pager shows the current page, total pages and matching purchases. Use First, Previous, a nearby page number, Next, or Last. Changing the sort or claim filter returns to page one. With a model loaded, you can sort by model risk. In the linked players, cards and devices panel of a case, select a linked value to see its purchases, filter them by status, and open a linked purchase that is awaiting review. A resolved linked review opens read-only with its scoring evidence, analyst decision, and resolution time. Close it to return to the same connection. A case also lists the other linked purchases awaiting review, which can be claimed and decided together. The Fraud model panel reads `GET /model` when the queue opens. It shows the loaded version, active mode, effective thresholds, and ranking metrics from the evaluation dataset named in the report. It shows “Model is off” when the ruleset disables it or the backend has no model. |
| Rulesets (`#/rulesets`) | Shows the active version and lets you edit decision thresholds, rule weights, applicable limits, and the fraud model mode and thresholds. Each rule has a short description of what it checks. Publishing saves a new version with an optional note and shows its version number. Selecting an earlier version in the history shows its settings read-only. |
| Assignments (`#/assignments`) | Admins see each reviewer's open claims and decided count, and release a claim to return a purchase to the shared queue. |
| Accounts (`#/accounts`) | Admins list accounts, create reviewer or admin accounts, and disable an account, which ends its sessions. |

After approving or rejecting from a review case, select **View decision** to reopen it. The original case link (`#/reviews/{purchase_id}`) shows the saved decision, analyst, resolution time, and read-only evidence whenever the case has been resolved. **Back to the queue** restores your claim filter, sort order, and page in the same tab. If the queue has shrunk, it opens the last available page. The case groups saved rules, model output, and payment verification under **Evidence at scoring**. Purchase history, account events, and available links appear under **Investigation context**. If the saved score has no model reasons, the case reports that they are unavailable.

Select **Card velocity** in **Fired rules** to open Evidence X-Ray. The panel shows the saved count, rule limit, and one-hour window. **At scoring** lists only verified contributors. **What we know now** also shows recent same-card purchases in that window as investigation context, with a separate label for records saved after scoring. Older decisions without references show their saved rule message. To run the deterministic browser demo and capture its screenshot, use `cd frontend && npm run build && npm run test:browser -- --project=chromium card-velocity.spec.ts`.

## API documentation

The scoring, account event, dashboard, blocklist, model status, review queue, and ruleset APIs are described in `openapi.yaml`. It documents their requests, responses, and credentials.

To read it in Swagger UI, run `make docs`, then open http://localhost:8090 and stop the server with Ctrl+C. To use another port, set `SWAGGER_PORT` in `.env`. Swagger UI only shows the API. Its "Try it out" button cannot call the backend from the browser, because the backend does not allow cross-origin requests. Use `curl` instead.

When a documented API request or response changes, update `openapi.yaml` in the same pull request.

### Blocklist search and pages

The console always sends `GET /blocklist?page=1&page_size=25`, optionally with `type` and `q`. Type accepts `card`, `device`, `player`, or `ip`; omitted/empty means all types. Text is a literal substring of value OR reason, combined with type using AND. PostgreSQL lowercases both sides under the database locale. `%`, `_`, backslash and regex punctuation are ordinary text. Search does not fold accents or normalize Unicode.

The API validates supplied text before trimming surrounding spaces: at most 200 Unicode characters and 800 decoded UTF-8 bytes, valid UTF-8 without control characters. An empty query disables text filtering. `page` is a positive signed-64-bit decimal integer; `page_size` is 1–100, default 25. Reject repeated/unknown parameters, malformed URL encoding, and an encoded query string over 4096 bytes with 400 and field errors. Any supported parameter opts into paging; omitting all parameters preserves the complete legacy `{entries:[...]}` response.

Paged responses contain `entries` and `pagination: {page,page_size,total_pages,total_items}`. Counts cover all matching entries before pagination. Rows are ordered by `created_at DESC, id DESC`. The read-only repeatable-read transaction keeps count and rows consistent; a store already bound to a transaction uses its caller's transaction semantics. Valid out-of-range pages clamp to the last page before calculating the offset; zero matches returns an empty array and page 1 of 1. Separate requests observe current saved state.

The page retains applied filters on pagination, resets to page one on Search/Clear or a saved addition, and reconciles removals using the server's clamped page. It cancels obsolete reads and invalidates prior results after writes. A failed refresh offers retry without restoring an invalidated list. Uncertain writes trigger reconciliation without a success claim. Unfiltered zero entries and filtered zero matches have different messages. Configuration history remains a separate feature.

## Database queries

The backend runs SQL directly with [pgx](https://github.com/jackc/pgx). Each feature package keeps its queries as string constants next to the store code that runs them, with numbered parameters (`$1`, `$2`, ...):

```go
const getActiveRuleset = `
SELECT version, review_threshold, block_threshold
FROM rulesets
WHERE is_active`

err := conn.QueryRow(ctx, getActiveRuleset).Scan(&rs.Version, &rs.ReviewThreshold, &rs.BlockThreshold)
```

Pass nil pointers for NULL values and scan nullable columns into pointers. A query on a new table needs its migration first. A query on the scoring or dashboard path should be added to the index check in `backend/internal/queryplans/`, which fails when PostgreSQL would answer it with a sequential scan.

## Saved decision status

The service-authenticated `GET /decision-status?transaction_id=...` reads minimal saved status. It has no console UI or proxy credential injection. Public routing is explicitly allowed in deploy/Caddyfile and deploy/Dockerfile.edge includes it in the generated public API reference. The integration guide documents polling and fulfillment responsibilities.
