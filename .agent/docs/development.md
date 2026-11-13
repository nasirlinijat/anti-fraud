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

The development server forwards same-origin API requests to the backend. It reads `PORT` and `API_KEY` from the repository's `.env` file and adds the service key only to `POST /score`. Every other request carries the session token of the signed-in account. To use another backend, start the server with `BACKEND_URL=http://localhost:<port> npm run dev`.

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
| Blocklist (`#/blocklist`) | Lists the blocked card tokens, device IDs, player IDs and IP addresses with their reasons, adds new entries and removes them. A new entry applies to the next scored purchase. Invalid values, a missing reason and duplicates are shown next to their fields. |
| Tester (`#/tester`) | A form with every score request field, including the payment verification results. It sends the purchase to `POST /score` and shows the outcome, the rule score, the model score and the fired rules with their points. Validation errors from the API are shown next to their fields. The buttons above the form fill in a normal purchase, a suspicious one, or nothing. |
| Simulator (`#/simulator`) | Starts live or backfill simulation runs, follows decisions every three seconds and shows recent runs. Runs continue across navigation, and history survives backend restarts. |
| Review queue (`#/reviews`) | Claim a purchase awaiting review, then approve or reject it, or open a case to inspect its evidence, including the player's logins and password resets in the 30 days before the purchase. Events recorded after the decision are marked, because the rules could not have seen them. **My claims** lists only the purchases you hold. The queue shows the highest rule score first and refreshes every three seconds. With a model loaded, you can sort by model risk. |
| Rulesets (`#/rulesets`) | Shows the active version and lets you edit decision thresholds, rule weights, and applicable limits. Each rule has a short description of what it checks. Publishing saves a new version with an optional note and shows its version number. Selecting an earlier version in the history shows its settings read-only. |
| Assignments (`#/assignments`) | Admins see each reviewer's open claims and decided count, and release a claim to return a purchase to the shared queue. |
| Accounts (`#/accounts`) | Admins list accounts, create reviewer or admin accounts, and disable an account, which ends its sessions. |

## API documentation

The scoring, account event, dashboard, blocklist, review queue, and ruleset APIs are described in `openapi.yaml`. It documents their requests, responses, and credentials.

To read it in Swagger UI, run `make docs`, then open http://localhost:8090 and stop the server with Ctrl+C. To use another port, set `SWAGGER_PORT` in `.env`. Swagger UI only shows the API. Its "Try it out" button cannot call the backend from the browser, because the backend does not allow cross-origin requests. Use `curl` instead.

When a documented API request or response changes, update `openapi.yaml` in the same pull request.

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
