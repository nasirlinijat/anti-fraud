# Evaluation Labels, Chargebacks, and Decision Quality

## Private evaluation labels

Scored simulator runs now require `LABELS_DATABASE_URL` pointing to the **same PostgreSQL database as the score API**, using the separate `antifraud_labels` role. Set `LABELS_DB_PASSWORD` in `.env` and rerun `make migrate` to provision it. Compose supplies the container connection; host CLI commands use the connection in `.env.example`. Scoring-only installations can leave evaluation credentials unset while `SIMULATION_ENABLED=false`. Never use the database owner for ingestion.

Both CLI and console simulations store private pending metadata before sending a purchase, then its label after the decision is saved. Console label writes and progress checkpoints commit together. A persistence failure stops traffic and leaves pending metadata recoverable; score requests never contain scenario or fraud answers. Dump-only runs need neither a database nor an API key.

After a crash, recover saved pending decisions without resending traffic (from `backend`, with `LABELS_DATABASE_URL` set):

```bash
go run ./cmd/import-labels -run my-run -recover
```

Load the IEEE-CIS replay's existing labels file after replay has saved decisions:

```bash
go run ./cmd/import-labels -run ieee-replay-1 -source ieee-cis -file ../ml/output/replay/labels.csv
```

The CSV has `transaction_id,is_fraud`, where the flag is exactly 0 or 1. For simulator replays, use `-source simulator` and the additional `scenario` column now written by `replay.py --requests`. Repeating the same import is safe; contradictory answers, different run assignments, duplicate file IDs, malformed rows and unscored transactions fail the complete import without partial writes. Keep run IDs stable for retries. Old console runs have no recoverable answer key and remain unlabelled. No historical answers are guessed.

Raw and replay-derived label files stay in ignored `ml/data/` or `ml/output/` and must never be committed. The public API and model inputs do not expose labels.

Pending simulator answers now include a private purchase snapshot, normalized to scoring's string, IP and timestamp conventions. Recovery requires the saved purchase to match that snapshot, not just its ID. Definitive HTTP rejections and snapshot mismatches are quarantined. Legacy intents without snapshots are not automatically labelled; verify their source before any explicit CSV import.

