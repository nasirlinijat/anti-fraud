# Model feature specification

The scoring service must compute these features exactly as `ml/features.py` does, or the model gives wrong scores. `ml/testdata/transactions.csv` holds synthetic purchases and `ml/testdata/expected_features.csv` the values each feature must have for them; a Go test loads both and compares every value.

This file describes model v4 (48 features), which the service computes and loads by default.

## General rules

1. **Order.** Purchases are ordered by event time, then by transaction ID. Two purchases in the same second are therefore ordered by ID.
2. **History means strictly earlier purchases** in that order. The current purchase never counts itself, and later purchases never count.
3. **Time windows.** "In the last hour" counts earlier purchases with `time > now - 3600` seconds; "in the last day" uses 86400. A purchase exactly 3600 seconds earlier is not counted.
4. **Missing values become `-1`**: a missing request field, or a history feature that has no history yet (for example an average with no earlier purchase).
5. **Model input order** is the order of the table below, the same as `FEATURES` in `ml/features.py` and `feature_names` in the model file. The service must refuse a model whose `feature_names` differ.
6. All features are float64.

## Identities

| Name | Live service | In training (IEEE-CIS) |
|---|---|---|
| Player ID | `player_id` from the request | `card1 + "_" + addr1 + "_" + (day - D1)`, where `day` is `floor(TransactionDT / 86400)`; missing parts are the text `none` |
| Card ID | `card_token` from the request | `card1` to `card6` joined with `\|`; missing parts are the text `none` |
| Email | `email_domain`; missing is the text `none`, treated as one more domain | `P_emaildomain` |
| Device name | `device_name`; missing is the text `none` | `DeviceInfo` |
| Card number part | `card_issuer`; missing is the text `none` | `card1` |
| Billing region | `billing_region`; missing is the text `none` | `addr1` |

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
| 1 | `amount` | `amount_minor / 100` |
| 2 | `cents` | `(amount_minor mod 100) / 100` |
| 3 | `product_code` | Code table above |
| 4 | `card_network` | Code table above (`card4`) |
| 5 | `card_type` | Code table above (`card6`) |
| 6 | `player_txn_count` | Number of earlier purchases by the player |
| 7 | `player_avg_amount` | Sum of `amount_minor` of the player's earlier purchases, divided by 100, divided by their number |
| 8 | `amount_to_avg` | `amount / player_avg_amount`, when the average is above 0 |
| 9 | `secs_since_last` | Seconds since the player's previous purchase |
| 10 | `secs_since_first` | Seconds since the player's first purchase |
| 11 | `card_uses_1h` | Earlier purchases with the same card ID in the last hour |
| 12 | `card_uses_24h` | Earlier purchases with the same card ID in the last day |
| 13 | `player_distinct_cards` | Distinct card IDs the player used before |
| 14 | `email_is_new_for_player` | 1 if the player has earlier purchases and none used this email, else 0 |
| 15 | `card_country` | `card3` as a number |
| 16 | `card_category` | `card5` as a number |
| 17 | `billing_country` | `addr2` as a number |
| 18 | `email_domain_share` | Earlier purchases (all players) with this email, divided by all earlier purchases |
| 19 | `has_recipient_email` | 1 if a recipient email is present, else 0 |
| 20 | `recipient_email_differs` | 1 if a recipient email is present and differs from the purchaser email, else 0 |
| 21 | `player_max_amount` | Largest amount among the player's earlier purchases |
| 22 | `player_uses_1h` | Earlier purchases by the player in the last hour |
| 23 | `player_uses_24h` | Earlier purchases by the player in the last day |
| 24 | `player_same_amount_count` | Earlier purchases by the player with exactly the same amount |
| 25 | `player_distinct_emails` | Distinct emails the player used before |
| 26 | `email_players_share` | Distinct earlier players who used this email, divided by all distinct earlier players |
| 27 | `has_device_info` | 1 if the request has a device type or device name, else 0 |
| 28 | `device_type` | desktop 0, mobile 1, else `-1` |
| 29 | `device_info_share` | Earlier purchases (all players) with this device name, divided by all earlier purchases; `-1` when the device name is missing |
| 30 | `device_players_share` | Distinct earlier players who used this device name, divided by all distinct earlier players; `-1` when the device name is missing |
| 31 | `device_is_new_for_player` | 1 if the device name is present, the player has earlier purchases, and none used this device name, else 0 |
| 32 | `player_distinct_devices` | Distinct known device names the player used before (missing names are not counted) |
| 33 | `card_distinct_players` | Distinct players who used this card ID before |
| 34 | `card1_distinct_players` | Distinct players who used this `card1` value before |
| 35 | `card1_distinct_addresses` | Distinct billing regions (`addr1`, missing counts as `none`) used with this `card1` value before |
| 36 | `card1_distinct_emails` | Distinct purchaser emails (missing counts as `none`) used with this `card1` value before |
| 37 | `billing_distance` | Distance field from the request (`dist1`) |
| 38 | `account_age_days` | Days the account existed before the purchase: whole days between `account_created_at` and `event_time` (`D1` in IEEE-CIS); `-1` when unknown |
| 39 | `has_client_info` | 1 if the request has a client OS, browser or screen, else 0 |
| 40 | `os_family` | First word of the client OS in lower case: windows 0, ios 1, mac 2, android 3, linux 4, else `-1` |
| 41 | `browser_family` | First word of the client browser in lower case: chrome 0, mobile 1, safari 2, ie 3, edge 4, firefox 5, samsung 6, android 7, opera 8, else `-1` |
| 42 | `os_share` | Earlier purchases (all players) with exactly this client OS, divided by all earlier purchases; `-1` when the OS is missing |
| 43 | `browser_share` | Same for the client browser |
| 44 | `screen_share` | Same for the screen resolution |
| 45 | `screen_area` | Width times height from a screen resolution written as `WIDTHxHEIGHT` (for example `1920x1080`); `-1` when missing or in another format |
| 46 | `os_is_new_for_player` | 1 if the OS is present, the player has earlier purchases, and none used exactly this OS, else 0 |
| 47 | `browser_is_new_for_player` | 1 if the browser is present, the player has earlier purchases, and none used exactly this browser, else 0 |
| 48 | `player_distinct_browsers` | Distinct known browsers the player used before (missing browsers are not counted) |

