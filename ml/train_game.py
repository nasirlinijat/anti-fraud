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

# Days of the first simulated world; the days between the ranges keep
# training, validation and test apart. Training starts at day 0, when no
# player has history yet, because the service starts the same way on an empty
# database: a model trained only on later days flags players for having no
# history.
SPLIT_DAYS = {"train": (0, 84), "validation": (90, 104), "test": (105, 124)}
# The first days of a world, scored separately as the empty-database case.
FIRST_DAYS = 14


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


def build_parser():
    parser = argparse.ArgumentParser()
    parser.add_argument("requests", help="simulator dump (JSON Lines) to train, validate and test on")
    parser.add_argument("--second-world", help="another simulator dump with a different seed, only used for testing")
    parser.add_argument("--compare", default="../backend/models/lgbm-ieee-v5-combined.json",
                        help="exported model scored on the same test days for comparison; empty to skip")
    parser.add_argument("--out", default="output")
    parser.add_argument("--version", default="lgbm-game-v5")
    parser.add_argument("--models-dir", default="../backend/models")
    parser.add_argument("--models", type=int, default=5, help="number of models averaged into the model (1 to 5)")
    parser.add_argument("--review-rate", type=float, default=0.005)
    parser.add_argument("--max-missed-fraud", type=float, default=0.02)
    parser.add_argument("--target-precision", type=float)
    parser.add_argument("--train-days", help="first-last day used for training, default 0-84")
    parser.add_argument("--validation-days", help="first-last validation day, default 90-104")
    parser.add_argument("--test-days", help="first-last test day, default 105-124")
    return parser


def split_days(args):
    return {
        "train": days_between(args.train_days, SPLIT_DAYS["train"]),
        "validation": days_between(args.validation_days, SPLIT_DAYS["validation"]),
        "test": days_between(args.test_days, SPLIT_DAYS["test"]),
    }


def check_both_labels(parser, args, sets):
    """Stops the run when a set has only fraud or only legitimate purchases,
    which the model and its evaluation cannot use."""
    data = sets.data
    for name, part in [("train", sets.train), ("validation", sets.validation), ("test", sets.test)]:
        if part["is_fraud"].nunique() < 2:
            parser.error(f"days {sets.days[name]} of {args.requests} need both fraud and legitimate purchases; "
                         f"the file covers days {data['day'].min()}-{data['day'].max()}")


def new_report(args, sets):
    """Starts the training report with the data it describes and the baseline."""
    data = sets.data
    report = {
        "features": features.FEATURES,
        "data": "simulated game traffic",
        "source": {
            "file": Path(args.requests).name,
            "purchases": int(len(data)),
            "days": f"{int(data['day'].min())}-{int(data['day'].max())}",
            "fraud_rate": round(float(data["is_fraud"].mean()), 4),
        },
        "split_days": {name: f"{first}-{last}" for name, (first, last) in sets.days.items()},
    }
    baseline = train.baseline_model().fit(sets.x_train, sets.y_train)
    report["logistic_regression"] = {
        "validation": train.evaluate(sets.y_val, baseline.predict_proba(sets.x_val)[:, 1], sets.val_amount),
        "test": train.evaluate(sets.y_test, baseline.predict_proba(sets.x_test)[:, 1], sets.test_amount),
    }
    return report


def fit_models(sets, checkpoints, count, started):
    """Trains the models that are averaged into the game model. Every finished
    model is saved, so an interrupted run continues after it."""
    models = []
    for seed in train.AVERAGE_SEEDS[:count]:
        models.append(train.fit_lightgbm(
            seed, sets.x_train, sets.y_train, sets.x_val, sets.y_val, checkpoints / f"main-seed{seed}.joblib"
        ))
        print(f"model {len(models)} of {count} ready, {time.time() - started:.0f}s")
    return models


