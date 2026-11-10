"""Checks how much of the game model's result comes from the simulator itself.

Three checks, all on simulated traffic:
  1. Leave one scenario out: train without one fraud scenario and see how much
     of it the model still catches.
  2. Empty database: score the first days of a world, when players have no
     history yet, with a model trained from day 0 (as train_game.py does) and
     with one trained only on later days.
  3. Realistic fraud share: train and test on traffic with far less fraud.

Generate the low-fraud traffic first (from the backend folder):
    go run ./cmd/simulator -dump ../ml/data/game/world3.jsonl -count 600000 -players 6000 -seed 31 -suspicious 0.015 -start 2026-01-01T00:00:00Z -run w3

Usage (from the ml folder):
    python check_game.py data/game/world1.jsonl --second-world data/game/world2.jsonl --realistic data/game/world3.jsonl
"""
import argparse
import json
import time
from pathlib import Path

import export
import features
import train
import train_game

REVIEW_RATE = 0.005
MAX_MISSED_FRAUD = 0.02


def fit(training, validation, checkpoint):
    """Trains one model and chooses its thresholds on validation."""
    model = train.fit_lightgbm(
        train.SEED,
        training[features.FEATURES], training["is_fraud"].to_numpy(),
        validation[features.FEATURES], validation["is_fraud"].to_numpy(),
        checkpoint,
    )
    y_val = validation["is_fraud"].to_numpy()
    score_val = model.predict_proba(validation[features.FEATURES])[:, 1]
    thresholds = export.choose_thresholds(y_val, score_val, REVIEW_RATE, MAX_MISSED_FRAUD, validation["amount"].to_numpy())
    return model, thresholds


def effect(model, thresholds, frame):
    """The policy's result on frame, with the flagged share of every scenario."""
    score = model.predict_proba(frame[features.FEATURES])[:, 1]
    report = train_game.policy_report(frame, score, thresholds)
    result = report["effect"]
    return {
        "purchases": int(len(frame)),
        "fraud_rate": report["evaluation"]["fraud_rate"],
        "pr_auc": report["evaluation"]["pr_auc"],
        "flagged_share": result["flagged_share"],
        "precision": result["flagged_precision"],
        "fraud_caught": result["flagged_fraud_caught"],
        "legit_flagged": legit_flagged(frame, score, thresholds),
        "scenarios": {row["scenario"]: row["flagged_share"] for row in report["scenarios"]},
    }


def legit_flagged(frame, score, thresholds):
    legit = (frame["is_fraud"] == 0).to_numpy()
    return round(float((score[legit] >= thresholds["flag_at"]).mean()), 4)


def leave_one_out(training, validation, test, checkpoints, full):
    """Trains without each fraud scenario in turn. The scenario is left out of
    validation too, so the thresholds never see it."""
    rows = []
    for scenario in sorted(training.loc[training["is_fraud"] == 1, "pattern"].unique()):
        model, thresholds = fit(
            training[training["pattern"] != scenario], validation[validation["pattern"] != scenario],
            checkpoints / f"without-{scenario}.joblib",
        )
        result = effect(model, thresholds, test)
        rows.append({
            "scenario": scenario,
            "caught_when_trained_on_it": full["scenarios"][scenario],
            "caught_when_left_out": result["scenarios"][scenario],
            "legit_flagged_when_left_out": result["legit_flagged"],
        })
        print(f"left out {scenario}: caught {result['scenarios'][scenario]:.1%} (with it {full['scenarios'][scenario]:.1%})")
    return rows


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("requests", help="the simulator dump the game model is trained on")
    parser.add_argument("--second-world", required=True, help="a dump with another seed, scored from its first purchase")
    parser.add_argument("--realistic", help="a dump with a realistic fraud share, trained and tested on its own")
    parser.add_argument("--out", default="output")
    args = parser.parse_args()
    out_dir = Path(args.out)
    checkpoints = out_dir / "checkpoints" / "game-checks"
    days = train_game.SPLIT_DAYS
    started = time.time()

    data = train_game.load(args.requests)
    training, validation, test = features.split(data, days)
    model, thresholds = fit(training, validation, checkpoints / "full.joblib")
    full = effect(model, thresholds, test)
    report = {"data": "simulated game traffic", "review_rate": REVIEW_RATE, "trained_on_every_scenario": full}
    print(f"full model ready, {time.time() - started:.0f}s")

    report["leave_one_scenario_out"] = leave_one_out(training, validation, test, checkpoints, full)

    # A world's first days are what the service sees on an empty database.
    world = train_game.load(args.second_world)
    first_days = world[world["day"] < train_game.FIRST_DAYS]
    later_days = world[world["day"] >= train_game.FIRST_DAYS]
    late_model, late_thresholds = fit(
        training[training["day"] >= train_game.FIRST_DAYS], validation, checkpoints / "from-day-14.joblib"
    )
    report["empty_database"] = {
        "trained_from_day_0": {
            "first_14_days_of_second_world": effect(model, thresholds, first_days),
            "later_days_of_second_world": effect(model, thresholds, later_days),
            "test_days_of_first_world": full,
        },
        "trained_from_day_14": {
            "first_14_days_of_second_world": effect(late_model, late_thresholds, first_days),
            "later_days_of_second_world": effect(late_model, late_thresholds, later_days),
            "test_days_of_first_world": effect(late_model, late_thresholds, test),
        },
    }
    print(f"empty database check ready, {time.time() - started:.0f}s")

    if args.realistic:
        low = train_game.load(args.realistic)
        low_train, low_val, low_test = features.split(low, days)
        low_model, low_thresholds = fit(low_train, low_val, checkpoints / "realistic.joblib")
        report["realistic_fraud_share"] = {
            "trained_and_tested_on_it": effect(low_model, low_thresholds, low_test),
            "game_model_trained_on_the_first_world": effect(model, thresholds, low_test),
        }
        print(f"realistic share check ready, {time.time() - started:.0f}s")

    out_dir.mkdir(exist_ok=True)
    (out_dir / "game_checks.json").write_text(json.dumps(report, indent=2))
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
