"""Replays IEEE-CIS transactions through the live score API and compares the
live decisions with the Python model.

Start the backend on an empty database first, because the live features use
every purchase already saved:

    docker compose down -v
    docker compose up -d --build --wait
    docker compose run --rm migrate

Usage (from the ml folder):
    python replay.py data/train_transaction.csv --api-key local-dev-key

Fraud labels never reach the service: they stay in this script and in the
report it writes.
"""
import argparse
import http.client
import json
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlparse

import numpy as np
import pandas as pd
from sklearn.metrics import average_precision_score, roc_auc_score

import export
import features
import train

# IEEE-CIS times are seconds from an unknown start. Starting at midnight UTC
# keeps hour_of_day the same as in training.
START = datetime(2017, 12, 1, tzinfo=timezone.utc)
TEST_FIRST_DAY = 153
RULE_REVIEW_AT = 50
RULE_BLOCK_AT = 80


def code(value):
    """Formats a code column as text: 150.0 becomes "150", missing becomes None."""
    if pd.isna(value):
        return None
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return str(value)


def requests_from(df):
    """Converts loaded transactions into score requests in the API format."""
    df = features.add_keys(df.copy())
    requests = []
    for row in df.itertuples(index=False):
        event_time = START + timedelta(seconds=int(row.TransactionDT))
        # D1 counts the days since the client began, the IEEE stand-in for account age.
        created = None if pd.isna(row.D1) else (event_time - timedelta(days=int(row.D1))).strftime("%Y-%m-%dT%H:%M:%SZ")
        request = {
            "transaction_id": str(row.TransactionID),
            "event_time": event_time.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "amount_minor": int(row.amount_minor),
            "player_id": row.player_id,
            "card_token": row.card_id,
            "card_issuer": code(row.card1),
            "card_network": code(row.card4),
            "card_type": code(row.card6),
            "card_country": code(row.card3),
            "card_category": code(row.card5),
            "product_code": code(row.ProductCD),
            "billing_region": code(row.addr1),
            "billing_country": code(row.addr2),
            "billing_distance": None if pd.isna(row.dist1) else float(row.dist1),
            "email_domain": code(row.P_emaildomain),
            "recipient_email_domain": code(row.R_emaildomain),
            "device_type": code(row.DeviceType),
            "device_name": code(row.DeviceInfo),
            "client_os": code(row.id_30),
            "client_browser": code(row.id_31),
            "screen_resolution": code(row.id_33),
            "account_created_at": created,
        }
        requests.append({name: value for name, value in request.items() if value is not None})
    return requests


class Client:
    def __init__(self, url, api_key):
        parsed = urlparse(url)
        self.connection = http.client.HTTPConnection(parsed.hostname, parsed.port or 80, timeout=30)
        self.headers = {"Content-Type": "application/json", "X-API-Key": api_key}

    def score(self, request, attempts=5):
        # A 500 or a lost connection saves nothing, so the purchase can be sent
        # again; this survives a database restart in the middle of a long replay.
        for attempt in range(1, attempts + 1):
            try:
                self.connection.request("POST", "/score", json.dumps(request), self.headers)
                response = self.connection.getresponse()
                body = response.read()
            except (OSError, http.client.HTTPException) as error:
                status, body = None, str(error).encode()
                self.connection.close()
            else:
                status = response.status
            if status is not None and status < 500:
                break
            if attempt == attempts:
                raise SystemExit(f"transaction {request['transaction_id']}: status {status}: {body.decode()}")
            print(f"transaction {request['transaction_id']}: status {status}, retrying in {2 * attempt}s")
            time.sleep(2 * attempt)
        if status == 409:
            raise SystemExit(
                f"transaction {request['transaction_id']} is already scored: start from an empty database "
                "(docker compose down -v, then up and migrate)"
            )
        if status != 200:
            raise SystemExit(f"transaction {request['transaction_id']}: status {status}: {body.decode()}")
        return json.loads(body)


def send(requests, client, progress_every=10000):
    results = []
    started = time.time()
    for i, request in enumerate(requests, 1):
        sent = time.perf_counter()
        response = client.score(request)
        results.append({
            "outcome": response["outcome"],
            "rule_score": response["score"],
            "live_score": response["model_score"],
            "latency_ms": (time.perf_counter() - sent) * 1000,
        })
        if i % progress_every == 0:
            rate = i / (time.time() - started)
            print(f"{i}/{len(requests)} sent, {rate:.0f} per second, about {(len(requests) - i) / rate / 60:.0f} min left")
    return pd.DataFrame(results)


def rule_outcome(rule_score):
    return np.where(rule_score >= RULE_BLOCK_AT, "block", np.where(rule_score >= RULE_REVIEW_AT, "review", "allow"))