The [demo seed command](simulator.md#seed-the-demo) reads only the finalized labels of its own simulator run after scoring completes. It uses them to submit ordinary analyst decisions through the review API. The API receives the decision and analyst credential, and the evaluation role retains the labels.

## Chargebacks (mocked)

A real merchant learns that a purchase was fraud only when the cardholder's bank disputes it: the processor sends a chargeback days or weeks later. The backend receives these on `POST /chargebacks`, a signed webhook, and stores them as training labels. In this project the processor is mocked by `cmd/mock-bank`, which charges back simulator purchases. Chargebacks never change a decision and are never read by scoring, model inputs, rules, or reviews.

Set `CHARGEBACK_WEBHOOK_SECRET` to a random value of at least 32 bytes that differs from `API_KEY`, for example `openssl rand -hex 32`, and restart the backend. While it is empty, the webhook returns 503. The sender signs `<X-Signature-Timestamp>.<raw body>` with HMAC-SHA256 and sends `X-Signature: sha256=<hex>`, where the timestamp is Unix seconds in plain digits; the backend rejects a wrong signature, or a timestamp more than 5 minutes from its clock, with 401. The body, reason codes, idempotency, and responses are described in `openapi.yaml`.

| Reason code | Counts as fraud in training labels |
|---|---|
| `fraud_card_not_present` | Yes |
| `fraud_card_absent_other` | Yes |
| `not_recognised` | No: often a relative or a forgotten purchase |
| `product_not_received`, `duplicate`, `other` | No: service disputes |

### Mock bank

After a scored simulator run, the mock bank reads the run's charged purchases (allowed, or reviewed and approved) and their private scenarios through the evaluation role, decides which ones cardholders dispute and when the bank receives each dispute, and posts the chargebacks received by the as-of time to the webhook. The API process never gets access to the scenarios. Run it from `backend` with `LABELS_DATABASE_URL` and `CHARGEBACK_WEBHOOK_SECRET` set:

```bash
go run ./cmd/mock-bank -run my-run -seed 1
```

| Option | Default | Meaning |
|---|---|---|
| `-run` | none | Simulator run ID whose purchases are charged back |
| `-seed` | 1 | Random seed for which purchases are disputed and when |
| `-as-of` | now | Sends only the chargebacks received by this RFC 3339 time. It cannot be in the future |
| `-url` | `http://localhost:$PORT` | Base URL of the backend |
| `-labels-database` | `$LABELS_DATABASE_URL` | Evaluation database |

A purchase's chargeback, its ID, and its receive time depend only on the seed and its transaction ID, so running the mock bank again later with a later `-as-of` resends the earlier chargebacks, which the webhook answers with 200, and adds the ones that have arrived since. Running it with a different `-seed` creates new chargeback IDs, so a purchase can get a second chargeback; the labels stay correct, because a purchase counts as fraud once it has any fraud chargeback. Chargebacks arrive late, so a backfill needs simulated time well in the past, for example:

```bash
go run ./cmd/simulator -count 3000 -gap 1h -suspicious 0.3 -rate 0 -start 2026-06-01T00:00:00Z -run cb-demo
go run ./cmd/mock-bank -run cb-demo
```

The rates and delays are illustrative, not measured:

| Pattern | Share charged back | Reasons | Median delay |
|---|---|---|---|
| `stolen_card` | 70% | `fraud_card_not_present` | 25 days |
| `new_account_spend` | 60% | `fraud_card_not_present` | 25 days |
| `device_farm` | 50% | `fraud_card_not_present` | 25 days |
| `low_and_slow` | 45% | `fraud_card_not_present` | 35 days |
| `account_takeover` | 30% | `fraud_card_absent_other` 60%, `not_recognised` 40% | 30 days |
| `quiet_takeover` | 20% | `fraud_card_absent_other` 60%, `not_recognised` 40% | 35 days |
| `card_testing`, `slow_card_testing` | 3% | `fraud_card_not_present` | 20 days |
| `family_card` | 3% | `not_recognised` 70%, `fraud_card_not_present` 30% | 40 days |
| Every other legitimate pattern | 0.4% | `not_recognised` 40%, `fraud_card_not_present` 25%, `product_not_received` 15%, `duplicate` 10%, `other` 10% | 40 days |

Delays are log-normal around the median and limited to 2 to 120 days, about the longest time networks allow for a dispute. Chargebacks for the full purchase amount are the only kind the mock bank sends. Legitimate purchases disputed with a fraud code are friendly fraud, and fraud disputed as `not_recognised` or never disputed is missed fraud: the labels a real business trains on are noisy in both directions. The private evaluation labels remain the exact answers used to measure decision quality; chargebacks are what a business would actually see. Turning chargebacks into a model is described in [ml/README.md](../../ml/README.md#retrain-from-chargebacks-mocked).

## Run decision quality

The Dashboard's Decision quality panel reports saved scoring outcomes against finalized known answers. Choose **Simulator** for console, CLI or imported simulator runs, or **IEEE-CIS replay** for imported dataset runs, then select a run. The two sources remain separate even when they use the same run ID. The panel refreshes every five seconds independently of the dashboard period.

Review and block count as flagged. Precision is the fraudulent share of flags, recall is the share of fraud flagged, and false-positive rate is the flagged share of legitimate purchases. A missing denominator is shown as Not applicable. Fraud value flagged includes reviews; it does not mean money recovered or saved. Analyst review decisions do not change these scoring-quality measurements.

`GET /stats/quality?source=simulator&run_id=<id>` returns the aggregate report. `source=ieee-cis` selects replay labels. `GET /stats/quality/runs?source=simulator` lists run IDs, with a bounded `limit` and exclusive `after` cursor for more. Both require an admin session and require a configured `LABELS_DATABASE_URL` with the restricted evaluation role. Quality reads work with `SIMULATION_ENABLED=false`; missing or unavailable evaluation access returns 503 without disabling scoring. The console proxy forwards these routes; the public production API host does not.

Only finalized answers contribute to metrics. Pending and quarantined intents are counted separately and never treated as legitimate purchases. Console runs show their recorded progress; CLI and replay runs have unknown total coverage because ingestion does not store an expected run size. No historical answers are guessed. Labels finalized before purchase verification was introduced need independent source verification; use fresh runs for acceptance comparisons.

Demo reset removes finalized, pending, and quarantined evaluation state together
with all purchases and simulation history. See [reset instructions](simulator.md#reset-demonstration-data).
