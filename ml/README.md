# ML

Trains the fraud model on the IEEE-CIS dataset. Python is used only offline; the Go service loads the exported model.

The service loads **`lgbm-ieee-v5-combined`** by default. [Current results](#current-results) describes how it performs; [History of changes](#history-of-changes) keeps the results of earlier versions.

## Setup

1. Download `train_transaction.csv` and `train_identity.csv` from the [IEEE-CIS competition](https://www.kaggle.com/competitions/ieee-fraud-detection/data) (accept the competition rules first) and put them in `ml/data/`. The identity file is joined automatically when it sits next to the transaction file. The `data/` folder is ignored by Git and must never be committed.
2. Create the environment from the `ml` folder:

   ```bash
   python -m venv .venv
   ```

   Then install the packages. On Linux and macOS:

   ```bash
   .venv/bin/pip install -r requirements.txt
   ```

   On Windows:

   ```powershell
   .venv\Scripts\pip install -r requirements.txt
   ```

The commands below use the Linux and macOS path `.venv/bin/python`. On Windows use `.venv\Scripts\python` instead.

## Train

```bash
.venv/bin/python train.py data/train_transaction.csv
```

It builds the features, trains a logistic regression baseline and LightGBM, chooses the model policy thresholds, and writes:

| File | Content |
|---|---|
| `backend/models/lgbm-ieee-v5.json` | Main model: the average of 5 LightGBM models, for the Go service |
| `backend/models/lgbm-ieee-v5.golden.csv` | 2,000 synthetic feature rows (random values within realistic ranges, not dataset rows) with the expected model score, for the Go tests |
| `backend/models/lgbm-ieee-v5.report.json` | Metrics of the baseline and the main model, thresholds and export checks |
| `backend/models/lgbm-ieee-v5-first.*` | The first-fraud model, trained on each customer's first fraud only |
| `backend/models/lgbm-ieee-v5-combined.*` | Both models with their percentile tables, the policy, golden rows and the results described below; the service loads this file |
| `ml/output/training_report.json` | Same report as the main model's, kept locally |

| Option | Default | Meaning |
|---|---|---|
| `--version` | `lgbm-ieee-v5` | Model name. Change it whenever the feature list changes, so the service never loads a model with the wrong inputs |
| `--review-rate` | 0.005 | Share of legitimate validation purchases the model may send to review; sets `flag_at` |
| `--max-missed-fraud` | 0.02 | Share of validation fraud that may score below `clear_below` |
| `--review-cost` | none | Sets `flag_at` by money instead: the threshold that maximizes the fraud money caught minus the cost of every review |
| `--target-precision` | none | Sets `flag_at` by precision instead: the lowest threshold whose flagged validation purchases reach the target |
| `--models` | 5 | Number of models averaged into the main model (1 to 5) |
| `--player-blend` | 0.5 | Weight of the player's earlier scores in the blend (0 turns it off) |
| `--inputs`, `--labels` | none | Train from the service's own saved inputs, see [Train on the service's own data](#train-on-the-services-own-data) |

All thresholds are chosen on the validation month only. The export fails if the model uses a split type the Go reader does not support, or if a plain tree reader does not reproduce LightGBM's scores on the test rows.

## How the default model scores a purchase

1. **Main model** (`lgbm-ieee-v5`): the average of 5 LightGBM models trained with different seeds, over the 50 features in [FEATURES.md](FEATURES.md).
2. **Player blend**: the main score is blended with the average of the same player's earlier main-model scores, `0.5 * score + 0.5 * earlier average`. A player's first purchase keeps its own score. The service reads the earlier scores from `decisions.model_score_unblended`, so a blended score never feeds the next blend.
3. **First-fraud model** (`lgbm-ieee-v5-first`): trained only on each customer's first fraud. IEEE-CIS marks a customer's later purchases as fraud after a chargeback; the main model learns to recognise those customers, and this model learns new fraud.
4. **Combination**: each score is turned into a percentile among the validation scores of the same model, and the higher percentile is the model score. The percentile counts validation scores lower by more than 1e-12, so a one-bit difference between Python and Go cannot change it.
5. **Policy**: an allowed purchase with a model score at or above `flag_at` goes to review; a purchase the rules sent to review is cleared when its score is below `clear_below`. The model never blocks.

## Current results

Model `lgbm-ieee-v5-combined`, policy from the default options (`flag_at` 0.994001, `clear_below` 0.391385). Test month: days 153-183, 85,430 purchases, 2,994 frauds (3.5%). The thresholds were chosen on the validation month, so these numbers estimate how the model does on new data.

| Policy on the test month | Result |
|---|---|
| Purchases flagged for review | 0.92% |
| Precision of the flags (share that is fraud) | 42.8% |
| Fraud caught by the flags | 11.3% of fraud cases, 17.7% of fraud money |
| Purchases scoring below `clear_below` | 21.4%, holding 1.9% of the fraud (0.3% fraud among them) |

The last row is measured on all purchases. In the service `clear_below` only applies to purchases the rules send to review, and how many of those it clears is not measured yet (AF-89, AF-90).

Ranking quality on the test month, which does not depend on a threshold:

| Test month | All fraud | First fraud only |
|---|---|---|
| ROC-AUC | 0.841 | 0.892 |
| PR-AUC | 0.240 | 0.200 |

"First fraud only" removes the purchases IEEE-CIS labels as fraud after a customer's first chargeback, to measure how well the model finds new fraud.

`--target-precision` gives these results on the test month (from `precision_targets` in the combined report; each `flag_at` is chosen on validation):

| Target precision | `flag_at` | Flagged per 10,000 purchases | Test precision | Fraud caught | Fraud money caught |
|---|---|---|---|---|---|
| 30% | 0.9789 | 347 | 29% | 29.1% | 35.7% |
| 40% | 0.9895 | 160 | 40% | 18.1% | 24.4% |
| 50% | 0.9943 | 89 | 43% | 11.0% | 17.0% |
| 60% | 0.9988 | 25 | 60% | 4.3% | 8.3% |
| 70% | 0.9994 | 15 | 64% | 2.8% | 6.1% |
| 80% | 0.9997 | 8 | 76% | 1.8% | 4.1% |
| 90% | 0.9999 | 4 | 95% | 1.2% | 2.7% |

Higher precision costs a lot of caught fraud. Above about 50% the validation month has few flagged purchases, so the test precision can land a few points under the target. A flag only sends a purchase to an analyst, so a lower precision costs review time, not blocked players.

The reports also list `operating_points`: fraud caught and precision when 0.1%, 0.5%, 1% and 2% of legitimate purchases are flagged. **Their thresholds are computed on the test month itself**, so they show how well the model ranks, not what a deployed threshold would do. Those numbers for the default model:

| Legitimate purchases flagged (threshold set on the test month) | 0.1% | 0.5% | 1% | 2% |
|---|---|---|---|---|
| Fraud caught | 4.2% | 10.5% | 18.1% | 25.6% |
| Precision | 61% | 44% | 40% | 32% |

The logistic regression baseline in `lgbm-ieee-v5.report.json`, on the same test month:

| Test month | Baseline, codes read as numbers (before AF-96) | Baseline, one-hot codes and missing indicators | Main LightGBM model |
|---|---|---|---|
| ROC-AUC | 0.784 | 0.789 | 0.851 |
| PR-AUC | 0.103 | 0.109 | 0.247 |
| First-fraud PR-AUC | 0.044 | 0.045 | 0.138 |
| Fraud caught at 1% of legit flagged (threshold on the test month) | 3.1% | 2.5% | 18.4% |

The report keeps both baselines (`logistic_regression` and `logistic_regression_all_numeric`). On validation the current baseline catches 13.2% of fraud at 1%, but on the test month only 2.5%: the lifetime counts listed under [Features](#features) keep growing, and a linear model extrapolates them. Without those six features the same baseline reaches test PR-AUC 0.126 and catches 7.9% at 1% (one-off check, 2026-11-10), which supports replacing them (AF-95).

Most model variants in [History of changes](#history-of-changes) were compared on the test month, so the test month is no longer a fully unbiased estimate, and gains of a few thousandths of PR-AUC are within noise for about 3,000 frauds. Moving these comparisons to validation folds is tracked in AF-94.

## Replay through the score API

`replay.py` sends IEEE-CIS purchases to a running backend through `POST /score`, in time order and in the API format, and compares the live decisions with Python. Fraud labels never reach the service; they stay in the script and in the report.

The live features count every purchase already saved, so the backend must start from an empty database. To keep your development data, use a separate database:

```bash
docker compose exec db createdb -U antifraud antifraud_replay
docker compose run --rm migrate -path=/migrations "-database=postgres://antifraud:antifraud@db:5432/antifraud_replay?sslmode=disable" up
```

On Windows Git Bash, prefix the second command with `MSYS_NO_PATHCONV=1` so `/migrations` is not turned into a Windows path.

Then run a second backend on it from `backend`, with the `POSTGRES_PORT` from `.env` in `DATABASE_URL` (5432 by default). On Linux and macOS:

```bash
PORT=8082 API_KEY=replay-key MODEL_PATH=models/lgbm-ieee-v5-combined.json DATABASE_URL="postgres://antifraud:antifraud@localhost:5432/antifraud_replay?sslmode=disable" go run ./cmd/api
```

On Windows PowerShell:

```powershell
$env:PORT="8082"; $env:API_KEY="replay-key"; $env:MODEL_PATH="models/lgbm-ieee-v5-combined.json"; $env:DATABASE_URL="postgres://antifraud:antifraud@localhost:5432/antifraud_replay?sslmode=disable"; go run ./cmd/api
```

Then, from `ml`:

```bash
.venv/bin/python replay.py data/train_transaction.csv --url http://localhost:8082 --api-key replay-key
```

By default it replays days 123 to 183: 30 days that build the history, then the test month (days 153 to 183). Add `--limit 10000` for a quick check. A full run sends about 170,000 purchases and takes about 40 minutes on a laptop. To run again, empty the tables first with `TRUNCATE review_resolutions, decisions, purchases, feature_counts` in that database.

It writes to `output/replay/`:

| File | Content |
|---|---|
| `replay_report.json` | Live vs Python score differences, test month quality of the live model score, rules only vs rules and model, and latency |
| `replay_rows.csv` | One row per purchase: label, Python score, live score, outcome, rule score and latency |
| `labels.csv` | `transaction_id,is_fraud` for training from the service's own data |

The Python score comes from the same features computed on the same replayed purchases and the exported model, so it should equal the live score up to float rounding.

**Latest check, `lgbm-ieee-v5-combined` with the player blend (2026-11-10, first 10,000 purchases from day 123):** the live score equals the Python score for every purchase (largest difference 0.0), with the same flag decision for all, and latency p95 of 39.8 ms.

Because a replay starts with only 30 days of history, the test month scores differ from the training report, which uses the history from day 1. Some history features are lifetime counts, so the share of purchases the model flags depends on how long the database has collected history (AF-95). The full-month replay results under [History of changes](#history-of-changes) are for model v3.

## Train on the service's own data

The service saves the model inputs with every decision. Training can use them instead of the IEEE-CIS CSV, so a model learns from real traffic and is guaranteed to see exactly the values the service computes.

1. Export the saved inputs (from `backend`):

   ```bash
   go run ./cmd/export-inputs -database "$DATABASE_URL" -out model_inputs.csv
   ```

   One row per decision, in event time order: `transaction_id`, `event_time`, `player_id`, `model_score`, then the features. Decisions saved for another feature list (an older model) are skipped and counted.

2. Prepare a labels file kept outside the service: `transaction_id,is_fraud`. For the IEEE-CIS replay, `replay.py` writes `labels.csv` next to its report. For live traffic it comes from chargebacks and analyst decisions; analyst decisions are only ever labels, never model inputs, and the scoring code never reads this file.

3. Train (from `ml`):

   ```bash
   .venv/bin/python train.py --inputs model_inputs.csv --labels labels.csv --train-days 123-150 --validation-days 151-165 --test-days 166-183
   ```

   `day` counts whole days since `--day-zero` (the replay's start by default). Purchases without a label are left out.

Check (2026-11-10, model v4): 30,000 purchases replayed through the service and exported matched the Python features exactly on all 48 columns, and training from the export produced a model file identical to training from the Python features.

## Compare feature groups

```bash
.venv/bin/python experiment.py data/train_transaction.csv
```

Trains one model per feature set (base, base plus each group, all groups, all minus each group) and prints ROC-AUC, PR-AUC, first-fraud PR-AUC and precision at fixed false-positive rates. Groups are defined in `FEATURE_GROUPS` in `features.py`; `SELECTED_GROUPS` picks the ones the model uses. Results are written to `output/experiments.json`. It currently compares on the test month, with thresholds computed on the test month (AF-94).

## Tests

```bash
.venv/bin/python -m pytest tests
```

The tests use synthetic purchases in `testdata/`, so they run without the dataset. They check exact feature values, that adding later purchases never changes earlier features, the window boundaries, the time split, the baseline's handling of missing values and the model export. After changing a feature, regenerate the expected values with `python make_fixture.py` and check the differences.

## Features

The exact feature definitions for the Go service are in [FEATURES.md](FEATURES.md).

All history features use only transactions that happened before the current one, so the scoring service can compute the same values at request time. Anonymized IEEE-CIS columns (`C`, `M`, `V` and most `D` and `id_*`) are never model inputs. The exceptions map to score request fields: `D1` (days since the client began) gives the account age, `id_30`, `id_31` and `id_33` hold the client's operating system, browser and screen, and `card1` and `addr1` are the card issuer and billing region codes. Whether these inputs agree with the project guide is tracked in AF-92.

| Feature | Meaning |
|---|---|
| `amount`, `cents` | Purchase amount and its fractional part |
| `product_code`, `card_network`, `card_type` | Product code, card network and card type as small integer codes |
| `card_country`, `card_category`, `billing_country` | Card and billing codes from the request |
| `card_issuer_code`, `billing_region_code` | Card issuer (`card1`) and billing region (`addr1`) codes |
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

Player ID: `card1 + addr1 + (day - D1)`. Card ID: `card1` to `card6` combined. Missing values are `-1`.

Left out after testing: money-velocity features (amount spent per card or player in a time window) did not improve the model; `hour_of_day` was removed in v4 because IEEE-CIS times start at an unknown moment, so the hour is shifted for live traffic; codes for the email domain, device, OS and browser gave no gain. `player_txn_count`, `secs_since_first`, `card_distinct_players` and the `card1_distinct_*` counts are lifetime counts and are still inputs; replacing them with windowed counts is tracked in AF-95.

## Evaluation

The split follows time, by dataset day: train 31-120, validation 136-152 (early stopping and thresholds), test 153-183. Days 1-30 only build up history and are not used as training rows, because at the start of the data nobody has history yet. The gap between train and validation stands in for the delay before fraud labels arrive.

- `flag_at` is set so that about `--review-rate` of legitimate validation purchases are flagged (or by `--review-cost` or `--target-precision`).
- `clear_below` is set so that only `--max-missed-fraud` of validation fraud scores fall below it. It is chosen on all validation purchases, not only the ones the rules send to review (AF-90).

Scores drift over time, so the thresholds should be checked against recent data before each release.

## History of changes

Each table below compares versions **on the test month**, which is how these choices were made. The current results are in [Current results](#current-results).

### Blending with the player's earlier scores (v5 combined)

The live, past-only version of the 1st-place IEEE-CIS solution's post-processing, which averaged each client's predictions. Blending the final combined score instead lowered precision, so the blend happens on the main model's score inside the combination.

| Test month, combined model | Without blending | With blending |
|---|---|---|
| PR-AUC | 0.233 | **0.240** |
| First-fraud PR-AUC | 0.191 | **0.200** |
| Fraud caught / precision at 1% of legit flagged | 16.7% / 37.8% | **18.1% / 39.7%** |
| Fraud caught on a player's first purchase at 1% | 29.4% | **31.7%** |
| Precision at 0.5% / 0.1% of legit flagged | 43.7% / 63.0% | 43.6% / 61.4% |

Thresholds in this table are computed on the test month.

### Averaging 5 models (v5 main)

The main model is the average of 5 LightGBM models trained with different random seeds. They are exported as one file: every tree of every model with its leaf values divided by 5, so the score is the sigmoid of the average raw score and the Go reader needs no change (4,700 trees, about 5 µs per purchase). The report keeps the first model alone as `single_model_test`.

| Test month, main model (thresholds computed on the test month) | One model | Average of 5 |
|---|---|---|
| PR-AUC | 0.245 | **0.247** |
| First-fraud PR-AUC | 0.133 | **0.138** |
| Precision at 0.5% of legit flagged | 45.7% | **46.1%** |
| Fraud caught / precision at 1% | 17.1% / 38.3% | **18.4% / 40.0%** |

### Model v5: card issuer and billing region codes

v5 adds `card_issuer_code` (`card1`) and `billing_region_code` (`addr1`) as numbers. The 1st-place IEEE-CIS solution relied on these columns; until v4 the model only saw counts built from them.

| Test month | v4 | v5 |
|---|---|---|
| Main model ROC-AUC | 0.838 | **0.852** |
| Main model PR-AUC | 0.231 | **0.245** |
| Main model first-fraud PR-AUC | 0.126 | **0.133** |
| Precision at 0.1% of legit flagged (threshold on the test month) | 58% | **60%** |
| Precision / fraud caught at 1% (threshold on the test month) | 37.7% / 16.7% | **38.3% / 17.1%** |
| Combined policy (thresholds from validation): flags, precision, fraud caught | 0.96%, 44%, 12.0% | 0.93%, 43%, 11.6% |
| Combined policy: purchases below `clear_below`, fraud among them | 19.5%, 1.9% of fraud | **22.3%**, 1.9% of fraud |

### First-fraud model and the combination (v4)

| Test month | v4 alone | v4 + first-fraud model |
|---|---|---|
| Fraud caught on a player's first purchase, 1% of legit flagged (threshold on the test month) | 21.6% | **29.6%** |
| First-fraud-only PR-AUC | 0.126 | **0.184** |
| Fraud caught / precision, 1% of legit flagged (threshold on the test month) | 16.7% / 37.7% | 16.6% / 37.6% |
| Policy (thresholds from validation): flagged / precision / fraud caught | 0.94% / 44% / 11.8% | 0.96% / 44% / 12.0% |
| Policy: purchases below `clear_below` / fraud among them | 20.5% / 1.7% of fraud | 19.5% / 1.9% of fraud |

The first-fraud model alone ranks overall fraud worse (ROC-AUC 0.66), so it is only used inside the combination. Replay check (2026-11-10, first 10,000 purchases from day 123): the live combined score equals Python exactly for every purchase, with the same flag decisions.

### Model v4 against v3

| Test month | v3 (38 features) | v4 (48 features) |
|---|---|---|
| ROC-AUC | 0.828 | **0.838** |
| PR-AUC | 0.204 | **0.231** |
| First-fraud-only PR-AUC | 0.116 | **0.126** |
| Fraud caught / precision at 0.5% of legit flagged (threshold on the test month) | 9.0% / 39% | **11.4% / 45%** |
| Fraud caught / precision at 1% (threshold on the test month) | 14.6% / 35% | **16.7% / 38%** |
| Policy `flag_at` (from validation): flagged, precision, fraud caught | 0.97%, 38%, 10.6% | 0.94%, **44%**, **11.8%** |

With model v4, a replay of the first 30,000 purchases from day 123 matched Python within 1e-9 for every purchase (largest difference 1.1e-16), with latency p50 / p95 / p99 of 7.6 / 18.0 / 25.9 ms.

### Choosing `flag_at` by review cost (v4)

`--review-cost` on the test month with model v4 (85,430 purchases, 2,994 frauds worth 457,435 in total), thresholds from validation:

| `flag_at` chosen by | Purchases flagged | Reviews | Precision | Fraud caught | Fraud money caught |
|---|---|---|---|---|---|
| **Review rate 0.5% (default)** | 0.9% | 805 | 44% | 11.8% | **22.3%** |
| Review cost 20 | 4.1% | 3,471 | 27% | 30.9% | 36.6% |
| Review cost 10 | 18.2% | 15,557 | 13% | 67.5% | 72.0% |
| Review cost 5 | 25.2% | 21,508 | 10% | 75.4% | 77.7% |
| Review cost 1 | 63.2% | 54,002 | 5% | 95.3% | 96.1% |

The default stays the review rate: at any realistic review cost the money rule asks analysts to check thousands of purchases a month, far more than a review team can handle.

### Full replay with model v3 (2026-11-09, days 123 to 182)

| Check | Result |
|---|---|
| Purchases sent | 170,474 in 37.5 minutes, none failed |
| Live score equals Python score (within 1e-9) | 170,465 of 170,474 (99.995%) |
| Same flag decision as Python | 100% |
| Latency p50 / p95 / p99 | 11.2 / 24.6 / 39.7 ms |

The 9 differences came from `player_avg_amount`, summed from float amounts in Python and from whole cents in Go; v4 sums minor units on both sides and removed them.

Test month (days 153 to 182, 85,430 purchases, 3.5% fraud), model v3, ruleset version 1 without account age:

| Decision | Sent to review | Fraud among them | Fraud stopped |
|---|---|---|---|
| Rules only | 0.14% | 4.2% | 0.17% |
| Rules and model | 2.25% | 26.4% | 17.0% |

Since v4 the replay sends `account_created_at` from `D1`, so the `new_account` rule fires on IEEE-CIS too; the rule outcomes with that mapping are tracked in AF-91. The model flagged 2.1% of purchases instead of the 1% expected from training, because its lifetime history features saw only 30 days (AF-95).