Features 18, 26, 29, 30, 42, 43 and 44 count over all players, so the service needs running totals per email domain, device name, client OS, browser and screen, and overall, not only per player.

## Mapping to the score request

| Score request field | IEEE-CIS column | Notes |
|---|---|---|
| `transaction_id` | `TransactionID` | Ties in time are ordered by it |
| `event_time` | `TransactionDT` | Seconds after a fixed start time |
| `amount_minor` | `TransactionAmt` | `round(TransactionAmt * 100)`; training uses the same rounding |
| `player_id` | `card1`, `addr1`, `D1` | Stand-in player ID, see Identities |
| `card_token` | `card1`-`card6` | Stand-in card ID, see Identities |
| `card_issuer` | `card1` | Used by the `card1_*` features |
| `card_network` | `card4` | |
| `card_type` | `card6` | |
| `card_country` | `card3` | Sent as text, used as a number |
| `card_category` | `card5` | Sent as text, used as a number |
| `product_code` | `ProductCD` | |
| `billing_region` | `addr1` | |
| `billing_country` | `addr2` | Sent as text, used as a number |
| `billing_distance` | `dist1` | |
| `email_domain` | `P_emaildomain` | |
| `recipient_email_domain` | `R_emaildomain` | |
| `device_type` | `DeviceType` | Identity file, joined on `TransactionID` |
| `device_name` | `DeviceInfo` | Identity file, joined on `TransactionID` |

`device_id`, `ip_address` and `ip_country` are not model inputs: IEEE-CIS has no such columns. `account_created_at` gives `account_age_days`, which IEEE-CIS approximates with `D1` (days since the client began). The client OS, browser and screen come from the identity file's `id_30`, `id_31` and `id_33` (values such as `Windows 10`, `chrome 63.0` and `1920x1080`); the score request gets matching optional fields in ML-3. `card_country`, `card_category` and `billing_country` are numeric codes in IEEE-CIS; a value that is not a number becomes `-1`.

`hour_of_day` was removed in model v4: IEEE-CIS times start at an unknown moment, so the hour was shifted for live traffic, and the model loses nothing without it.

## Model policy

The thresholds are in `backend/models/<version>.report.json` under `policy`: `flag_at` (an allowed purchase at or above it goes to review) and `clear_below` (a reviewed purchase below it is cleared). The model never blocks.

The service reads the thresholds at startup from the report next to `MODEL_PATH` and refuses to start if the report is missing, the thresholds are not `0 <= clear_below < flag_at <= 1`, or the model's `feature_names` differ from the service's features. `scoring.ApplyModel` applies them after the rules. A block, from the rules or the blocklist, never changes. The score is saved in `decisions.model_score` and returned as `model_score`; without a model it is null and only the rules decide.

## Go implementation

`backend/internal/features` computes the features for every scored purchase, and the decision saves them in `model_inputs`, blocked purchases included.

- Player, card and card issuer features read the earlier purchases from the `purchases` table, in `(event_time, transaction_id)` order, using the indexes from migration 000006. Transaction IDs are compared as text.
- Features 18, 26, 29, 30, 42, 43 and 44 read running totals from the `feature_counts` table: all purchases, and purchases per email domain, device name, client OS, browser and screen, each with its number of distinct players. Saving a purchase adds to them in the same transaction. The totals count every saved purchase, so purchases must be scored in event time order, as live traffic, the simulator and the replay do. Two purchases scored at the same moment do not see each other.

## Checking the Go implementation

1. Feature values: `TestHistoryMatchesExpectedFeatures` scores `ml/testdata/transactions.csv` row by row against PostgreSQL and compares with `ml/testdata/expected_features.csv` (tolerance 1e-9).
2. Model scores: `TestPredictMatchesGoldenRows` scores every row of `backend/models/<version>.golden.csv` and compares with its `expected` column (tolerance 1e-9).
3. Indexes: `TestFeatureQueriesUseIndexes` checks that every history query is served by an index.
