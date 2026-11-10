# ML

Trains the fraud model on the IEEE-CIS dataset. Python is used only offline; the Go service loads the exported model.

## Setup

1. Download `train_transaction.csv` and `train_identity.csv` from the [IEEE-CIS competition](https://www.kaggle.com/competitions/ieee-fraud-detection/data) (accept the competition rules first) and put them in `ml/data/`. The identity file is joined automatically when it sits next to the transaction file. The `data/` folder is ignored by Git and must never be committed.
2. Create the environment from the `ml` folder:

   ```bash
   python -m venv .venv
   .venv/Scripts/pip install -r requirements.txt
   ```

## Train

```bash
.venv/Scripts/python train.py data/train_transaction.csv
```

It builds the features, trains a logistic regression baseline and LightGBM, chooses the model policy thresholds, and writes:

| File | Content |
|---|---|
| `backend/models/lgbm-ieee-v5.json` | LightGBM model dump for the Go service |
| `backend/models/lgbm-ieee-v5.golden.csv` | 2,000 synthetic feature rows (random values within realistic ranges, not dataset rows) with the expected model score, for the Go tests |
| `backend/models/lgbm-ieee-v5.report.json` | Metrics, thresholds and export checks |
| `backend/models/lgbm-ieee-v5-first.*` | The first-fraud model, trained on each customer's first fraud only |
| `backend/models/lgbm-ieee-v5-combined.*` | Both models with their percentile tables, the combined policy, golden rows and the comparison with the main model; the service loads this file |
| `ml/output/training_report.json` | Same report, kept locally |

Options: `--version` (model name), `--review-rate` (share of legitimate purchases the model may send to review, default 0.005) and `--max-missed-fraud` (share of fraud a cleared review may contain, default 0.02).

`--review-cost` switches `flag_at` to money: it picks the threshold that maximizes the fraud money caught minus the cost of every review, on the validation month. The report shows fraud money caught next to fraud count for every operating point and for the policy.

Effect on the test month with model v4 (85,430 purchases, 2,994 frauds worth 457,435 in total):

| `flag_at` chosen by | Purchases flagged | Reviews | Precision | Fraud caught | Fraud money caught |
|---|---|---|---|---|---|
| **Review rate 0.5% (default)** | 0.9% | 805 | 44% | 11.8% | **22.3%** |
| Review cost 20 | 4.1% | 3,471 | 27% | 30.9% | 36.6% |
| Review cost 10 | 18.2% | 15,557 | 13% | 67.5% | 72.0% |
| Review cost 5 | 25.2% | 21,508 | 10% | 75.4% | 77.7% |
| Review cost 1 | 63.2% | 54,002 | 5% | 95.3% | 96.1% |

The default stays the review rate. Fraud is worth about 150 on average, so at any realistic review cost the money rule asks analysts to check thousands of purchases a month, far more than a review team can handle; the review rate keeps the queue at a size people can work through. The money view still shows the model's value: at the default the flagged frauds are larger than average (288 against 153), so 11.8% of the frauds carry 22.3% of the fraud money. Use `--review-cost` when the team's review cost and capacity are known.

The export fails if the model uses a split type the Go reader does not support, or if a plain tree reader does not reproduce LightGBM's scores on the test rows. Change `--version` whenever the feature list changes, so the service never loads a model with the wrong inputs.

## Compare feature groups

```bash
.venv/Scripts/python experiment.py data/train_transaction.csv
```

Trains one model per feature set (base, base plus each group, all groups, all minus each group) and prints test ROC-AUC, PR-AUC, first-fraud PR-AUC and precision at fixed false-positive rates. Groups are defined in `FEATURE_GROUPS` in `features.py`; `SELECTED_GROUPS` picks the ones the model uses. Results are written to `output/experiments.json`.

## Replay through the score API

`replay.py` sends IEEE-CIS purchases to a running backend through `POST /score`, in time order and in the API format, and compares the live decisions with Python. Fraud labels never reach the service; they stay in the script and in the report.

The live features count every purchase already saved, so the backend must start from an empty database. To keep your development data, use a separate database:

```bash
docker compose exec db createdb -U antifraud antifraud_replay
docker compose run --rm migrate -path=/migrations "-database=postgres://antifraud:antifraud@db:5432/antifraud_replay?sslmode=disable" up
```

Then run a second backend on it from `backend` (PowerShell):

```powershell
$env:PORT="8082"; $env:API_KEY="replay-key"; $env:MODEL_PATH="models/lgbm-ieee-v5.json"; $env:DATABASE_URL="postgres://antifraud:antifraud@localhost:5432/antifraud_replay?sslmode=disable"; go run ./cmd/api
```

Use the `POSTGRES_PORT` from `.env` in `DATABASE_URL`. Then, from `ml`:

```bash
.venv/Scripts/python replay.py data/train_transaction.csv --url http://localhost:8082 --api-key replay-key
```

By default it replays days 123 to 183: 30 days that build the history, then the test month (days 153 to 183). Add `--limit 3000` for a quick check. A full run sends about 170,000 purchases and takes about 45 minutes on a laptop. To run again, empty the tables first with `TRUNCATE decisions, purchases, feature_counts` in that database.

It writes to `output/replay/`:

| File | Content |
|---|---|
| `replay_report.json` | Live vs Python score differences, test month quality of the live model score, rules only vs rules and model, and latency |
| `replay_rows.csv` | One row per purchase: label, Python score, live score, outcome, rule score and latency |

The Python score comes from the same features computed on the same replayed purchases and the exported model, so it should equal the live score up to float rounding. Because the replay starts with only 30 days of history, the test month scores are lower than in the training report, which uses the history from day 1.

### Results (days 123 to 182, run on 2026-11-09)

| Check | Result |
|---|---|
| Purchases sent | 170,474 in 37.5 minutes, none failed |
| Live score equals Python score (within 1e-9) | 170,465 of 170,474 (99.995%) |
| Largest score difference | 0.0008, on 9 purchases |
| Same flag decision as Python | 100% |
| Latency p50 / p95 / p99 | 11.2 / 24.6 / 39.7 ms |

The 9 differences all come from `player_avg_amount`. The service computes it from whole cents (57.95), while `features.py` adds float amounts (57.950000000000045). The model has a split at that value, so one tree takes the other branch. No decision changed. Computing the average from `amount_minor` in `features.py` and retraining would remove the difference.

With model v4 (2026-11-10, first 30,000 purchases from day 123): the live score equals the Python score within 1e-9 for every purchase (largest difference 1.1e-16), with the same flag decision for all, and latency p50 / p95 / p99 of 7.6 / 18.0 / 25.9 ms. The v3 float-average differences are gone because both sides now sum minor units.

Test month (days 153 to 182, 85,430 purchases, 3.5% fraud), model v3:

| Decision | Sent to review | Fraud among them | Fraud stopped |
|---|---|---|---|
| Rules only | 0.14% | 4.2% | 0.17% |
| Rules and model | 2.25% | 26.4% | 17.0% |

The live model score has ROC-AUC 0.829 and PR-AUC 0.188, the same as Python. Rules alone stop almost nothing here, because IEEE-CIS has no IP country or account creation time, so two of the four rules never fire. The model flags 2.1% of purchases instead of the 1% expected from training, because its history features see only 30 days.

## Train on the service's own data

The service saves the 48 model inputs with every decision. Training can use them instead of the IEEE-CIS CSV, so a model learns from real traffic and is guaranteed to see exactly the values the service computes.

1. Export the saved inputs (from `backend`):

   ```bash
   go run ./cmd/export-inputs -database "$DATABASE_URL" -out model_inputs.csv
   ```

   One row per decision, in event time order: `transaction_id`, `event_time`, `player_id`, `model_score`, then the features. Decisions saved for another feature list (an older model) are skipped and counted.

2. Prepare a labels file kept outside the service: `transaction_id,is_fraud`. For the IEEE-CIS replay, `replay.py` writes `labels.csv` next to its report. For live traffic it comes from chargebacks and analyst decisions; analyst decisions are only ever labels, never model inputs, and the scoring code never reads this file.

3. Train (from `ml`):

   ```bash
   .venv/Scripts/python train.py --inputs model_inputs.csv --labels labels.csv --train-days 123-150 --validation-days 151-165 --test-days 166-183
   ```

   `day` counts whole days since `--day-zero` (the replay's start by default). Purchases without a label are left out.

Check (2026-11-10): 30,000 purchases replayed through the service and exported matched the Python features exactly on all 48 columns, and training from the export produced a model file identical to training from the Python features (test ROC-AUC 0.818, days 123-128 / 129-130 / 131-132).

## Tests

```bash
.venv/Scripts/python -m pytest tests
```

The tests use synthetic purchases in `testdata/`, so they run without the dataset. They check exact feature values, that adding later purchases never changes earlier features, the window boundaries, the time split and the model export. After changing a feature, regenerate the expected values with `python make_fixture.py` and check the differences.

The exact feature definitions for the Go service are in `FEATURES.md`.

## Features

All history features use only transactions that happened before the current one, so the scoring service can compute the same values at request time. Anonymized IEEE-CIS columns (`C`, `M`, `V` and most `D` and `id_*`) are never model inputs. The exceptions map to score request fields: `D1` (days since the client began) gives the account age, and `id_30`, `id_31` and `id_33` hold the client's operating system, browser and screen.

| Feature | Meaning |
|---|---|
| `amount`, `cents` | Purchase amount and its fractional part |
| `product_code`, `card_network`, `card_type` | Product code, card network and card type as small integer codes |
| `card_country`, `card_category`, `billing_country` | Card and billing codes from the request |
| `email_domain_share` | Share of earlier purchases that used this email domain |
| `email_players_share` | Share of earlier players who used this email domain |
| `has_recipient_email`, `recipient_email_differs` | Whether a recipient email is set and differs from the purchaser's |
| `player_txn_count`, `player_avg_amount`, `player_max_amount`, `amount_to_avg` | Player's earlier purchases and amounts |
| `secs_since_last`, `secs_since_first` | Time since the player's previous and first purchase |
| `player_uses_1h`, `player_uses_24h`, `card_uses_1h`, `card_uses_24h` | Earlier purchases by the player or card in the last hour and day |
| `player_same_amount_count` | Earlier purchases by the player with the same amount |
| `player_distinct_cards`, `player_distinct_emails`, `email_is_new_for_player` | Cards and email domains the player used before |
| `has_device_info`, `device_type` | Whether device data was sent, and desktop or mobile |
| `device_info_share`, `device_players_share` | How common the device name is among earlier purchases and players |
| `device_is_new_for_player`, `player_distinct_devices` | Device names the player used before |
| `card_distinct_players` | Players who used this card before |
| `card1_distinct_players`, `card1_distinct_addresses`, `card1_distinct_emails` | Players, billing regions and emails seen with this `card1` value before |
| `billing_distance` | Distance field from the request (`dist1`) |
| `account_age_days` | Days the account existed before the purchase (`D1`) |
| `has_client_info`, `os_family`, `browser_family`, `screen_area` | Client details sent, OS and browser family, screen size |
| `os_share`, `browser_share`, `screen_share` | How common the OS, browser and screen are among earlier purchases |
| `os_is_new_for_player`, `browser_is_new_for_player`, `player_distinct_browsers` | OS and browsers the player used before |

Player ID: `card1 + addr1 + (day - D1)`. Card ID: `card1` to `card6` combined. Missing values are `-1`. Money-velocity features (amount spent per card or player in a time window) were tested and left out because they did not improve the model. `hour_of_day` was removed in v4: IEEE-CIS times start at an unknown moment, so the hour is shifted for live traffic, and the model does not lose anything without it. Lifetime counts and ages (for example a card's total purchases) were tested and left out because they keep growing over the months and drift.

### Model v5: card issuer and billing region codes

v5 adds `card_issuer_code` (`card1`) and `billing_region_code` (`addr1`) as numbers. The 1st-place IEEE-CIS solution relied on these columns; until v4 the model only saw counts built from them. Codes for the email domain, device, OS and browser were tested too and gave no gain.

| Test month | v4 | v5 |
|---|---|---|
| Main model ROC-AUC | 0.838 | **0.852** |
| Main model PR-AUC | 0.231 | **0.245** |
| Main model first-fraud PR-AUC | 0.126 | **0.133** |
| Precision at 0.1% of legit flagged | 58% | **60%** |
| Precision / fraud caught at 1% | 37.7% / 16.7% | **38.3% / 17.1%** |
| Combined policy: flags, precision, fraud caught | 0.96%, 44%, 12.0% | 0.93%, 43%, 11.6% |
| Combined policy: reviews cleared, fraud missed by clearing | 19.5%, 1.9% | **22.3%**, 1.9% |

### Blending with the player's earlier scores

The live, past-only version of the 1st-place solution's post-processing (which averaged each client's predictions). Before combining, the main model's score is blended with the average of the same player's earlier main-model scores: `0.5 * score + 0.5 * earlier average` (`--player-blend`, default 0.5; a player's first purchase keeps its score). The percentile table of the main model is built from blended validation scores, and the thresholds are chosen on the blended combined score. The service reads the earlier scores from the saved decisions (`model_score_unblended`, the main model's own score, so a blended score never feeds the next blend).

Blending the final combined score instead lowered precision, so the blend happens inside the combination.

| Test month, combined model | Without blending | With blending |
|---|---|---|
| PR-AUC | 0.233 | **0.240** |
| First-fraud PR-AUC | 0.191 | **0.200** |
| Fraud caught / precision at 1% review | 16.7% / 37.8% | **18.1% / 39.7%** |
| Fraud caught on a player's first purchase at 1% | 29.4% | **31.7%** |
| Precision at 0.5% / 0.1% review | 43.7% / 63.0% | 43.6% / 61.4% |

Replay check: 10,000 purchases scored exactly like Python, including the blend.

### Averaging 5 models

The main model is the average of 5 LightGBM models trained with different random seeds (`--models`, default 5). They are exported as one file: every tree of every model with its leaf values divided by 5, so the score is the sigmoid of the average raw score and the Go reader needs no change (4,700 trees, about 5 µs per purchase). The report keeps the first model alone as `single_model_test`.

| Test month, main model | One model | Average of 5 |
|---|---|---|
| PR-AUC | 0.245 | **0.247** |
| First-fraud PR-AUC | 0.133 | **0.138** |
| Precision at 0.5% of legit flagged | 45.7% | **46.1%** |
| Fraud caught / precision at 1% | 17.1% / 38.3% | **18.4% / 40.0%** |

Replay check: 10,000 purchases scored exactly like Python, p95 17.6 ms.

With the combination, the gain mostly shows as more reviews cleared for the same missed fraud. Replay check: 10,000 purchases through the service scored exactly like Python, p95 14.6 ms.

### Model v4 against v3 (test month, days 153-183)

| | v3 (38 features) | v4 (48 features) |
|---|---|---|
| ROC-AUC | 0.828 | **0.838** |
| PR-AUC | 0.204 | **0.231** |
| First-fraud-only PR-AUC | 0.116 | **0.126** |
| Fraud caught / precision at 0.5% of legit flagged | 9.0% / 39% | **11.4% / 45%** |
| Fraud caught / precision at 1% | 14.6% / 35% | **16.7% / 38%** |
| Policy `flag_at` on the test month: flagged, precision, fraud caught | 0.97%, 38%, 10.6% | 0.94%, **44%**, **11.8%** |

The Go service computes these features.

### First-fraud model and the combination

IEEE-CIS marks a customer's later purchases as fraud after a chargeback. The main model learns to recognise those customers; that helps on later purchases, which the blocklist also stops once fraud is confirmed, but not on new fraud. `train.py` also trains `lgbm-ieee-v5-first` on clean labels, where only each customer's first fraud counts.

The service loads `lgbm-ieee-v5-combined.json` by default: it scores every purchase with both models and keeps the higher percentile (the share of validation purchases each model scores lower, by more than 1e-12 so that a one-bit difference between Python and Go cannot change it). `flag_at` and `clear_below` are chosen on this combined score with the same review rate and missed-fraud limit. A percentile, not the raw score, is used because the two models score on different scales; counting strictly lower scores gives the first-fraud model's most common "nothing suspicious" score a percentile of 0, so it never blocks clearing.

Test month (days 153-183):

| | v4 alone | v4 + first-fraud model |
|---|---|---|
| Fraud caught on a player's first purchase, 1% review | 21.6% | **29.6%** |
| First-fraud-only PR-AUC | 0.126 | **0.184** |
| Fraud caught / precision, 1% review | 16.7% / 37.7% | 16.6% / 37.6% |
| Policy: flagged / precision / fraud caught | 0.94% / 44% / 11.8% | 0.96% / 44% / 12.0% |
| Policy: cleared / fraud missed by clearing | 20.5% / 1.7% | 19.5% / 1.9% |

The first-fraud model alone ranks overall fraud worse (ROC-AUC 0.66), so it is only used inside the combination. Replay check (2026-11-10, first 10,000 purchases from day 123): the live combined score equals Python exactly for every purchase, with the same flag decisions, and latency p50 / p95 / p99 of 8.6 / 20.3 / 34.3 ms.

## Evaluation

The split follows time, by dataset day: train 31-120, validation 136-152 (early stopping and thresholds), test 153-183. Days 1-30 only build up history and are not used as training rows, because at the start of the data nobody has history yet. The gap between train and validation stands in for the delay before fraud labels arrive.

## Policy thresholds

- `flag_at`: an allowed purchase with a model score at or above it goes to review. It is set so that about `--review-rate` of legitimate validation purchases are flagged.
- `clear_below`: a purchase the rules sent to review is cleared when its score is below it. It is set so that only `--max-missed-fraud` of validation fraud scores fall below it.

The model never blocks. Scores drift over time, so the thresholds should be checked against recent data before each release.

The report gives ROC-AUC, PR-AUC and the share of fraud caught, with precision, when 0.1%, 0.5%, 1% and 2% of legitimate purchases are flagged. It also reports "first fraud only": IEEE-CIS labels a player's later transactions as fraud after a chargeback, so those follow-up rows are removed to measure how well the model finds new fraud.
