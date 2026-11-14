# Rulesets and Rules

## Ruleset API

Use `GET /rulesets/active` to read the current version, `GET /rulesets` to list published versions newest first with their note and creation time, and `GET /rulesets/{version}` to read a specific version. To change settings, send the full configuration (and an optional `note`) to `POST /rulesets`. Ruleset reads and publishing require an admin session in the `Authorization: Bearer` header. Publishing returns the new version, which is used for subsequent scores. Earlier decisions keep their original version. A rule omitted from the configuration is disabled in the new version.

On the Rulesets page, choose **Off** to skip model scoring, **Shadow** to record model risk without changing the decision, or **Assist** to let the model add or clear reviews. Enter both model thresholds between 0 and 1, with the clear threshold below the flag threshold, or leave both blank to use the loaded model's report. The page shows when no model is loaded. Publishing saves the settings in a new version.

Device card count starts off. To enable it, publish a new ruleset with `device_card_count` enabled. The Rulesets page suggests 5 distinct cards in 60 minutes, including the current card, and a weight of 20 points. Before rolling back to a version of the service that predates this rule, publish a new ruleset that omits `device_card_count`.

New device spike also starts off. The Rulesets page suggests at least 3 trusted purchases in 90 days, a minimum amount of 1,000 minor units, a minimum of 3 times the trusted average, and a weight of 50 points. Trusted history includes allowed purchases and approvals recorded by the time of scoring. Before rolling back to a service version that predates this rule, publish a new ruleset that omits `new_device_spike` and keep the migration that stores its settings for historical versions.

## Card velocity evidence

Card velocity counts existing purchases on the same card after the start of the one-hour window and through the scored purchase's event time. When the rule fires, the decision saves the observed count, limit, window, and up to 50 purchase references from the same database snapshot as the count. If the count exceeds 50, it saves the observation without individual references. The review case displays verified contributors only when the complete set can be retrieved. It returns up to 20 other purchases in the window as investigation context. Neither rule evaluation nor model inputs use that later context.

## Payment verification rules

A payment gateway checks the card during authorization and reports the results, which the caller passes in the score request as `cvv_result`, `avs_result`, and `three_ds_result`. Three rules read them, and like `country_mismatch` they take no limit:

| Rule | Fires when | Suggested weight | Why |
|---|---|---:|---|
| `cvv_mismatch` | `cvv_result` is `mismatch` | 35 | The buyer does not know the security code printed on the card, but a genuine cardholder can mistype it, so it needs one more signal to reach review |
| `avs_mismatch` | `avs_result` is `mismatch` | 15 | A weak signal: many issuers outside a few countries do not support the check, and a recent move gives a genuine mismatch. `partial` does not fire, because a differently written street or postcode is common |
| `three_ds_failed` | `three_ds_result` is `failed` | 50 | The card issuer could not authenticate the cardholder, which is strong evidence on its own, so it reaches the review threshold alone |

Before rolling back to a service version that predates these rules, publish a new ruleset that omits `cvv_mismatch`, `avs_mismatch`, and `three_ds_failed`: an older version returns an error for a rule name it does not know, so it cannot load the active ruleset.

A result the request leaves out never fires its rule. The results are saved with the purchase, take part in the retry comparison, and are shown as evidence on the review case. They are also model inputs, as one code per check (see `ml/FEATURES.md`); a game model trained before them ignores them.

The rules start off, so existing rulesets keep their behaviour. To enable them with the suggested weights, publish the seeded settings plus the three rules (the Rulesets page offers the same weights):

```json
{
  "review_threshold": 50,
  "block_threshold": 80,
  "note": "Enable payment verification rules",
  "rules": {
    "high_amount": { "enabled": true, "weight": 30, "limit": 50000 },
    "card_velocity": { "enabled": true, "weight": 40, "limit": 5 },
    "new_account": { "enabled": true, "weight": 20, "limit": 24 },
    "country_mismatch": { "enabled": true, "weight": 25 },
    "cvv_mismatch": { "enabled": true, "weight": 35 },
    "avs_mismatch": { "enabled": true, "weight": 15 },
    "three_ds_failed": { "enabled": true, "weight": 50 }
  }
}
```

With these weights, a failed 3-D Secure check or a CVV mismatch from a foreign IP address sends the purchase to review, and a CVV mismatch together with a failed 3-D Secure check blocks it.

## Account takeover rules

The account takeover rules start off. They read the logins and password resets that the game's account system sends to `POST /account-events` with the service key, and only events before the purchase count. `recent_password_reset` fires when the player reset their password within the limit before the purchase. `new_device_login` and `new_country_login` fire when any login within the limit was the player's first from a device or IP country that no earlier login and no earlier allowed or approved purchase used. Blocked, rejected, and undecided purchases do not make a device or country known. A player with no earlier device or country on record does not trigger them, and a missing field never fires a rule. Later logins from the same device or country do not hide the first one, and a purchase checks at most the 50 devices and 50 countries whose first login in the window is most recent, whether or not they turn out to be new. Event, player, and device IDs are at most 256 bytes without control characters, and the IP country is a two-letter code saved in upper case. The Rulesets page suggests 24 hours for each limit and weights of 30, 25, and 20 points, so none of them reaches the review threshold alone. Limits are whole hours from 1 to 720. Before rolling back to a service version that predates these rules, publish a new ruleset that omits them and keep the migration that adds the `account_events` table. The events are also model inputs of game models trained with them, from `lgbm-game-v8`: four values from the player's events in the 30 days before the purchase, computed without the rules' trusted purchases, because a purchase is trusted by its decision, which a model input must never use (see `ml/FEATURES.md`).

