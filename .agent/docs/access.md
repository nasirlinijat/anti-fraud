# Accounts, Keys, and Access

## Console accounts

Reviewers and admins sign in with their own account. Create the first admin from `backend` after the database is migrated, with `DATABASE_URL` from `.env`:

```bash
go run ./cmd/create-account -username alice
```

The command asks for the password twice without showing it. A password is 12 to 72 bytes long. In a setup script, pipe the password on standard input instead, so that it never appears in the shell history or process list:

```bash
printf '%s\n' "$ADMIN_PASSWORD" | go run ./cmd/create-account -username alice
```

| Option | Default | Meaning |
|---|---|---|
| `-username` | none | 3 to 64 letters, digits, dots, hyphens or underscores, starting with a letter or digit. Saved in lowercase |
| `-role` | `admin` | `admin` or `reviewer` |
| `-database` | `DATABASE_URL` | Database connection |

The command exits with an error when the username is already taken. After the first admin exists, admins manage accounts through the API: `POST /admin/accounts` creates a reviewer or admin, `GET /admin/accounts` lists them, and `POST /admin/accounts/{username}/disable` disables one and signs it out at once. An admin cannot disable their own account, so the last admin cannot lock everyone out.

Sign in with `POST /auth/login` and send the returned token as `Authorization: Bearer <token>`. A session lasts 12 hours, and `POST /auth/logout` ends it. The console checks the session every 60 seconds and when the tab regains focus or visibility, including on pages without data polling. A temporary check failure preserves the session; a 401 returns to sign-in. Responses from an older session cannot clear or restore a newer session. After 5 sign-in attempts for a username within 15 minutes, further attempts are rejected until the 15 minutes have passed. The attempt count is kept in memory, so a restart clears it and each backend process counts on its own.

| Role | Can use |
|---|---|
| `reviewer` | The review endpoints and `GET /model` |
| `admin` | The review endpoints, `GET /model`, and every statistics, quality, ruleset, blocklist and simulation endpoint, including changes |

`GET /blocklist` requires an admin session for both its complete legacy response and filtered/paginated responses. `POST /blocklist` and `DELETE /blocklist/{id}` require the same role. A valid reviewer receives 403; a service key alone or a missing/invalid session receives 401. Authorization happens before query validation. Paging uses the existing blocklist SELECT grant; it adds no audit-read privilege and never exposes configuration history.

A reviewer or admin claims a purchase with `POST /reviews/{purchase_id}/claim` before deciding it, so that two people never work on the same purchase. Only the account holding the claim can decide it. Claiming, releasing and deciding the same purchase share a transaction-level advisory lock, acquired before reading its current state. A decision that commits first preserves its claim; a release that commits first prevents the former holder from deciding until they claim again. The single-bigint advisory namespace is reserved for purchase IDs, separate from the two-integer ruleset and simulation locks. `GET /reviews?claimed=mine` lists your own claims, and every queue item shows who has claimed it.

A case lists the other purchases awaiting review that share its device, card or player (`GET /reviews/{purchase_id}/network/reviews`), so that a ring can be handled together. `POST /reviews/claims` claims up to 50 of them and, like the single claim, needs a session. `POST /reviews/decisions` decides up to 50 with one decision and follows the single decision's rules: a session decides only the purchases it has claimed, the block fields cover only each purchase's own device, card and player. Each purchase is still decided separately, in its own transaction, with one decision per purchase, so a purchase someone else decided meanwhile is reported as `already_decided` and does not undo the others.

`GET /reviews/{purchase_id}/resolved` returns the scoring evidence and saved analyst decision for a resolved review. It accepts the same reviewer and admin sessions as the review queue.

Open and resolved case reads can include the saved Card velocity observation, verified contributor details, and related investigation context. They use the existing case authorization. The `/score` response keeps the original fired-rule summary.

A reviewer who rejects a purchase can also blocklist that purchase's own device, card and player, in the same transaction as the rejection (`block_device`, `block_card` and `block_player` on `POST /reviews/{purchase_id}/decision`). That is the only blocklist change a reviewer can make: it covers only the values of the purchase being rejected, a session needs the claim on it, and each entry's reason names who rejected it. Every other blocklist change needs an admin session.

Admins see what each reviewer is working on with `GET /admin/assignments`: every active reviewer, and any other account still holding a claim, with its open claims and the number of purchases it decided. An admin returns a stuck purchase to the shared queue with `DELETE /reviews/{purchase_id}/claim`, for example when a reviewer is away. Both endpoints need an admin session; no key works for them.

Other endpoints answer a valid session of the wrong role with 403. Review decisions and configuration changes save the username. `POST /score` accepts only the service key. Console endpoints accept only `Authorization: Bearer <token>`. A key alone never grants console access. `POST /score` and `POST /account-events` accept only the service key.

## Database roles

When upgrading an existing Compose installation, add a separate `RUNTIME_DB_PASSWORD` to `.env` before starting the stack. Keep the existing `POSTGRES_USER` and `POSTGRES_PASSWORD` for migrations and backups. If you run the backend on the host, update `DATABASE_URL` to use `antifraud_api` and the runtime password.

The [STRIDE threat model](../../docs/threat-model.md) describes the local deployment's trust boundaries, controls, and remaining requirements for shared access.

When upgrading, verify an admin can sign in before removing obsolete console credentials from local environment files and deployment secrets. Existing accounts, sessions, claims, and audit records are retained.

## Saved decision status

`GET /decision-status?transaction_id=...` accepts only the deployment-wide service key. It returns original outcome, review status and UTC resolution time without analyst identity or investigation evidence. Existing runtime SELECT grants suffice; console endpoints remain session-only. There is no per-merchant ownership isolation.
