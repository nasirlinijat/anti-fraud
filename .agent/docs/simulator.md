# Simulator

## Demo traffic

The simulator sends generated purchases to `POST /score` in the same format as real clients. With Go and GNU Make installed and the backend running, run:

```bash
make simulate
```

It sends 500 purchases, 20 per second, and prints how many of each pattern were allowed, reviewed, or blocked. It reads `API_KEY` and `PORT` from `.env`. Pass options with `ARGS`, for example:

```bash
make simulate ARGS="-seed 7 -count 2000 -rate 50 -suspicious 0.2"
```

For a live demo, open the dashboard's **15 minutes** view and run:

```bash
make simulate ARGS="-count 0 -rate 5 -seed 1"
```

Live mode timestamps each purchase as it is sent. Press Ctrl+C to stop and see the totals by pattern. The seed controls the purchase sequence, while timestamps and the default run prefix vary by launch. Changing the send rate can change which time-based rules fire.

If you need the simulator's exit status after Ctrl+C, build and run it directly from `backend` with `API_KEY` and `PORT` set. The binary exits successfully on interrupt if there were no send failures. Make and `go run` can report an interrupt after printing the summary.

```bash
go build -o ./bin/simulator ./cmd/simulator
./bin/simulator -count 0 -rate 5 -seed 1
```

Without `make`, for example in Windows PowerShell, run it from `backend` with the same values as `.env`:

```powershell
$env:API_KEY="local-dev-key"; $env:PORT="8080"; go run ./cmd/simulator -count 1000 -rate 50
```

| Option | Default | Meaning |
|---|---|---|
| `-seed` | 1 | Random seed for the purchase sequence |
| `-start` | the last backfill purchase happens now | Backfill start in RFC 3339. The score API rejects event times more than 5 minutes in the future. Use only with a positive count |
| `-count` | 500 | Number of purchases to send. Use 0 for live mode until Ctrl+C |
| `-rate` | 20 | Purchases sent per second, 0 for as fast as possible |
| `-suspicious` | 0.1 | Share of events that start a special scenario, including legitimate edge cases and fraud. Use 0 for normal purchases only or 1 for special scenarios only |
| `-players` | 300 | Number of regular players |
| `-gap` | 20s | Mean gap on the simulated timeline used to order purchases. The send rate controls live pacing |
| `-missing` | 0.25 | Share of players whose client leaves out each group of optional fields: IP, device ID, device, account creation time, billing, card details, email, and payment verification results. A player misses the same groups on every purchase, whatever the scenario. Use 0 to send every field |
| `-run` | seed and start for backfill, unique for live | Prefix of all generated IDs |
| `-url` | `http://localhost:$PORT` | Base URL of the backend |
| `-dump` | none | Write the purchases with their scenario and fraud label to this JSON Lines file instead of sending them. Needs a positive count and no API key. Used to train the game model, see `ml/README.md` |

Backfill simulates event times, so a day of traffic can be sent in a few minutes. Every ID starts with the run prefix. Repeating a backfill with the same prefix, seed, start time, and settings returns saved decisions for matching purchases. Reusing an ID with different fields, including a different event time, returns 409. Use a new run prefix to send new purchases.

## Seed the demo

