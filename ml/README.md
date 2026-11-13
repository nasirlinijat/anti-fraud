# ML

Trains the fraud models. Python is used only offline; the Go service loads the exported model.

There are two models. **`lgbm-ieee-v5-combined`** is trained on the IEEE-CIS dataset and is the real-data benchmark: [Current results](#current-results) describes how it performs, and [History of changes](#history-of-changes) keeps the results of earlier versions. **`lgbm-game-v5`** is trained on the simulator's game traffic, see [Game model](#game-model-simulated-traffic). The service loads `lgbm-game-v5` by default, because the demo traffic comes from the simulator. `lgbm-game-v8`, which adds the payment, IP and account event inputs, is an opt-in model file until it passes its exam (see [Payment, IP and account event inputs](#payment-ip-and-account-event-inputs-v8)).

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
| `backend/models/lgbm-ieee-v5-combined.*` | Both models with their percentile tables, the policy, golden rows and the results described below; the file the service loads for the IEEE-CIS model |
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

## How the IEEE-CIS model scores a purchase

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

## Game model (simulated traffic)

IEEE-CIS is card data from 2017 with no game signals. On the simulator's game traffic the IEEE-CIS model flags 0.06% of purchases and catches 0.2% of the fraud, so it adds almost nothing to what the demo shows. `lgbm-game-v5` is trained on the simulator's own traffic with the first 54 features in [FEATURES.md](FEATURES.md) and the same LightGBM settings. **Every number in this section is for simulated traffic. It says how well the model learns the simulator's scenarios, not how it would do on real fraud;** the real-data result is the IEEE-CIS model above.

The traffic has normal purchases, 10 legitimate edge cases and 7 fraud scenarios, listed in the simulator section of the main `README.md`. Each player's client also leaves out each of seven groups of optional request fields with a probability of 25% (`-missing`), on every purchase of that player and whatever the scenario. The model therefore also learns from requests without an IP country, a device ID or an account creation time.

Generate labelled traffic (from `backend`). `-dump` writes the purchases with their scenario and fraud label to a file and calls no API:

```bash
go run ./cmd/simulator -dump ../ml/data/game/world1.jsonl -count 600000 -players 6000 -seed 11 -start 2026-01-01T00:00:00Z -run w1
go run ./cmd/simulator -dump ../ml/data/game/world2.jsonl -count 150000 -players 2500 -seed 23 -start 2026-01-01T00:00:00Z -run w2
```

Train (from `ml`):

```bash
.venv/bin/python train_game.py data/game/world1.jsonl --second-world data/game/world2.jsonl
```

It writes `backend/models/lgbm-game-v5.json`, its golden rows and its report. The first world covers 125 days: training uses days 0-84, validation days 90-104 (early stopping and thresholds) and test days 105-124. The second world has other players, cards and devices and another seed. It is only scored, its first 14 days separately. To run the service with another model, set `MODEL_PATH`, for example `MODEL_PATH=models/lgbm-ieee-v5-combined.json`.

Training saves every finished model in `output/checkpoints/<version>/`. If a run is interrupted, start the same command again: finished models are loaded and only the missing ones are trained. A checkpoint is used only for the same seed, settings and data. `train.py` and `check_game.py` do the same.

### Results

Thresholds are chosen on validation (`flag_at` 0.538138 at a 0.5% review rate, `clear_below` 0.168688).

| Simulated traffic | Purchases | Flagged | Precision | Fraud caught |
|---|---|---|---|---|
| Second world, first 14 days (empty database) | 67,613 | 7.5% | 97.9% | 78.2% |
| Second world, later days | 82,387 | 7.6% | 97.0% | 80.3% |
| First world, test days | 95,487 | 7.8% | 94.1% | 83.2% |
| First world, test days, `lgbm-ieee-v5-combined` with its own thresholds | 95,487 | 0.06% | 31% | 0.2% |

Share of each scenario flagged in the second world's later days:

| Fraud scenario | Flagged | Legitimate scenario | Flagged |
|---|---|---|---|
| Card testing | 99% | Normal | 0.0% |
| Account takeover | 98% | Expat | 0.0% |
| Stolen card | 82% | Internet cafe | 0.0% |
| Slow card testing | 75% | Returning player | 0.0% |
| Quiet takeover | 69% | Traveler | 0.2% |
| New account spend | 17% | Burst buyer | 0.3% |
| Low and slow | 0.2% | Family card | 3.0% |
| | | Session shopper | 4.8% |
| | | New casual player | 5.1% |
| | | New phone | 6.0% |
| | | New big spender | 13% |

Three scenarios are missed by design, and missing them is the honest limit:

- Low and slow is one ordinary purchase from an old account, built to look like a returning player.
- New account spend has the account age and the amounts of the legitimate new big spender, so the model flags little of either. The rules send both to review when the account is new and the amount is high.
- The first purchase of a slow card testing series is an ordinary small purchase. Only the later purchases, on other cards, can be told from a session shopper.

Replay through the service (2026-11-11, first 10,000 purchases of the second world on an empty database, see the next section): the live score equals the Python score for every purchase (largest difference 1.1e-16) with the same flag decisions, and latency p50 / p95 / p99 of 3.7 / 5.6 / 6.5 ms.

| First 10,000 purchases on an empty database | Fraud sent to review or blocked | Precision |
|---|---|---|
| Rules only | 21% | 88.8% |
| Rules and `lgbm-game-v5` | 77% | 95.2% |

The model adds slow card testing (0% to 74%), stolen card (0% to 68%), quiet takeover (0% to 43%), the card testing purchases the rules let through (34% to 96%) and the account takeovers they miss (50% to 90%). The rules alone stop less than on traffic with every field, because a rule that needs a missing field does not fire. The model cleared none of the 141 fraud purchases the rules sent to review, and 1 of the 24 legitimate ones.

### Missing request fields

Only `transaction_id`, `event_time`, `amount_minor`, `player_id` and `card_token` are required by the score API. The table shows the second world's later days when a caller never sends a group of optional fields. It is measured on the second world generated with `-missing 0`, with the fields removed from every request.

| Request has | Precision | Fraud caught |
|---|---|---|
| Every field | 98.7% | 80.7% |
| No IP country | 98.4% | 75.0% |
| No device ID | 98.8% | 77.1% |
| No device name and type | 98.7% | 81.2% |
| No account creation time | 98.6% | 80.6% |
| No billing region | 98.7% | 80.2% |
| No card details | 97.8% | 77.6% |
| No email | 98.7% | 80.7% |
| No IP, device ID or device | 98.2% | 71.4% |
| Required fields only | 98.2% | 68.7% |
| The simulator's default: each group missing for 25% of players | 97.0% | 80.3% |

This is why the training traffic has missing fields. A model with the same 54 features, trained on traffic where every field is always present, caught 84.7% of the fraud with every field, 30% without the IP country and 17% with the required fields only: it had never seen a request without them.

### Checks: what the model really learned

```bash
go run ./cmd/simulator -dump ../ml/data/game/world3.jsonl -count 600000 -players 6000 -seed 31 -suspicious 0.015 -start 2026-01-01T00:00:00Z -run w3
.venv/bin/python check_game.py data/game/world1.jsonl --second-world data/game/world2.jsonl --realistic data/game/world3.jsonl
```

The first command runs from `backend`, the second from `ml`. The checks use one model instead of the average of 5 and write `output/game_checks.json`.

**1. Leave one scenario out.** The model is trained and its thresholds chosen without one fraud scenario, then scored on test days that contain it:

| Fraud scenario | Caught when trained on it | Caught when left out |
|---|---|---|
| Card testing | 100% | 0% |
| Account takeover | 99% | 0% |
| Stolen card | 91% | 0% |
| Slow card testing | 76% | 0.4% |
| Quiet takeover | 64% | 0% |
| New account spend | 51% | 0% |
| Low and slow | 0.6% | 0.4% |

**The model recognises the scenarios it was trained on and does not find fraud of a kind it has never seen.** New fraud types stay the job of the rules and the analysts until labelled examples exist. New account spend sits at the threshold, so the single model of this check flags half of it and the exported average of 5 flags 17%.

**2. Empty database.** A model trained only on days 14-84 learns that a player without history is suspicious, because by then every regular player has history and only scenario players do not. On an empty database nobody has history:

| Second world, first 14 days | Precision | Fraud caught | Legitimate purchases flagged |
|---|---|---|---|
| Trained on days 14-84 | 92.8% | 81.8% | 0.66% |
| Trained on days 0-84 (`lgbm-game-v5`) | 98.0% | 78.2% | 0.17% |

**3. Realistic fraud share.** With 1.6% of purchases fraudulent instead of 9% (`-suspicious 0.015`), the model trained on the first world keeps its result on that traffic's test days: 97.2% precision, 80.5% of fraud caught, 0.04% of legitimate purchases flagged. A model trained on the low-fraud traffic itself catches 96.2% at 73.6% precision, because the same review rate then covers many more legitimate purchases per fraud.

**4. Training days against unseen days.** The exported model with its threshold, on the days it was trained on and on purchases it never saw:

| Purchases | Precision | Fraud caught |
|---|---|---|
| First world, training days 0-84 | 94.3% | 83.9% |
| First world, validation days 90-104 | 94.6% | 83.2% |
| First world, test days 105-124 | 94.1% | 83.2% |
| Second world, all days | 97.4% | 79.3% |

The model catches less than one point more on its training days than on later days, so it has not memorised purchases. The second world differs by a few points in both directions, and it is the number to quote.

### Simulator tells

| Tell | Status |
|---|---|
| Scenario players have an old account and no purchase history, while regular players soon have history | Reduced: training from day 0 stops the model from flagging missing history. On the first world's test days v5 flags 2.1% of returning players |
| About 9% of purchases are fraud | Accepted: the model keeps its precision at 1.6% fraud (check 3) |
| Fraud comes in a few fixed scenarios | Accepted and stated: the model does not generalise to unseen scenarios (check 1) |
| Every special scenario is equally likely, so a hard fraud scenario is as common as the legitimate behaviour it imitates | Accepted and stated: in real traffic the legitimate behaviour is far more common, and precision on these scenarios would be lower |
| Some amounts only occur in fraud: under 2.00 almost only in card testing, above 1,200.00 only in account takeover | Not removed |
| A field is missing for a player at random, not because of the platform or the integration | Accepted: the missing fields say nothing about the scenario, which is what the model must not learn from |
| The traffic is easy: the logistic regression baseline reaches ROC-AUC 0.989 on the test days (LightGBM 0.997) | Accepted: the results show that the pipeline learns labelled patterns, not real-world accuracy |
| Account events only for some scenarios: until v9 the simulator sent them only for regular players, takeovers, new phones and travelers, so having events meant "regular player", and `lgbm-game-v8` learned it (exam F) | Removed: every scenario, honest or fraud, now logs in, with rates from how players use a game account (see `.agent/docs/simulator.md`); every purchase has a login in the 30 days before |
| Takeovers hit about half of the regular players each month, so the attacker's password reset stays in the owner's history: 13% of normal purchases in the second simulator world have a reset in the 30 days before, against 2% from the players' own resets | Accepted: a consequence of the high fraud share; a reset within the last hours still separates takeovers, and v9 is checked on world F, where resets are rare |
| A takeover follows a password reset in 45% (quiet takeover 30%) of cases, against 2% of regular players a month | Accepted and stated: chosen from first principles, not measured; real takeover reset rates depend on how the account was stolen |
| The model can clear a correct rule review | Open: it cleared none in the replay, but `clear_below` is still chosen on all purchases, not on rule-reviewed ones (AF-90) |

### Traffic from other generators

`lgbm-game-v5` learns the simulator, so it was also tested on traffic nobody on the project designed. Five more worlds were written by separate generators that only saw the request fields: no rules, model, code or simulator. Each was briefed as a different expert (game store analyst, card fraud investigator, game trust and safety lead, publisher disputes analyst, payment risk lead for games in Asia and Latin America), with its own fraud schemes, legitimate edge cases, field values and amounts. The data and the generators are kept under `ml/data/game/blind/` and are never committed.

| World | Purchases | Days | Fraud | Role |
|---|---|---|---|---|
| A | 19,873 | 14, from an empty history | 6.6% | trained `lgbm-game-v7` and `v8` |
| B | 100,000 | 60 | 5.2% | trained `lgbm-game-v6`, `v7` and `v8` |
| C | 99,377 | 60 | 5.6% | trained `lgbm-game-v6`, `v7` and `v8` |
| D | 40,066 | 28 | 5.7% | exam for `v6`, then trained `v7` and `v8` |
| E | 38,713 | 35 | 5.5% | exam for `v7` and `v7-combined`, scored once, then trained `v8` |

On world A, `lgbm-game-v5` had ROC-AUC 0.59. A model trained on A itself reached 0.95, so the inputs carry the signal: v5 had learned the simulator's history lengths and amounts (`secs_since_first`, the most important input, has a median of 63 days in the simulator and 4 days in A).

`train_game.py` with several dumps trains on all of them, every world weighted equally, and adds history variants: the same world with the purchases before day 14, 20, 40, ... dropped, so that a short history stops meaning fraud. It chooses the inputs to mask and the regularisation by leaving one world out, and the thresholds from scores of models that did not see the world. `combine_game.py` combines a multi-world model with `lgbm-game-v5` by highest percentile.

```bash
.venv/bin/python train_game.py data/game/world1.jsonl data/game/worldB.jsonl data/game/worldC.jsonl data/game/worldA.jsonl data/game/worldD.jsonl --history-starts 0,14,20,40,60,80,100 --history-window 14 --version lgbm-game-v7
.venv/bin/python combine_game.py data/game/world1.jsonl data/game/worldB.jsonl data/game/worldC.jsonl data/game/worldA.jsonl data/game/worldD.jsonl --history-starts 0,14,20,40,60,80,100 --history-window 14 --multi lgbm-game-v7 --single lgbm-game-v5 --review-rate 0.01 --version lgbm-game-v7-combined
```

Exam world E, each model with its own thresholds (the combined model was replayed through the score API: live scores equal Python exactly, p95 6.5 ms):

| Model | ROC-AUC | PR-AUC | Fraud caught | Precision | Legitimate flagged |
|---|---|---|---|---|---|
| `lgbm-game-v5` | 0.66 | 0.13 | 14.7% | 13.0% | 5.7% |
| `lgbm-game-v6` (simulator, B, C) | 0.87 | 0.52 | 30.6% | 82.9% | 0.4% |
| `lgbm-game-v7` (simulator, A-D) | 0.88 | 0.52 | 40.2% | 57.1% | 1.8% |
| `lgbm-game-v7-combined` | 0.83 | 0.46 | 40.8% | 46.3% | 2.7% |

Simulator, second world, later days: v5 catches 80.3% at 97.0% precision, `v7-combined` 72.2% at 92.9%, `v7` 68.0% at 92.3%.

- The multi-world models rank unfamiliar traffic far better than v5. They catch rings sharing devices, top-up agents, resold subscription time and card testing (74-79% in E), and about a third of account takeovers and stocked accounts.
- Combining with v5 adds card testing but lowers the ranking on unfamiliar traffic (ROC-AUC 0.83 against 0.88 for v7 alone).
- Disputes by the real cardholder (a child using a parent's card, serial refunders) are not separated at all (ROC-AUC 0.35-0.53). They look like normal customers when they buy; only chargebacks and review outcomes can label them.
- Thresholds do not carry over: the combined model was set to flag 1% of legitimate purchases on the training worlds and flagged 0.5% in A, 0.6% in D and 2.7% in E. A deployment has to set `flag_at` on its own traffic, for example to a review budget after the first days.
- The rule written before scoring E required at least 95% precision on the simulator's second world, the demo traffic. `v7-combined` reached 92.9%, so the service keeps `lgbm-game-v5` by default. Set `MODEL_PATH=models/lgbm-game-v7-combined.json` (or `lgbm-game-v7.json`) to run a multi-world model.
- All worlds are synthetic, and several were written by generators of the same model family, which share some vocabulary (card tiers such as `STD`, `GLD`, `PLT`). Real traffic is likely to be harder.

### Payment, IP and account event inputs (v8)

`lgbm-game-v8` takes all 63 features: the 54 of v7, the payment verification codes and IP intelligence flags (55-59), and four inputs from the player's account events (60-63), defined in [FEATURES.md](FEATURES.md). The account inputs target the takeover scenarios: a takeover pays with the owner's saved card, so the payment checks look normal, but the account system has just seen a password reset or a login from a new device.

Generate the simulator worlds again from `backend` (the dump now also writes `world1.account_events.jsonl` and `world2.account_events.jsonl`), then train from `ml` on the first simulator world and the five generator worlds. The second simulator world is only scored, as the demo-traffic check:

```bash
go run ./cmd/simulator -dump ../ml/data/game/world1.jsonl -count 600000 -players 6000 -seed 11 -start 2026-01-01T00:00:00Z -run w1
go run ./cmd/simulator -dump ../ml/data/game/world2.jsonl -count 150000 -players 2500 -seed 23 -start 2026-01-01T00:00:00Z -run w2
.venv/bin/python train_game.py data/game/world1.jsonl data/game/worldB.jsonl data/game/worldC.jsonl data/game/worldA.jsonl data/game/worldD.jsonl data/game/worldE.jsonl --history-starts 0,14,20,40,60,80,100 --history-window 14 --version lgbm-game-v8 --development data/game/world2.jsonl
```

Leaving one world out chose `codes/v5` (the v5 settings with the five code inputs masked) at 150 trees, with `flag_at` 0.514162 and `clear_below` 0.00982 from the out-of-world scores.

**Worlds without account events.** Only the simulator worlds have account events; worlds A-E have none, so every one of their purchases has the four account inputs at `-1`. Left alone, `-1` would mostly mean "a generator world", and a tree could split on it to learn those worlds' lower fraud rate instead of fraud: "no events = honest". Two choices prevent that:

- The inputs have no "account events available" value. A player without any account event in the 30 days before the purchase gets `-1` for all four, exactly like a world without account events, so the inputs cannot name the world. In the first simulator world 22% of purchases have no account event in the window.
- A world with account events is also trained on a second time with the four inputs set to `-1` (`worlds.without_account_events`), as a service without account events would see it. The copy shares its world's weight and is never scored. The same simulator purchases are then on both sides of a split on the account inputs, so the inputs can only add what the events say, and the model still learns the simulator's takeovers for traffic without account events, such as worlds A-E.

Leaving one world out cannot measure the account inputs: when the first simulator world is left out, no training world has account events. The candidate and the thresholds are therefore chosen without them, and the second simulator world is the only check of what they add.

Second simulator world, later days (days 14-30, 80,207 purchases), each model with its own thresholds:

| Model | ROC-AUC | PR-AUC | Fraud caught | Precision | Legitimate flagged |
|---|---|---|---|---|---|
| `lgbm-game-v5` | 0.983 | 0.905 | 55.5% | 97.2% | 0.24% |
| `lgbm-game-v7` | 0.989 | 0.946 | 73.7% | 96.0% | 0.45% |
| `lgbm-game-v8` | 0.996 | 0.977 | 87.2% | 95.8% | 0.56% |

v5 catches far less than in its own results above because the current simulator has changed since: it now sends payment verification results, IP addresses and the device farm scenario, which v5 flags 15% of. On the first 14 days (an empty database) v8 catches 86.8% at 95.9% precision.

Ablation: the same settings, tree count and data, each variant with its own out-of-world thresholds, the average of 3 models, on the same days:

| Training | PR-AUC | Fraud caught | Precision | Legitimate flagged |
|---|---|---|---|---|
| v8 | 0.977 | 87.2% | 95.8% | 0.56% |
| Without the 4 account event inputs | 0.975 | 85.9% | 95.9% | 0.54% |
| Without the 9 new inputs (55-63) | 0.968 | 85.6% | 92.9% | 0.95% |

Share of each scenario flagged on the same days (v8 is the exported model):

| Scenario | v5 | v7 | v8 | v8 without account inputs | v8 without the 9 new inputs |
|---|---|---|---|---|---|
| Account takeover | 98% | 71% | 92% | 86% | 87% |
| Quiet takeover | 67% | 30% | 58% | 38% | 47% |
| Card testing | 99% | 92% | 98% | 98% | 99% |
| Device farm | 15% | 88% | 97% | 97% | 97% |
| Slow card testing | 75% | 55% | 76% | 75% | 66% |
| Stolen card | 86% | 42% | 68% | 67% | 72% |
| New account spend | 5% | 26% | 75% | 74% | 79% |
| Low and slow | 0% | 0% | 4.0% | 2.7% | 0% |
| New big spender (legitimate) | 5.2% | 23% | 42% | 41% | 73% |
| New casual player (legitimate) | 4.9% | 25% | 22% | 20% | 42% |
| New phone (legitimate) | 7.9% | 13% | 3.5% | 6.8% | 8.7% |
| Internet cafe (legitimate) | 0% | 0.5% | 5.5% | 4.4% | 2.3% |
| Session shopper (legitimate) | 6.4% | 1.8% | 2.1% | 2.3% | 8.8% |
| Normal, expat, traveler (legitimate) | 0% | 0% | 0% | 0% | 0% |

- The account inputs add the takeovers: quiet takeover from 38% to 58% and account takeover from 86% to 92%, while the new phone, the legitimate look-alike of a quiet takeover, drops from 6.8% to 3.5%. In the simulator a takeover starts with a password reset 70% of the time, and a new phone never does, so the reset is most of what they learn. That is a simulator choice, not a measured fact about real takeovers.
- The payment and IP inputs mostly cut legitimate flags: without them, precision falls from 95.8% to 92.9% and new big spenders are flagged 73% instead of 42%.
- v5 still catches more account takeovers (98%), because it learned the simulator's takeover amounts, which v7 and v8 mask or share with other worlds.

Replay through the service (second simulator world, all 150,000 purchases and 40,130 account events on an empty database, `SCORE_RATE_LIMIT=0`): the live score equals the Python score for every purchase (largest difference 2.2e-16) with the same flag decisions, and the exported model inputs equal the Python features on all 63 columns (largest difference 7.1e-15). Latency p50 / p95 / p99 is 4.3 / 6.3 / 7.1 ms over the first 10,000 purchases and 4.8 / 26.1 / 29.8 ms over all of them. The growth comes from the card issuer history query for purchases without a `card_issuer` (a quarter of this world), which counts every earlier purchase without one: their p95 is 29.2 ms, against 6.0 ms for purchases with an issuer. The account event query is index seeks only (0.15 ms on the player with the most events). In the replay the rules alone stop 30.8% of the fraud at 93.1% precision, and the rules with v8 87.6% at 94.8%.

Exam world F is written by a separate generator and scored once, after v8 was frozen: _result pending, to be added after the exam_.

v8 does not change the default model. The rule written before exam F requires, on F, a higher PR-AUC than v5 and v7 and more fraud caught than v5 at no lower precision, and on the second simulator world at least 95% precision and at least as much fraud caught as v5. Set `MODEL_PATH=models/lgbm-game-v8.json` to run it.

### Earlier game models

`lgbm-game-v1` was trained on days 14-84 only and was replaced (check 2). `lgbm-game-v2` is kept in `backend/models/`. It has the first 50 features and was trained on earlier traffic with 5 fraud scenarios, 6 legitimate edge cases and every field present. On that traffic its precision was 97.9% and it caught 91.9% of the fraud in the second world's later days. On the current traffic it reaches 81.8% precision and catches 65.5%, which is why it was replaced.

## Replay through the score API

`replay.py` sends IEEE-CIS purchases to a running backend through `POST /score`, in time order and in the API format, and compares the live decisions with Python. Fraud labels never reach the score API; they stay in the script and report, and can be imported into private evaluation tables after scoring.

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

A simulator dump is replayed the same way, from its first purchase, with the model trained on it; the report then also shows, per scenario, how many purchases the rules alone and the rules with the model stopped. The account events in the file next to the dump are sent to `POST /account-events` before the first purchase that comes after them. Set `SCORE_RATE_LIMIT=0` on the replay backend, or the replay is slowed down by 429 responses:

```bash
.venv/bin/python replay.py --requests data/game/world2.jsonl --limit 10000 --model ../backend/models/lgbm-game-v5.json --url http://localhost:8082 --api-key replay-key --out output/replay-game
```

For IEEE-CIS, by default it replays days 123 to 183: 30 days that build the history, then the test month (days 153 to 183). Add `--limit 10000` for a quick check. A full run sends about 170,000 purchases and takes about 40 minutes on a laptop. To run again, empty the tables first with `TRUNCATE review_resolutions, decisions, purchases, feature_counts, account_events` in that database.

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

2. Prepare a labels file kept outside the service: `transaction_id,is_fraud`. For the IEEE-CIS replay, `replay.py` writes `labels.csv` next to its report. For live traffic it comes from chargebacks: `export-inputs -chargeback-labels` writes it, see [Retrain from chargebacks](#retrain-from-chargebacks-mocked). Labels are never model inputs, and the scoring code never reads this file.

3. Train (from `ml`):

   ```bash
   .venv/bin/python train.py --inputs model_inputs.csv --labels labels.csv --train-days 123-150 --validation-days 151-165 --test-days 166-183
   ```

   `day` counts whole days since `--day-zero` (the replay's start by default). Purchases without a label are left out.

Check (2026-11-10, model v4): 30,000 purchases replayed through the service and exported matched the Python features exactly on all 48 columns, and training from the export produced a model file identical to training from the Python features.

## Retrain from chargebacks (mocked)

In production the fraud labels are chargebacks: the processor reports them
through the signed `POST /chargebacks` webhook weeks after the purchase. **In
this project the chargebacks are mocked:** `backend/cmd/mock-bank` disputes a
share of the simulator's charged purchases with illustrative rates and delays,
described in the main README under Chargebacks. The private evaluation labels
(`purchase_labels`) stay the exact answers for measuring decision quality;
chargebacks are the noisier labels a real business would train on, with missed
fraud and friendly fraud.

1. Generate traffic far enough in the past for chargebacks to arrive, then let
   the mock bank send the ones received by now (from `backend`):

   ```bash
   go run ./cmd/simulator -count 3000 -gap 1h -suspicious 0.3 -rate 0 -start 2026-06-01T00:00:00Z -run cb-demo
   go run ./cmd/mock-bank -run cb-demo
   ```

2. Export the model inputs and the chargeback labels together:

   ```bash
   go run ./cmd/export-inputs -database "$DATABASE_URL" -out ../ml/data/cb/model_inputs.csv -chargeback-labels ../ml/data/cb/labels.csv
   ```

   Only purchases that were charged to the card get a label: allowed ones and
   reviewed ones an analyst approved. Blocked, rejected and undecided
   purchases can never be charged back, so they are left out; the model
   therefore learns only from traffic the current system let through.

   Only purchases older than the maturity window are labelled. Such a
   purchase is `1` when it has a `fraud_card_not_present` or
   `fraud_card_absent_other` chargeback received by the as-of time, and `0`
   otherwise, also when it has a non-fraud chargeback. Every purchase inside
   the window is left out, with or without a chargeback: chargebacks arrive
   late, so a recent purchase without one is not yet known to be good, and
   keeping only the recent purchases that already have a fraud chargeback
   would make the recent days look almost all fraud. The window is 60 days by
   default (`-maturity-days`), because most disputes arrive within two months
   and networks allow about 120 days. `-maturity-days 0` labels every
   purchase up to the as-of time, which is only useful for tests.

   `-as-of` (default now, never in the future) ignores chargebacks received
   later. The labels still depend on the chargebacks and review decisions
   recorded so far, so the same command run later can give different labels.

   The export labels every charged purchase in the database. `-since` and
   `-until` (RFC 3339, converted to UTC) keep only purchases with an event
   time at or after `-since` and before `-until`. The database must contain
   only traffic that the bank has settled for the chosen range: a purchase
   whose disputes the mock bank, or a real processor, never reports is
   labelled `0` once it is mature.

3. Train from `ml`, splitting by day since the run's start. Write the model to
   `output/` instead of `backend/models/` until it has been checked:

   ```bash
   .venv/bin/python train.py --inputs data/cb/model_inputs.csv --labels data/cb/labels.csv --day-zero 2026-06-01T00:00:00Z --train-days 0-55 --validation-days 60-72 --test-days 76-88 --version lgbm-cb-v1 --models-dir output/cb-models
   ```

   Leave a gap of a few days between the splits, as the IEEE-CIS split does,
   for the delay before labels arrive.

4. Run the new model in shadow mode: copy the model file and its report to
   `backend/models/`, start the backend with `MODEL_PATH` pointing to it, and
   publish a ruleset with `model.mode` set to `shadow`. Every decision then
   saves the new model's score while the outcome stays with the rules.

5. After enough new chargebacks have arrived, export again with a later
   `-as-of`, compare the shadow scores with the labels, and publish a ruleset
   with `model.mode` set to `assist` only if the model clears reviews and
   flags fraud as expected. The model still never blocks a purchase.

Exported files are derived from the service's data and stay in the ignored
`data/` and `output/` folders.

## Compare feature groups

```bash
.venv/bin/python experiment.py data/train_transaction.csv
```

Trains one model per feature set (base, base plus each group, all groups, all minus each group) and prints ROC-AUC, PR-AUC, first-fraud PR-AUC and precision at fixed false-positive rates. Groups are defined in `FEATURE_GROUPS` in `features.py`; `SELECTED_GROUPS` picks the ones the model uses. Results are written to `output/experiments.json`. It currently compares on the test month, with thresholds computed on the test month (AF-94).

## Tests

```bash
.venv/bin/python -m pytest tests
```

The tests use synthetic purchases in `testdata/`, so they run without the dataset. They check exact feature values, that adding later purchases never changes earlier features, the window boundaries, the time split, the baseline's handling of missing values, loading a simulator dump, the training checkpoints and the model export. After changing a feature, regenerate the expected values with `python make_fixture.py` and check the differences.

## Features

The exact feature definitions for the Go service are in [FEATURES.md](FEATURES.md).

All history features use only transactions and account events that happened before the current one, so the scoring service can compute the same values at request time. Anonymized IEEE-CIS columns (`C`, `M`, `V` and most `D` and `id_*`) are never model inputs. The exceptions map to score request fields: `D1` (days since the client began) gives the account age, `id_30`, `id_31` and `id_33` hold the client's operating system, browser and screen, and `card1` and `addr1` are the card issuer and billing region codes. Whether these inputs agree with the project guide is tracked in AF-92.

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
| `cvv_result_code`, `avs_result_code`, `three_ds_result_code`, `ip_is_hosting`, `ip_is_anonymous` | Payment verification results and the IP intelligence answer (game traffic only) |
| `hours_since_password_reset`, `device_first_login_hours`, `country_first_login_hours`, `login_devices_24h` | The player's account events before the purchase (game traffic only) |

Player ID: `card1 + addr1 + (day - D1)`. Card ID: `card1` to `card6` combined. Missing values are `-1`.

Left out after testing: money-velocity features (amount spent per card or player in a time window) did not improve the model; `hour_of_day` was removed in v4 because IEEE-CIS times start at an unknown moment, so the hour is shifted for live traffic; codes for the email domain, device, OS and browser gave no gain. `player_txn_count`, `secs_since_first`, `card_distinct_players` and the `card1_distinct_*` counts are lifetime counts and are still inputs; replacing them with windowed counts is tracked in AF-95.

## Evaluation

The split follows time, by dataset day: train 31-120, validation 136-152 (early stopping and thresholds), test 153-183. Days 1-30 only build up history and are not used as training rows, because at the start of the data nobody has history yet. The gap between train and validation stands in for the delay before fraud labels arrive.

- `flag_at` is set so that about `--review-rate` of legitimate validation purchases are flagged (or by `--review-cost` or `--target-precision`).
- `clear_below` is set so that only `--max-missed-fraud` of validation fraud scores fall below it. It is chosen on all validation purchases, not only the ones the rules send to review (AF-90).

Scores drift over time, so the thresholds should be checked against recent data before each release.

## History of changes

Each table below compares versions **on the test month**, which is how these choices were made, except the first, which was chosen on the validation month. The current results are in [Current results](#current-results).

### Purchase-history inputs tried for v6 (not kept)

Each candidate uses only the request and earlier purchases, so the service could compute it. They were compared without the test month: training days 31-105, early stopping on days 106-120, scored on the validation month (days 136-152), each number the average of 3 LightGBM models with different seeds. The rule, written before any test month score: an input is kept only if it raises validation PR-AUC by at least 0.002 and does not lower the fraud caught when 0.5% of legitimate purchases are flagged. **None qualified**, so the feature list is unchanged.

| Added to the 54 features | Definition | Validation PR-AUC | Fraud caught at 0.5% |
|---|---|---|---|
| None | | 0.221 | 11.2% |
| `amount_to_max` | Amount divided by the player's largest earlier amount | 0.219 | 10.5% |
| `card_txn_count` | Earlier purchases with the card | 0.219 | 10.3% |
| `card_secs_since_first` | Seconds since the card's first purchase | 0.203 | 9.7% |
| `card_secs_since_first`, capped at 30 days | | 0.223 | 10.8% |
| `card_secs_since_last` | Seconds since the card's previous purchase | 0.220 | 10.8% |
| `amount_to_card_avg` | Amount divided by the card's average earlier amount | 0.220 | 10.4% |
| `card_distinct_emails` | Email domains used with the card before | 0.220 | 10.6% |
| `email_is_new_for_card` | The card has earlier purchases, none with this email domain | 0.222 | 11.0% |
| `product_is_new_for_player` | The player has earlier purchases, none with this product code | 0.221 | 10.5% |
| `device_distinct_cards` | Cards seen with this device name before | 0.201 | 9.1% |
| `card_uses_7d`, `player_uses_7d` | Earlier purchases by the card or player in the last 7 days | 0.223, 0.221 | 11.0%, 11.3% |
| `card_players_30d`, `player_cards_30d`, `player_emails_30d` | Distinct players on the card, cards and email domains of the player, in the last 30 days | 0.221, 0.223, 0.220 | 11.0%, 10.9%, 10.6% |

- The PR-AUC of one model changes by up to 0.003 with its seed, so every difference except the two drops is within noise.
- The two lifetime inputs, `card_secs_since_first` and `device_distinct_cards`, lower PR-AUC by about 0.02: they grow with the history the data has collected, so their values on later days are outside the training range (the problem of AF-95). Their capped and windowed versions do no harm but add nothing.
- The distinct email domains of a player are already an input, `player_distinct_emails`.
- IEEE-CIS has no device ID, so cards per device can only be counted per device name (`DeviceInfo`, such as `Windows`), which thousands of unrelated purchases share.

The 54 features retrained without a new input (as `lgbm-ieee-v6-combined`) reached test PR-AUC 0.237 against 0.240 for v5, and caught 10.9% of fraud at 0.5% of legitimate purchases flagged against 10.5%. The rule required both to be higher, so v5 stays the IEEE-CIS model and v6 was not added.

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

### Import replay labels for evaluation

After a completed replay, set the separate `LABELS_DATABASE_URL` for the replay
database, then run from `backend`:

```bash
go run ./cmd/import-labels -run ieee-replay-1 -source ieee-cis -file ../ml/output/replay/labels.csv
```

IEEE-CIS imports keep scenario empty. Simulator replay CSVs include scenario and
use `-source simulator`. Importing never sends score requests or changes model
inputs. Existing training reads only transaction_id and is_fraud, so it accepts
the added scenario column. Labels and derived CSV files must remain uncommitted.