## IP intelligence rules

When a score request has `ip_address`, the backend asks an IP intelligence provider whether the address belongs to a hosting or data-centre network, a VPN, a public proxy, or a Tor exit. A real deployment would connect a commercial IP reputation service; this project ships only a mock that classifies addresses from a table of ranges. `IP_INTEL_PROVIDER` selects it:

| Value | Effect |
|---|---|
| `mock` | The mock classifies addresses. Docker Compose and `.env.example` use it by default; the production override `deploy/compose.prod.yml` defaults to `off` |
| `off`, `none`, or empty | No lookup is made, and the IP rules never fire |

The mock's default ranges lie inside the documentation ranges of RFC 5737 and RFC 3849, so it never flags a real address:

| Kind | IPv4 | IPv6 |
|---|---|---|
| Hosting or data centre | `192.0.2.0/25` | `2001:db8:d0::/48` |
| VPN | `192.0.2.128/26` | `2001:db8:a0::/48` |
| Public proxy | `192.0.2.192/27` | `2001:db8:b0::/48` |
| Tor exit | `192.0.2.224/27` | `2001:db8:c0::/48` |

To replace them, set `IP_INTEL_MOCK_RANGES` to the path of a JSON file such as `[{"cidr": "10.0.0.0/8", "kind": "hosting"}]`, with `kind` one of `hosting`, `vpn`, `proxy`, or `tor`. The server exits with an error if the file cannot be read or lists an invalid range. The path is read by the backend process: with `make run` it is a path on your computer, while under Docker Compose it must exist inside the backend container, which mounts no such file by default. Write IPv4 ranges in IPv4 form; an IPv4-mapped IPv6 range such as `::ffff:10.0.0.0/104` is rejected.

The lookup never fails or slows a score by more than 40 ms. A lookup that fails, panics, or takes longer is logged as a warning, and the purchase is scored without it. The lookup runs for every new purchase with an IP address while a provider is configured, even when both IP rules are off or the purchase is blocklisted, so every review case carries the evidence. The cost is latency: the mock answers in microseconds, but a remote provider can add up to the 40 ms bound to each new purchase. Set `IP_INTEL_PROVIDER=off` to avoid it. The result is saved with the decision in `decisions.ip_intel` together with the provider name, and the review case shows it as evidence: `available` is false when the lookup failed or timed out, and the field is null when no lookup was made. A retry returns the saved decision without a new lookup. The address itself is never a model input. The model sees only two flags from the result, hosting and anonymous (VPN, proxy, or Tor), which are missing when no lookup was made or it failed (see `ml/FEATURES.md`). The training code classifies addresses with the same default ranges file, `backend/internal/ipintel/default_ranges.json`, so a model trained on simulator traffic expects the default ranges.

Two rules read the result, and like `country_mismatch` they take no limit:

| Rule | Fires when | Suggested weight | Why |
|---|---|---:|---|
| `datacenter_ip` | The address belongs to a hosting or data-centre network | 30 | Players buy from home or mobile connections, while bots and scripted card testing run on rented servers. A cloud gaming service or a corporate network can also look like a data centre, so it needs one more signal to reach review |
| `anonymous_ip` | The address is a VPN, a public proxy, or a Tor exit | 20 | A weak signal: fraudsters hide their location this way, but many players use a VPN for privacy or while travelling |

Like the payment verification rules, both rules start off. To enable them with the suggested weights, publish the seeded settings plus the two rules (the Rulesets page offers the same weights):

```json
{
  "review_threshold": 50,
  "block_threshold": 80,
  "note": "Enable IP intelligence rules",
  "rules": {
    "high_amount": { "enabled": true, "weight": 30, "limit": 50000 },
    "card_velocity": { "enabled": true, "weight": 40, "limit": 5 },
    "new_account": { "enabled": true, "weight": 20, "limit": 24 },
    "country_mismatch": { "enabled": true, "weight": 25 },
    "datacenter_ip": { "enabled": true, "weight": 30 },
    "anonymous_ip": { "enabled": true, "weight": 20 }
  }
}
```

With these weights, a data-centre address alone (30 points) or an anonymous address alone (20 points) stays below review. An address flagged both as hosting and as a VPN, proxy, or Tor exit scores 50 and reaches review on its own. A data-centre address reaches review with one more signal such as a new account or a high amount, and so does an anonymous address with a high amount (50 points); with a foreign country (45 points) it needs one signal more.

Before rolling back to a service version that predates these rules, publish a new ruleset that omits `datacenter_ip` and `anonymous_ip`: an older version does not know the rule names and fails to load a ruleset that contains them. Keep the migration that adds `decisions.ip_intel`, because an older version ignores the column.
