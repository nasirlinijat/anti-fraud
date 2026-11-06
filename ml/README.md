# ML

Trains the fraud model on the IEEE-CIS dataset. Python is used only offline; the Go service loads the exported model.

## Setup

1. Download `train_transaction.csv` from the [IEEE-CIS competition](https://www.kaggle.com/competitions/ieee-fraud-detection/data) (accept the competition rules first) and put it in `ml/data/`. The `data/` folder is ignored by Git and must never be committed.
2. Create the environment from the `ml` folder:

   ```bash
   python -m venv .venv
   .venv/Scripts/pip install -r requirements.txt
   ```

## Train

```bash
.venv/Scripts/python train.py data/train_transaction.csv
```

It builds the features, trains a logistic regression baseline and LightGBM, and writes `output/af13_report.json`.

## Features

All history features use only transactions that happened before the current one, so the scoring service can compute the same values at request time. Anonymized IEEE-CIS columns (`C`, `D`, `M`, `V`, `id_*`) are never model inputs; `D1` is used only to build the player ID.

| Feature | Meaning |
|---|---|
| `amount`, `cents` | Purchase amount and its fractional part |
| `product_code`, `card_network`, `card_type` | Product code, card network and card type as small integer codes |
| `card_country`, `card_category`, `billing_country` | Card and billing codes from the request |
| `hour_of_day` | Hour of the purchase |
| `email_domain_share` | Share of earlier purchases that used this email domain |
| `email_players_seen` | Distinct players seen earlier with this email domain |
| `has_recipient_email`, `recipient_email_differs` | Whether a recipient email is set and differs from the purchaser's |
| `player_txn_count`, `player_avg_amount`, `player_max_amount`, `amount_to_avg` | Player's earlier purchases and amounts |
| `secs_since_last`, `secs_since_first` | Time since the player's previous and first purchase |
| `player_uses_1h`, `player_uses_24h`, `card_uses_1h`, `card_uses_24h` | Earlier purchases by the player or card in the last hour and day |
| `player_same_amount_count` | Earlier purchases by the player with the same amount |
| `player_distinct_cards`, `player_distinct_emails`, `email_is_new_for_player` | Cards and email domains the player used before |

Player ID: `card1 + addr1 + (day - D1)`. Card ID: `card1` to `card6` combined. Missing values are `-1`.

## Evaluation

The split follows time, by dataset day: train 1-120, validation 136-152 (early stopping), test 153-183. The gap between train and validation stands in for the delay before fraud labels arrive.

The report gives ROC-AUC, PR-AUC and the share of fraud caught, with precision, when 0.1%, 0.5%, 1% and 2% of legitimate purchases are flagged. It also reports "first fraud only": IEEE-CIS labels a player's later transactions as fraud after a chargeback, so those follow-up rows are removed to measure how well the model finds new fraud.
