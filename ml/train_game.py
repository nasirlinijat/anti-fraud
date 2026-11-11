"""Trains the fraud model on simulated game traffic.

The simulator writes labelled purchases in the score API format. This script
builds the same features the service computes, trains LightGBM on the earlier
days and reports the result on later days, per scenario. Every result here is
for simulated traffic only.

Generate the traffic first (from the backend folder):
    go run ./cmd/simulator -dump ../ml/data/game/world1.jsonl -count 600000 -players 6000 -seed 11 -start 2026-01-01T00:00:00Z -run w1
    go run ./cmd/simulator -dump ../ml/data/game/world2.jsonl -count 150000 -players 2500 -seed 23 -start 2026-01-01T00:00:00Z -run w2

Usage (from the ml folder):
    python train_game.py data/game/world1.jsonl --second-world data/game/world2.jsonl
"""
import argparse
import json
import time
from pathlib import Path

import numpy as np

import export
import features
import train

# Days of the first simulated world: days 0-13 only build history, and the
# days between the ranges keep training, validation and test apart.
SPLIT_DAYS = {"train": (14, 84), "validation": (90, 104), "test": (105, 124)}
WARM_UP_DAYS = 14


def load(path):
    """Builds the features of a simulator dump and keeps each purchase's scenario."""
    raw = features.load_requests(path)
    data = features.build(raw.copy())
    data["pattern"] = raw["pattern"].to_numpy()
    return data


def by_scenario(frame, score, thresholds):
    """Per scenario: how many purchases the policy flags and how many score
    below clear_below."""
    flagged = score >= thresholds["flag_at"]
    below = score < thresholds["clear_below"]
    rows = []
    for pattern in sorted(frame["pattern"].unique()):
        chosen = (frame["pattern"] == pattern).to_numpy()
        rows.append({
            "scenario": pattern,
            "fraud": bool(frame["is_fraud"].to_numpy()[chosen].max()),
            "purchases": int(chosen.sum()),
            "flagged": int(flagged[chosen].sum()),
            "flagged_share": round(float(flagged[chosen].mean()), 4),
            "below_clear_below_share": round(float(below[chosen].mean()), 4),
        })
    return rows


def policy_report(frame, score, thresholds):
    y, amount = frame["is_fraud"].to_numpy(), frame["amount"].to_numpy()
    return {
        "evaluation": train.evaluate(y, score, amount),
        "effect": export.policy_effect(y, score, thresholds, amount),
        "scenarios": by_scenario(frame, score, thresholds),
    }


def exported_model(path):
    """Returns a scoring function and the policy of a model exported to
    backend/models: a single model or a combination."""
    path = Path(path)
    dump = json.loads(path.read_text())
    policy = json.loads(path.with_suffix(".report.json").read_text())["policy"]
    if "combination" not in dump:
        return dump["model_version"], policy, lambda frame: export.predict_fast(dump, frame[dump["feature_names"]].to_numpy())
    parts = [(json.loads((path.parent / part["file"]).read_text()), np.array(part["percentiles"])) for part in dump["models"]]

    def score(frame):
        return export.combined_score(
            parts, frame[dump["feature_names"]].to_numpy(), frame["player_id"].to_numpy(), policy.get("player_blend_weight", 0)
        )

    return dump["model_version"], policy, score