def outcome_summary(outcome, is_fraud):
    stopped = np.isin(outcome, ["review", "block"])
    summary = {}
    for name in ["allow", "review", "block"]:
        chosen = outcome == name
        summary[name] = {
            "share": round(float(chosen.mean()), 4),
            "fraud_rate": round(float(is_fraud[chosen].mean()), 4) if chosen.any() else None,
        }
    summary["fraud_stopped"] = round(float(is_fraud[stopped].sum() / max(is_fraud.sum(), 1)), 4)
    summary["stopped_precision"] = round(float(is_fraud[stopped].mean()), 4) if stopped.any() else None
    return summary


def score_quality(is_fraud, score):
    return {
        "roc_auc": round(float(roc_auc_score(is_fraud, score)), 4),
        "pr_auc": round(float(average_precision_score(is_fraud, score)), 4),
    }


def build_report(rows, args, policy):
    diff = (rows["live_score"] - rows["python_score"]).abs()
    test = rows[rows["day"] >= TEST_FIRST_DAY]
    test_first = test[test["first_fraud_only"]]
    y = test["is_fraud"].to_numpy()
    model_only = np.where(test["python_score"] >= policy["flag_at"], "review", "allow")
    has_both_labels = len(set(y)) == 2
    return {
        "replayed_days": f"{int(rows['day'].min())}-{int(rows['day'].max())}",
        "history_days_before_test": TEST_FIRST_DAY - args.from_day,
        "purchases": int(len(rows)),
        "test_month_purchases": int(len(test)),
        "test_month_fraud_rate": round(float(y.mean()), 4) if len(y) else None,
        "live_vs_python": {
            "max_abs_difference": float(diff.max()),
            "mean_abs_difference": float(diff.mean()),
            "share_within_1e-9": round(float((diff <= 1e-9).mean()), 6),
            "share_within_1e-6": round(float((diff <= 1e-6).mean()), 6),
            "same_flag_decision": round(float(((rows["live_score"] >= policy["flag_at"]) == (rows["python_score"] >= policy["flag_at"])).mean()), 6),
        },
        "test_month": {
            "live_model_score": score_quality(y, test["live_score"]),
            "python_model_score": score_quality(y, test["python_score"]),
            "live_model_score_first_fraud_only": score_quality(test_first["is_fraud"], test_first["live_score"]),
            "rules_only": outcome_summary(rule_outcome(test["rule_score"].to_numpy()), y),
            "rules_and_model": outcome_summary(test["outcome"].to_numpy(), y),
            "model_flag_only": outcome_summary(model_only, y),
        } if has_both_labels else None,
        "latency_ms": {
            "p50": round(float(rows["latency_ms"].quantile(0.5)), 2),
            "p95": round(float(rows["latency_ms"].quantile(0.95)), 2),
            "p99": round(float(rows["latency_ms"].quantile(0.99)), 2),
        },
        "policy": policy,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("csv")
    parser.add_argument("--url", default="http://localhost:8080")
    parser.add_argument("--api-key", required=True)
    parser.add_argument("--from-day", type=int, default=TEST_FIRST_DAY - features.WARM_UP_DAYS,
                        help="first day to replay; the days before the test month build the history")
    parser.add_argument("--limit", type=int, help="replay only the first N purchases, for a quick check")
    parser.add_argument("--model", default="../backend/models/lgbm-ieee-v4-combined.json")
    parser.add_argument("--out", default="output/replay")
    args = parser.parse_args()
    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    data = features.load(args.csv)
    data = data[data["day"] >= args.from_day].reset_index(drop=True)
    if args.limit:
        data = data.head(args.limit)
    print(f"replaying {len(data)} purchases from day {args.from_day} to {args.url}")

    dump = json.loads(Path(args.model).read_text())
    policy = json.loads(Path(args.model).with_suffix(".report.json").read_text())["policy"]
    policy = {"flag_at": policy["flag_at"], "clear_below": policy["clear_below"]}

    built = features.build(data.copy())
    if "combination" in dump:
        # A combination of models: the highest percentile, as the service computes it.
        parts = [
            (json.loads((Path(args.model).parent / part["file"]).read_text()), np.array(part["percentiles"]))
            for part in dump["models"]
        ]
        python_score = export.combined_score(parts, built[dump["feature_names"]].to_numpy())
    else:
        python_score = export.predict_fast(dump, built[dump["feature_names"]].to_numpy())

    started = time.time()
    live = send(requests_from(data), Client(args.url, args.api_key))
    print(f"sent {len(live)} purchases in {(time.time() - started) / 60:.1f} min")

    rows = pd.DataFrame({
        "transaction_id": data["TransactionID"].to_numpy(),
        "day": data["day"].to_numpy(),
        "is_fraud": data["isFraud"].to_numpy(),
        "first_fraud_only": train.first_fraud_only(built).to_numpy(),
        "python_score": python_score,
    })
    rows = pd.concat([rows, live], axis=1)
    rows.to_csv(out_dir / "replay_rows.csv", index=False)

    report = build_report(rows, args, policy)
    (out_dir / "replay_report.json").write_text(json.dumps(report, indent=2))
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