def lightgbm_report(models, sets, scores):
    importance = np.mean([m.booster_.feature_importance(importance_type="gain") for m in models], axis=0)
    return {
        "models": len(models),
        "best_iterations": [int(m.best_iteration_) for m in models],
        "validation": train.evaluate(sets.y_val, scores.val, sets.val_amount),
        "test": train.evaluate(sets.y_test, scores.test, sets.test_amount),
        "feature_importance_gain": {
            name: round(float(value), 1)
            for name, value in sorted(zip(features.FEATURES, importance), key=lambda item: -item[1])
        },
    }


def second_world_report(path, models, thresholds):
    world = load(path)
    report = {"file": Path(path).name}
    # The first days are what the service sees on an empty database.
    for name, part in [("first_days", world[world["day"] < FIRST_DAYS]), ("later_days", world[world["day"] >= FIRST_DAYS])]:
        report[name] = {
            "days": f"{int(part['day'].min())}-{int(part['day'].max())}",
            **policy_report(part, export.average_proba(models, part[features.FEATURES]), thresholds),
        }
    return report


def compared_model_report(path, sets):
    """Scores the test days with an exported model and its own policy."""
    version, policy, score = exported_model(path)
    data, days = sets.data, sets.days
    # The compared model may blend a player's earlier scores, so it scores
    # every purchase after the training days in time order.
    window = data[data["day"] > days["train"][1]]
    scores = score(window)[(window["day"] >= days["test"][0]).to_numpy() & (window["day"] <= days["test"][1]).to_numpy()]
    return {
        "version": version,
        "flag_at": policy["flag_at"],
        "clear_below": policy["clear_below"],
        **policy_report(sets.test, scores, policy),
    }


def summary(report):
    """The part of the report printed at the end of a run."""
    result = {"policy": report["policy"], "test_scenarios": report["test_scenarios"]}
    if "second_world" in report:
        result["second_world"] = {name: report["second_world"][name]["effect"] for name in ("first_days", "later_days")}
    if "compared_model" in report:
        result["compared_model"] = report["compared_model"]["effect"]
    return result


def main():
    parser = build_parser()
    args = parser.parse_args()
    out_dir = Path(args.out)
    out_dir.mkdir(exist_ok=True)
    days = split_days(args)

    started = time.time()
    sets = train.Sets(load(args.requests), days)
    check_both_labels(parser, args, sets)
    print(f"features built in {time.time() - started:.0f}s: train {len(sets.train)}, validation {len(sets.validation)}, test {len(sets.test)}")

    report = new_report(args, sets)
    models = fit_models(sets, out_dir / "checkpoints" / args.version, args.models, started)
    scores = train.Scores(export.average_proba(models, sets.x_val), export.average_proba(models, sets.x_test))
    report["lightgbm"] = lightgbm_report(models, sets, scores)

    # Thresholds come from validation only; the test days and the second world
    # are scored once with them.
    thresholds = export.choose_thresholds(
        sets.y_val, scores.val, args.review_rate, args.max_missed_fraud, sets.val_amount, target_precision=args.target_precision
    )
    report["policy"] = train.policy_with_effects(sets, thresholds, scores)
    report["test_scenarios"] = by_scenario(sets.test, scores.test, thresholds)
    report["precision_targets"] = export.precision_targets(sets.y_val, scores.val, sets.y_test, scores.test, sets.test_amount)
    if args.second_world:
        report["second_world"] = second_world_report(args.second_world, models, thresholds)
    if args.compare:
        report["compared_model"] = compared_model_report(args.compare, sets)

    (out_dir / "game_training_report.json").write_text(json.dumps(report, indent=2))
    golden = export.synthetic_rows(sets.x_train, 2000, train.SEED)
    model_path, _ = export.export(models, features.FEATURES, args.version, args.models_dir, golden, report)

    print(json.dumps(summary(report), indent=2))
    print(f"model written to {model_path}")
    print(f"done in {time.time() - started:.0f}s")


if __name__ == "__main__":
    main()
