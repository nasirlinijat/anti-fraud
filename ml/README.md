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
| `backend/models/lgbm-ieee-v2.json` | LightGBM model dump that the Go service loads |
| `backend/models/lgbm-ieee-v2.golden.csv` | 2,000 synthetic feature rows (random values within realistic ranges, not dataset rows) with the expected model score, for the Go tests |
| `backend/models/lgbm-ieee-v2.report.json` | Metrics, thresholds and export checks |
| `ml/output/training_report.json` | Same report, kept locally |

Options: `--version` (model name), `--review-rate` (share of legitimate purchases the model may send to review, default 0.005) and `--max-missed-fraud` (share of fraud a cleared review may contain, default 0.02).

The export fails if the model uses a split type the Go reader does not support, or if a plain tree reader does not reproduce LightGBM's scores on the test rows. Change `--version` whenever the feature list changes, so the service never loads a model with the wrong inputs.

## Compare feature groups

```bash
.venv/Scripts/python experiment.py data/train_transaction.csv
```

Trains one model per feature set (base, base plus each group, all groups, all minus each group) and prints test ROC-AUC, PR-AUC, first-fraud PR-AUC and precision at fixed false-positive rates. Groups are defined in `FEATURE_GROUPS` in `features.py`; `SELECTED_GROUPS` picks the ones the model uses. Results are written to `output/experiments.json`.

## Tests

```bash
.venv/Scripts/python -m pytest tests
```

The tests use synthetic purchases in `testdata/`, so they run without the dataset. They check exact feature values, that adding later purchases never changes earlier features, the window boundaries, the time split and the model export. After changing a feature, regenerate the expected values with `python make_fixture.py` and check the differences.

The exact feature definitions for the Go service are in `FEATURES.md`.

## Features

All history features use only transactions that happened before the current one, so the scoring service can compute the same values at request time. Anonymized IEEE-CIS columns (`C`, `D`, `M`, `V`, `id_*`) are never model inputs; `D1` is used only to build the player ID.

| Feature | Meaning |
|---|---|
| `amount`, `cents` | Purchase amount and its fractional part |
| `product_code`, `card_network`, `card_type` | Product code, card network and card type as small integer codes |
| `card_country`, `card_category`, `billing_country` | Card and billing codes from the request |
| `hour_of_day` | Hour of the purchase |
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

Player ID: `card1 + addr1 + (day - D1)`. Card ID: `card1` to `card6` combined. Missing values are `-1`. Money-velocity features (amount spent per card or player in a time window) were tested and left out because they did not improve the model.

## Evaluation

The split follows time, by dataset day: train 31-120, validation 136-152 (early stopping and thresholds), test 153-183. Days 1-30 only build up history and are not used as training rows, because at the start of the data nobody has history yet. The gap between train and validation stands in for the delay before fraud labels arrive.

## Policy thresholds

- `flag_at`: an allowed purchase with a model score at or above it goes to review. It is set so that about `--review-rate` of legitimate validation purchases are flagged.
- `clear_below`: a purchase the rules sent to review is cleared when its score is below it. It is set so that only `--max-missed-fraud` of validation fraud scores fall below it.

The model never blocks. Scores drift over time, so the thresholds should be checked against recent data before each release.

The report gives ROC-AUC, PR-AUC and the share of fraud caught, with precision, when 0.1%, 0.5%, 1% and 2% of legitimate purchases are flagged. It also reports "first fraud only": IEEE-CIS labels a player's later transactions as fraud after a chargeback, so those follow-up rows are removed to measure how well the model finds new fraud.