def days_between(value, default):
    if not value:
        return default
    first, last = (int(part) for part in value.split("-"))
    return first, last


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("requests", help="simulator dump (JSON Lines) to train, validate and test on")
    parser.add_argument("--second-world", help="another simulator dump with a different seed, only used for testing")
    parser.add_argument("--compare", default="../backend/models/lgbm-ieee-v5-combined.json",
                        help="exported model scored on the same test days for comparison; empty to skip")
    parser.add_argument("--out", default="output")
    parser.add_argument("--version", default="lgbm-game-v1")
    parser.add_argument("--models-dir", default="../backend/models")
    parser.add_argument("--models", type=int, default=5, help="number of models averaged into the model (1 to 5)")
    parser.add_argument("--review-rate", type=float, default=0.005)
    parser.add_argument("--max-missed-fraud", type=float, default=0.02)
    parser.add_argument("--target-precision", type=float)
    parser.add_argument("--train-days", help="first-last day used for training, default 14-84")
    parser.add_argument("--validation-days", help="first-last validation day, default 90-104")
    parser.add_argument("--test-days", help="first-last test day, default 105-124")
    args = parser.parse_args()
    out_dir = Path(args.out)
    out_dir.mkdir(exist_ok=True)
    days = {
        "train": days_between(args.train_days, SPLIT_DAYS["train"]),
        "validation": days_between(args.validation_days, SPLIT_DAYS["validation"]),
        "test": days_between(args.test_days, SPLIT_DAYS["test"]),
    }

    started = time.time()
    data = load(args.requests)
    training, validation, test = features.split(data, days)
    for name, part in [("train", training), ("validation", validation), ("test", test)]:
        if part["is_fraud"].nunique() < 2:
            parser.error(f"days {days[name]} of {args.requests} need both fraud and legitimate purchases; "
                         f"the file covers days {data['day'].min()}-{data['day'].max()}")
    print(f"features built in {time.time() - started:.0f}s: train {len(training)}, validation {len(validation)}, test {len(test)}")

    x_train, y_train = training[features.FEATURES], training["is_fraud"].to_numpy()
    x_val, y_val = validation[features.FEATURES], validation["is_fraud"].to_numpy()
    x_test, y_test = test[features.FEATURES], test["is_fraud"].to_numpy()
    val_amount, test_amount = validation["amount"].to_numpy(), test["amount"].to_numpy()

    report = {
        "features": features.FEATURES,
        "data": "simulated game traffic",
        "source": {
            "file": Path(args.requests).name,
            "purchases": int(len(data)),
            "days": f"{int(data['day'].min())}-{int(data['day'].max())}",
            "fraud_rate": round(float(data["is_fraud"].mean()), 4),
        },
        "split_days": {name: f"{first}-{last}" for name, (first, last) in days.items()},
    }

    baseline = train.baseline_model().fit(x_train, y_train)
    report["logistic_regression"] = {
        "validation": train.evaluate(y_val, baseline.predict_proba(x_val)[:, 1], val_amount),
        "test": train.evaluate(y_test, baseline.predict_proba(x_test)[:, 1], test_amount),
    }

    # Every finished model is saved, so an interrupted run continues after it.
    checkpoints = out_dir / "checkpoints" / args.version
    models = []
    for seed in train.AVERAGE_SEEDS[: args.models]:
        models.append(train.fit_lightgbm(seed, x_train, y_train, x_val, y_val, checkpoints / f"main-seed{seed}.joblib"))
        print(f"model {len(models)} of {args.models} ready, {time.time() - started:.0f}s")
    model_val = export.average_proba(models, x_val)
    model_test = export.average_proba(models, x_test)
    importance = np.mean([m.booster_.feature_importance(importance_type="gain") for m in models], axis=0)
    report["lightgbm"] = {
        "models": len(models),
        "best_iterations": [int(m.best_iteration_) for m in models],
        "validation": train.evaluate(y_val, model_val, val_amount),
        "test": train.evaluate(y_test, model_test, test_amount),
        "feature_importance_gain": {
            name: round(float(value), 1)
            for name, value in sorted(zip(features.FEATURES, importance), key=lambda item: -item[1])
        },
    }

    # Thresholds come from validation only; the test days and the second world
    # are scored once with them.
    thresholds = export.choose_thresholds(
        y_val, model_val, args.review_rate, args.max_missed_fraud, val_amount, target_precision=args.target_precision
    )
    report["policy"] = {
        **thresholds,
        "validation_effect": export.policy_effect(y_val, model_val, thresholds, val_amount),
        "test_effect": export.policy_effect(y_test, model_test, thresholds, test_amount),
    }
    report["test_scenarios"] = by_scenario(test, model_test, thresholds)
    report["precision_targets"] = export.precision_targets(y_val, model_val, y_test, model_test, test_amount)

    if args.second_world:
        world = load(args.second_world)
        world = world[world["day"] >= WARM_UP_DAYS]
        report["second_world"] = {
            "file": Path(args.second_world).name,
            "days": f"{int(world['day'].min())}-{int(world['day'].max())}",
            **policy_report(world, export.average_proba(models, world[features.FEATURES]), thresholds),
        }

    if args.compare:
        version, policy, score = exported_model(args.compare)
        # The compared model may blend a player's earlier scores, so it scores
        # every purchase after the training days in time order.
        window = data[data["day"] > days["train"][1]]
        scores = score(window)[(window["day"] >= days["test"][0]).to_numpy() & (window["day"] <= days["test"][1]).to_numpy()]
        report["compared_model"] = {
            "version": version,
            "flag_at": policy["flag_at"],
            "clear_below": policy["clear_below"],
            **policy_report(test, scores, policy),
        }

    (out_dir / "game_training_report.json").write_text(json.dumps(report, indent=2))
    golden = export.synthetic_rows(x_train, 2000, train.SEED)
    model_path, _ = export.export(models, features.FEATURES, args.version, args.models_dir, golden, report)

    summary = {"policy": report["policy"], "test_scenarios": report["test_scenarios"]}
    for name in ("second_world", "compared_model"):
        if name in report:
            summary[name] = {k: report[name][k] for k in ("effect", "scenarios")}
    print(json.dumps(summary, indent=2))
    print(f"model written to {model_path}")
    print(f"done in {time.time() - started:.0f}s")


if __name__ == "__main__":
    main()
