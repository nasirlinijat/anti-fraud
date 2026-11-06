# Model feature specification

The scoring service must compute these features exactly as `ml/features.py` does, or the model gives wrong scores. `ml/testdata/transactions.csv` holds synthetic purchases and `ml/testdata/expected_features.csv` the values each feature must have for them; a Go test should load both and compare every value.

## General rules

1. **Order.** Purchases are ordered by event time, then by transaction ID. Two purchases in the same second are therefore ordered by ID.
2. **History means strictly earlier purchases** in that order. The current purchase never counts itself, and later purchases never count.
3. **Time windows.** "In the last hour" counts earlier purchases with `time > now - 3600` seconds; "in the last day" uses 86400. A purchase exactly 3600 seconds earlier is not counted.
4. **Missing values become `-1`**: a missing request field, or a history feature that has no history yet (for example an average with no earlier purchase).
5. **Model input order** is the order of the table below, the same as `FEATURES` in `ml/features.py` and `feature_names` in the model file. The service must refuse a model whose `feature_names` differ.
6. All features are float64.

## Identities

| Name | Built from | Notes |
|---|---|---|
| Player ID | `card1 + "_" + addr1 + "_" + (day - D1)` | `day` is `floor(event_time / 86400)`. Missing parts are the text `none`. |
| Card ID | `card1` to `card6` joined with `\|` | Missing parts are the text `none`. |
| Email | purchaser email domain | Missing is the text `none`, which is treated as one more domain. |

## Code tables

| Field | Values |
|---|---|
| `product_code` | W 0, H 1, C 2, S 3, R 4 |
| `card_network` | visa 0, mastercard 1, american express 2, discover 3 |
| `card_type` | debit 0, credit 1, debit or credit 2, charge card 3 |

Any other or missing value is `-1`.

## Features

| # | Feature | Definition |
|---|---|---|
| 1 | `amount` | Purchase amount |
| 2 | `cents` | `amount - floor(amount)`, rounded to 3 decimals |
| 3 | `product_code` | Code table above |
| 4 | `card_network` | Code table above (`card4`) |
| 5 | `card_type` | Code table above (`card6`) |
| 6 | `player_txn_count` | Number of earlier purchases by the player |
| 7 | `player_avg_amount` | Mean amount of the player's earlier purchases |
| 8 | `amount_to_avg` | `amount / player_avg_amount`, when the average is above 0 |
| 9 | `secs_since_last` | Seconds since the player's previous purchase |
| 10 | `secs_since_first` | Seconds since the player's first purchase |
| 11 | `card_uses_1h` | Earlier purchases with the same card ID in the last hour |
| 12 | `card_uses_24h` | Earlier purchases with the same card ID in the last day |
| 13 | `player_distinct_cards` | Distinct card IDs the player used before |
| 14 | `email_is_new_for_player` | 1 if the player has earlier purchases and none used this email, else 0 |
| 15 | `hour_of_day` | `floor(event_time / 3600) mod 24` |
| 16 | `card_country` | `card3` as a number |
| 17 | `card_category` | `card5` as a number |
| 18 | `billing_country` | `addr2` as a number |
| 19 | `email_domain_share` | Earlier purchases (all players) with this email, divided by all earlier purchases |
| 20 | `has_recipient_email` | 1 if a recipient email is present, else 0 |
| 21 | `recipient_email_differs` | 1 if a recipient email is present and differs from the purchaser email, else 0 |
| 22 | `player_max_amount` | Largest amount among the player's earlier purchases |
| 23 | `player_uses_1h` | Earlier purchases by the player in the last hour |
| 24 | `player_uses_24h` | Earlier purchases by the player in the last day |
| 25 | `player_same_amount_count` | Earlier purchases by the player with exactly the same amount |
| 26 | `player_distinct_emails` | Distinct emails the player used before |
| 27 | `email_players_share` | Distinct earlier players who used this email, divided by all distinct earlier players |

Features 19 and 27 count over all players, so the service needs running totals per email domain and overall, not only per player.

## Mapping to the score request

| Feature input | IEEE-CIS column | Score request field |
|---|---|---|
| Event time | `TransactionDT` | event time in seconds |
| Amount | `TransactionAmt` | amount (convert from minor units) |
| Product | `ProductCD` | product code |
| Card parts | `card1`-`card6` | card token parts (network, type, country, category) |
| Billing region and country | `addr1`, `addr2` | billing address codes |
| Emails | `P_emaildomain`, `R_emaildomain` | purchaser and recipient email domains |

`hour_of_day` depends on the clock's reference point. IEEE-CIS times are offsets from an unknown start, so for live traffic the hour will be shifted; treat this feature as approximate until the team decides how to align it.

## Model policy

The thresholds are in `backend/models/<version>.report.json` under `policy`: `flag_at` (an allowed purchase at or above it goes to review) and `clear_below` (a reviewed purchase below it is cleared). The model never blocks.

## Checking the Go implementation

1. Feature values: compute features for `ml/testdata/transactions.csv` in order and compare with `ml/testdata/expected_features.csv` (tolerance 1e-9).
2. Model scores: score every row of `backend/models/<version>.golden.csv` and compare with its `expected` column (tolerance 1e-9).
