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
| `backend/models/lgbm-ieee-v4.json` | LightGBM model dump for the Go service |
| `backend/models/lgbm-ieee-v4.golden.csv` | 2,000 synthetic feature rows (random values within realistic ranges, not dataset rows) with the expected model score, for the Go tests |
| `backend/models/lgbm-ieee-v4.report.json` | Metrics, thresholds and export checks |
| `ml/output/training_report.json` | Same report, kept locally |

Options: `--version` (model name), `--review-rate` (share of legitimate purchases the model may send to review, default 0.005) and `--max-missed-fraud` (share of fraud a cleared review may contain, default 0.02).

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
$env:PORT="8082"; $env:API_KEY="replay-key"; $env:MODEL_PATH="models/lgbm-ieee-v4.json"; $env:DATABASE_URL="postgres://antifraud:antifraud@localhost:5432/antifraud_replay?sslmode=disable"; go run ./cmd/api
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

### Model v4 against v3 (test month, days 153-183)

| | v3 (38 features) | v4 (48 features) |
|---|---|---|
| ROC-AUC | 0.828 | **0.838** |
| PR-AUC | 0.204 | **0.231** |
| First-fraud-only PR-AUC | 0.116 | **0.126** |
| Fraud caught / precision at 0.5% of legit flagged | 9.0% / 39% | **11.4% / 45%** |
| Fraud caught / precision at 1% | 14.6% / 35% | **16.7% / 38%** |
| Policy `flag_at` on the test month: flagged, precision, fraud caught | 0.97%, 38%, 10.6% | 0.94%, **44%**, **11.8%** |

The Go service computes these features and loads v4 by default.

## Evaluation

The split follows time, by dataset day: train 31-120, validation 136-152 (early stopping and thresholds), test 153-183. Days 1-30 only build up history and are not used as training rows, because at the start of the data nobody has history yet. The gap between train and validation stands in for the delay before fraud labels arrive.

## Policy thresholds

- `flag_at`: an allowed purchase with a model score at or above it goes to review. It is set so that about `--review-rate` of legitimate validation purchases are flagged.
- `clear_below`: a purchase the rules sent to review is cleared when its score is below it. It is set so that only `--max-missed-fraud` of validation fraud scores fall below it.

The model never blocks. Scores drift over time, so the thresholds should be checked against recent data before each release.

The report gives ROC-AUC, PR-AUC and the share of fraud caught, with precision, when 0.1%, 0.5%, 1% and 2% of legitimate purchases are flagged. It also reports "first fraud only": IEEE-CIS labels a player's later transactions as fraud after a chargeback, so those follow-up rows are removed to measure how well the model finds new fraud.
