Real-time fraud scoring for in-game purchases. Send each purchase to `POST /score` before you charge the player, and act on the outcome: `allow`, `review` or `block`.

## 1. Get an API key

Ask the Anti-Fraud operators for an API key for your shop. Send it in the `X-API-Key` header of every score request. Keep it on your server: never put it in a game client, a web page or a mobile app.

Click **Authorize** below and paste the key to try the requests on this page.

## 2. Score a purchase

Only five fields are required. Every extra field you send (card details, device, IP address, account age, 3-D Secure result) lets more rules and the fraud model work.

```bash
curl -X POST "$API/score" \
  -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "transaction_id": "order-10042",
    "event_time": "2026-11-08T10:00:00Z",
    "amount_minor": 1999,
    "player_id": "player-381",
    "card_token": "card-7f3a"
  }'
```

```javascript
const response = await fetch(`${API}/score`, {
  method: 'POST',
  headers: { 'X-API-Key': process.env.ANTI_FRAUD_API_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({
    transaction_id: 'order-10042',
    event_time: new Date().toISOString(),
    amount_minor: 1999,
    player_id: 'player-381',
    card_token: 'card-7f3a',
  }),
})
const decision = await response.json()
```

- `amount_minor` is an integer in minor units: 19.99 USD is `1999`.
- `event_time` is RFC 3339. It may be at most 5 minutes in the future.
- `card_token` is your payment provider's token for the card, never the card number.

## 3. Act on the outcome

```json
{
  "transaction_id": "order-10042",
  "outcome": "review",
  "score": 50,
  "fired_rules": [{ "name": "high_amount", "points": 50, "message": "Amount 1999 minor units is at or above the limit of 1000" }],
  "ruleset_version": 3,
  "model_score": 0.62,
  "model_version": "lgbm-game-v9"
}
```

| Outcome | What your shop does |
|---|---|
| `allow` | Charge the player and deliver the item. |
| `review` | Hold the order. Poll the status endpoint until an analyst approves or rejects it in the Anti-Fraud console. |
| `block` | Decline the purchase. Show the player a neutral message, never the reason. |

`score` and `fired_rules` explain the decision for your support team. `model_score` is the fraud model's risk from 0 to 1; the model can send a purchase to review or clear a review, but it never blocks on its own.

## 4. Retries are safe

Use one `transaction_id` per order and keep it when you retry. Sending the same purchase again returns the decision that was saved the first time, without scoring it twice. Reusing a `transaction_id` for a different purchase returns `409`.

Retry with the same request when you get:

| Status | Meaning | Retry after |
|---|---|---|
| `429` | Your key sent more requests than its rate limit | the `Retry-After` header, in seconds |
| `503` | The service could not finish; nothing was saved | the `Retry-After` header, in seconds |
| timeout or network error | The answer was lost | 1 second, then back off |

Do not retry `400`, `401`, `409` or `413`: fix the request first. Every error has the same JSON body: `error` is one sentence, and `fields` names each invalid field when there are any.

## 5. Check the service

`GET /health` answers while the service runs. `GET /ready` answers `ready` only when purchases can be scored. Neither needs an API key.

## Retrieve a held order's review result

Use the same server-side service key with an exact transaction ID:

```bash
curl --get "$API/decision-status" \
  -H "X-API-Key: $API_KEY" \
  --data-urlencode "transaction_id=order-10042"
```

```json
{"transaction_id":"order-10042","outcome":"review","review_status":"approved","resolved_at":"2026-11-14T15:00:00Z"}
```

The original `outcome` stays unchanged. `review_status` is `not_required` for
original allow/block decisions, `pending` for an open review, or `approved` /
`rejected` for a resolved review. `resolved_at` is null until resolution and for
`not_required`. This is a risk decision, not confirmation of payment or delivery.
Your backend remains responsible for charging and idempotent fulfillment.

Poll pending reviews every five seconds; stop on a final result. Back off after
network errors and honor `Retry-After` on 429/503. A 404 means there is no saved
scored purchase, not approval or rejection. If the original score request timed
out, recover it with the same idempotent POST /score request first. Retrying that
POST still returns its original scoring outcome, even after review resolution.

This deployment has one service key and no per-shop data isolation. Keep the key
on your server. Console sessions cannot call this endpoint, and the service key
cannot browse console investigation details. Status responses are not cached.
