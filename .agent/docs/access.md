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

Sign in with `POST /auth/login` and send the returned token as `Authorization: Bearer <token>`. A session lasts 12 hours, and `POST /auth/logout` ends it. After 5 sign-in attempts for a username within 15 minutes, further attempts are rejected until the 15 minutes have passed. The attempt count is kept in memory, so a restart clears it and each backend process counts on its own.

| Role | Can use |
|---|---|
| `reviewer` | The review endpoints and `GET /model` |
| `admin` | The review endpoints, `GET /model`, and every statistics, quality, ruleset, blocklist and simulation endpoint, including changes |

A reviewer or admin claims a purchase with `POST /reviews/{purchase_id}/claim` before deciding it, so that two people never work on the same purchase. Only the account holding the claim can decide it. `GET /reviews?claimed=mine` lists your own claims, and every queue item shows who has claimed it. Analyst keys cannot claim and decide without a claim, as before.

A case lists the other purchases awaiting review that share its device, card or player (`GET /reviews/{purchase_id}/network/reviews`), so that a ring can be handled together. `POST /reviews/claims` claims up to 50 of them and, like the single claim, needs a session. `POST /reviews/decisions` decides up to 50 with one decision and follows the single decision's rules: a session decides only the purchases it has claimed, an analyst key decides without a claim, and the block fields cover only each purchase's own device, card and player. Each purchase is still decided separately, in its own transaction, with one decision per purchase, so a purchase someone else decided meanwhile is reported as `already_decided` and does not undo the others.

`GET /reviews/{purchase_id}/resolved` returns the scoring evidence and saved analyst decision for a resolved review. It accepts the same reviewer and admin sessions and analyst keys as the review queue.

A reviewer, or an analyst key, who rejects a purchase can also blocklist that purchase's own device, card and player, in the same transaction as the rejection (`block_device`, `block_card` and `block_player` on `POST /reviews/{purchase_id}/decision`). That is the only blocklist change a reviewer or analyst key can make: it covers only the values of the purchase being rejected, a session needs the claim on it, and each entry's reason names who rejected it. Every other blocklist change needs an admin session or an operator key.

Admins see what each reviewer is working on with `GET /admin/assignments`: every active reviewer, and any other account still holding a claim, with its open claims and the number of purchases it decided. An admin returns a stuck purchase to the shared queue with `DELETE /reviews/{purchase_id}/claim`, for example when a reviewer is away. Both endpoints need an admin session; no key works for them.

Other endpoints answer a valid session of the wrong role with 403. Review decisions and configuration changes save the username. `POST /score` accepts only the service key. A request with an `Authorization` header is checked as a session and its `X-API-Key` is ignored; requests without one still use the keys described below, until the console signs in.

## Analyst keys

Generate a distinct key for each analyst with Docker:

```bash
docker run --rm node:26-bookworm-slim node -e "console.log(require('node:crypto').randomBytes(32).toString('hex'))"
```

Replace the placeholders in `REVIEW_ANALYST_KEYS` in your local `.env` and give each analyst their own key.

```dotenv
REVIEW_ANALYST_KEYS=[{"analyst_id":"analyst-one","key":"<generated-key-one>"},{"analyst_id":"analyst-two","key":"<generated-key-two>"}]
```

Run `docker compose up -d --wait` to apply the updated environment, or restart the host backend if you use `make run`. The console does not use analyst keys: it signs in with an account. An analyst key works for API calls to the review endpoints and `GET /model`, sent in the `X-API-Key` header. Keep the configured keys out of Git. Each analyst key must be distinct from `API_KEY`. Leaving `REVIEW_ANALYST_KEYS` empty disables review access while the other APIs remain available.

## Operator keys and database roles

Configure `OPERATOR_API_KEYS` in `.env` to allow ruleset publishing and blocklist changes. Each operator needs a unique key distinct from the service and analyst keys. Generate keys with `openssl rand -hex 32`. The console does not use operator keys: an admin session publishes rulesets and changes the blocklist. An operator key works only for direct API calls, sent in the `X-API-Key` header.

```dotenv
OPERATOR_API_KEYS=[{"operator_id":"operator-1","key":"replace-with-an-individual-key"}]
```

When upgrading an existing Compose installation, add a separate `RUNTIME_DB_PASSWORD` to `.env` before starting the stack. Keep the existing `POSTGRES_USER` and `POSTGRES_PASSWORD` for migrations and backups. If you run the backend on the host, update `DATABASE_URL` to use `antifraud_api` and the runtime password.

The [STRIDE threat model](../../docs/threat-model.md) describes the local deployment's trust boundaries, controls, and remaining requirements for shared access.