Follow [Populate the demo](../../README.md#populate-the-demo) and run `make seed`. It targets the local API at `127.0.0.1:$PORT`, uses the reset endpoint with `RESET_SESSION_TOKEN`, and uses `API_KEY` for scoring. The token must belong to an admin. `LABELS_DATABASE_URL` must point to the same database as the backend, using the evaluation role. Pause other traffic and review work while it runs.

The command generates purchases from January 5 through January 11, 2026 UTC with seed 42. It uses the active ruleset and loaded model. After scoring completes, it uses finalized simulator labels to approve legitimate purchases and reject fraud through the review API, leaving up to 10 of the newest reviews open. It adds no blocklist entries. If a run fails after reset, fix the reported error and rerun `make seed` to start from empty demo data.

## Scenarios

The scenario descriptions below are for a player whose client sends every field. A rule that needs a missing field does not fire.

Every purchase also carries the payment gateway's verification results: `cvv_result`, `avs_result`, and `three_ds_result`. Legitimate patterns almost always pass and fail each check in only 1 to 3% of purchases. A saved card keeps no security code, so the CVV check is skipped (`not_checked`) for about 35% of regular players' purchases and about 40% of account takeovers, which pay with the owner's saved card; a card typed in for the first time, on a new account, a relative's account, or an internet cafe's computer, is checked and mistyped in about 3% of purchases. The address check exists only for cards issued in the United States and the United Kingdom; every other card, honest or not, reports `not_checked`. Card testing fails the CVV check in about 45% of purchases and, on cards that support it, the address check in about half. Stolen cards usually pass the CVV check, because the details were stolen with the card, but fail 3-D Secure in about 40%. The results are drawn from the seed and the transaction ID, outside the main random sequence, so a seed generates the same purchases as before apart from these fields.

Some connections are moved into the mock IP intelligence ranges, a quarter of them as IPv6 addresses. About 3% of regular players' home connections are on a VPN, so the IP rules also catch innocent purchases. Travellers and expats use a VPN in 15 to 20% of cases, and about a fifth of internet cafes sit behind a public proxy. Bots run in data centres: about half of device farms and card testing come from a hosting range, and card testing also uses proxies and Tor. Stolen cards and account takeovers hide behind a VPN, proxy, or Tor in 40 to 45% of cases, and quieter fraud less often. Most fraud still uses residential connections that a reputation service cannot flag. The choice is drawn from the seed and the original address, outside the main random sequence, so a shared connection such as a family home or a device farm moves as a whole, and only `ip_address` changes.

Each special scenario is equally likely when an event starts one. Some scenarios generate several purchases, so the share of special purchases can differ from the share of special events.

| Pattern | What happens |
|---|---|
| `normal` | A regular player buys with their own card from their own country: mostly an ordinary amount, sometimes a larger bundle |
| `card_testing` | A new account tries 7 to 9 small purchases on one card from another country |
| `account_takeover` | An established account is used for a large purchase from another country and a new device |
| `new_account_spend` | An account created a few hours ago buys for a large amount |
| `traveler` | A player uses their usual card and device while abroad |
| `new_phone` | A player uses their usual card from home on a new phone |
| `internet_cafe` | Two or three players use their own cards on the same public computer and connection |
| `burst_buyer` | A player makes four to seven small purchases in quick succession |
| `new_casual_player` | A player with a new account makes a small purchase from home |
| `returning_player` | A player who was inactive before the run returns for an ordinary purchase |
| `stolen_card` | A different player makes one purchase with a regular player's card |
| `low_and_slow` | Fraud looks like one ordinary purchase from an older account and can pass the rules |
| `expat` | A returning player who lives abroad buys with a card from their home country |
| `new_big_spender` | A player with a new account makes a large legitimate purchase from home |
| `family_card` | A relative uses a regular player's card from the same home connection |
| `session_shopper` | A player with an older account makes two to four small purchases with their own card, minutes apart on the simulated timeline |
| `slow_card_testing` | Fraud tries three to five cards with one small purchase each from an older account in the cards' country, minutes apart on the simulated timeline, and can pass the rules |
| `quiet_takeover` | Fraud uses an established account from a new phone in the player's own country for a mid-sized amount, and can pass the rules |
| `device_farm` | Fraud uses one emulator and one connection for four to six accounts created between ten minutes and ten hours earlier, each with its own card, one to three purchases each, minutes apart, ending in a larger purchase |

Before a purchase, the simulator also sends the player's logins and password resets to `POST /account-events`. Every scenario has them, honest or not, because a game asks for a login before it sells anything:

| Accounts | Account events |
|---|---|
| Established, on first purchase in the run | Logins on 5 to 40% of the 30 days before, from the player's own device and country; the share is drawn per player. A player back after a long break (`returning_player`) has none |
| Created less than a day earlier | One login right after the account was created, from the device and country of the first purchase |
| Every session | A login 1 to 60 minutes before a purchase whose device and country have not logged in for 6 hours |
| Second device | 20% of players also play on a second device: 30% of their earlier logins come from it, and 15% of their sessions follow a login on it 1 to 20 hours before |
| Password resets | 2% of established accounts per 30 days, 2 to 60 minutes before a login; also 10% of `new_phone`, 5% of `internet_cafe`, and 25% of `returning_player` sessions |
| New device or country, honest | A new phone, an internet cafe's computer, and a trip abroad each log in from the new device or country before the purchase |
| `account_takeover`, `quiet_takeover` | A login from the attacker's device 1 to 15 minutes before, after a password reset in 45% and 30% of cases, with up to two and one further logins within the hour before |
| `card_testing`, `device_farm` | Scripts log in 10 seconds to 2 or 5 minutes before every purchase, with up to two and one further logins in the 5 minutes before |
| `new_account_spend`, `new_big_spender`, `new_casual_player` | The same events: the login after the account was created and a session login 1 to 30 minutes before the purchase |
| `stolen_card`, `slow_card_testing`, `low_and_slow` | The fraudster's own older account logs in like a regular player's |

These rates follow how players use a game account, not any one dataset. Account events leave out the device and country when the player's client leaves them out of purchases. They come from their own random stream and seeded draws, so a seed produces the same purchases as before. `-dump` writes them to a second file next to the purchases, `world1.account_events.jsonl` for `world1.jsonl`, for model training and the replay. A failed account event is reported and the purchase is still scored. Console simulation runs log each failed account event with the run ID, and log the number of failures when the run finishes.

The simulator keeps pattern names local and prints the score API's actual outcomes for every pattern, including rows with zero purchases. Decisions depend on the active ruleset, blocklist, model, and purchase history. For example, card testing can start with allowed purchases before the velocity rule contributes to a block. Compose runs with the model enabled, so a purchase that passes the rules can still be sent to review.

## Console simulation API

Set `SIMULATION_ENABLED=true` and sign in as an admin to control console-managed traffic. Restart the backend after changing the setting. It defaults to false, including production. These routes are available through the console host; the public production API hostname does not expose them.

`POST /simulations` uses an admin session and accepts, for example:

```json
{"request_id":"backfill-42-first","mode":"backfill","seed":42,"rate":5,"count":500,"scenarios":["normal","card_testing"]}
```

It returns 202 with the run ID and a Location header. For live traffic use `mode: "live"` and omit count. Poll `GET /simulations/{id}` with an admin session, or `GET /simulations?limit=20` for history; pass its `next_cursor` as `before` for the next page. `POST /simulations/{id}/stop` uses an admin session and drains the current purchase. Stopping an already finished run is safe.

Matching start retries with the same account and request_id return the original run with 200, including after completion. Changed settings or another active run return 409. Rates are 1–50 purchases/second; backfills allow up to 10,000 purchases and default to 500. Seeds are nonnegative JavaScript-safe integers. Omitted scenarios select all patterns. Normal plus special scenarios keeps the existing 10 percent special-event share; without normal, every event starts one of the selected special scenarios. Bursts can produce unequal purchase shares.

History and counters are saved in PostgreSQL. `sent` counts saved decisions and is the sum of allowed, reviewed and blocked. The worker records an in-flight transaction before scoring and checkpoints its result afterwards. On recovery it reconciles that transaction against saved decisions and marks unfinished runs `interrupted`; it does not automatically continue their traffic. `pending: true` means an outcome still cannot be confirmed. A new run needs a new request_id. Database failures stop dispatch until recovery. Only console-managed runs share the active-run guard; separately launched CLI simulators are not controlled.

A seed determines traffic given the same generator settings and time anchor. Actual outcomes also depend on existing history, blocklists, rules and model. Backfill should use a clean demonstration database when comparing results. This API does not expose fraud labels; the controller persists evaluation answers privately.

## Reset demonstration data

Pause external traffic, replay, label import/recovery, and mock-bank jobs and
let in-flight work finish. With `SIMULATION_ENABLED=true`, an admin session can call `POST /simulations/reset`. Success returns 204; an active
console simulation returns 409, and a disabled feature returns 404.

`make reset` calls the same endpoint on the running backend. Set
`RESET_SESSION_TOKEN` in the environment; optionally set
`RESET_URL` to override `http://127.0.0.1:$PORT` (port 8080 by default).
`RESET_URL` must be an absolute HTTP or HTTPS URL. A base path is supported;
user information, query strings, fragments, and invalid ports are rejected.
This CLI sends its credentials to the operator-selected backend, so configure
only a trusted destination. Redirects are never followed. Credentials are read
from the environment and never passed as command arguments.

Reset clears every purchase, decision, review claim/resolution, private finalized
and pending evaluation answer, feature total, blocklist entry, account event,
chargeback, and simulation run in this database. Start request IDs can be reused.
Ruleset versions and their active selection, console accounts, sessions, audit
records, and identity sequence positions remain. Model files, purchase dumps,
and accompanying account-event JSONL files remain on disk. Replay sends account
events and purchases; both flows must be paused.

Scoring falls back to PostgreSQL until its Redis blocklist mirror is rebuilt.
A cache rebuild failure does not undo a committed reset; database fallback stays
active until a successful reload. Apply migrations and role grants before
starting the backend with simulations enabled. Runtime and evaluation database
connection targets must match, including fallback hosts.
