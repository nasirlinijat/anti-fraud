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

With several dumps, each one is a training world. The settings are chosen by
leaving one world out, the thresholds come from the scores of models that never
saw the scored world, and the final model is trained on every world:
    python train_game.py data/game/world1.jsonl data/game/worldB.jsonl data/game/worldC.jsonl \
        --history-starts 0,40,80 --development path/to/development.jsonl
"""
import argparse
import json
import time
from pathlib import Path

import numpy as np
from sklearn.metrics import average_precision_score, roc_auc_score

import export
import features
import train
import worlds

# Days of the first simulated world; the days between the ranges keep
# training, validation and test apart. Training starts at day 0, when no
# player has history yet, because the service starts the same way on an empty
# database: a model trained only on later days flags players for having no
# history.
SPLIT_DAYS = {"train": (0, 84), "validation": (90, 104), "test": (105, 124)}
# The first days of a world, scored separately as the empty-database case.
FIRST_DAYS = 14
# The first days of a development dump, reported apart as the cold start.
DEVELOPMENT_FIRST_DAYS = 7
SINGLE_WORLD_VERSION = "lgbm-game-v5"
SINGLE_WORLD_COMPARE = "../backend/models/lgbm-ieee-v5-combined.json"
WORLDS_VERSION = "lgbm-game-v6"
WORLDS_COMPARE = "../backend/models/lgbm-game-v5.json"


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
    parser.add_argument("requests", nargs="+",
                        help="simulator dumps (JSON Lines): one is split by day into training, validation and test; "
                             "several are training worlds compared by leaving one world out")
    parser.add_argument("--second-world", help="one dump only: another simulator dump with a different seed, only used for testing")
    parser.add_argument("--compare",
                        help=f"exported model scored for comparison, {SINGLE_WORLD_COMPARE} with one dump and "
                             f"{WORLDS_COMPARE} on the development dumps with several; empty to skip")
    parser.add_argument("--development", nargs="+", default=[],
                        help="several dumps only: labelled dumps that are only scored, never used for training, "
                             "tree counts, settings or thresholds")
    parser.add_argument("--out", default="output")
    parser.add_argument("--version", help=f"{SINGLE_WORLD_VERSION} with one dump, {WORLDS_VERSION} with several")
    parser.add_argument("--models-dir", default="../backend/models")
    parser.add_argument("--models", type=int, default=5, help="number of models averaged into the model (1 to 5)")
    parser.add_argument("--review-rate", type=float, default=0.005)
    parser.add_argument("--max-missed-fraud", type=float, default=0.02)
    parser.add_argument("--target-precision", type=float)
    parser.add_argument("--train-days", help="first-last day used for training, default 0-84")
    parser.add_argument("--validation-days", help="first-last validation day, default 90-104")
    parser.add_argument("--test-days", help="first-last test day, default 105-124")
    worlds.add_world_arguments(parser)
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
            parser.error(f"days {sets.days[name]} of {args.requests[0]} need both fraud and legitimate purchases; "
                         f"the file covers days {data['day'].min()}-{data['day'].max()}")


def new_report(args, sets):
    """Starts the training report with the data it describes and the baseline."""
    data = sets.data
    report = {
        "features": features.FEATURES,
        "data": "simulated game traffic",
        "source": {
            "file": Path(args.requests[0]).name,
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


def train_one_world(parser, args):
    """Trains on the early days of one world and tests on its later days."""
    out_dir = Path(args.out)
    out_dir.mkdir(exist_ok=True)
    days = split_days(args)

    started = time.time()
    sets = train.Sets(load(args.requests[0]), days)
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


def check_mode_and_fill_defaults(parser, args):
    """Stops the run on options that belong to the other kind of training and
    fills in the defaults that depend on it."""
    single = len(args.requests) == 1
    if single and args.development:
        parser.error("--development needs several training worlds")
    world_only = [args.history_starts != "0", args.history_window != worlds.DEFAULT_HISTORY_WINDOW,
                  args.max_trees != worlds.DEFAULT_MAX_TREES]
    if single and any(world_only):
        parser.error("--history-starts, --history-window and --max-trees need several training worlds")
    day_options = [args.second_world, args.train_days, args.validation_days, args.test_days]
    if not single and any(day_options):
        parser.error("--second-world and the day ranges need a single dump")
    if args.version is None:
        args.version = SINGLE_WORLD_VERSION if single else WORLDS_VERSION
    if args.compare is None:
        args.compare = SINGLE_WORLD_COMPARE if single else WORLDS_COMPARE


def train_on_worlds(parser, args):
    """Chooses the settings by leaving one world out, then trains the final
    model on every world. The development dumps are read only after the model
    and its thresholds are final."""
    starts = worlds.check_world_arguments(parser, args, args.requests)
    out_dir = Path(args.out)
    out_dir.mkdir(exist_ok=True)
    checkpoints = out_dir / "checkpoints" / args.version
    started = time.time()

    data, infos = worlds.load_worlds(args.requests, starts, args.history_window)
    check_world_labels(parser, data)
    print(f"features built in {time.time() - started:.0f}s: {len(data)} rows")
    selection = worlds.Selection(args.max_trees, args.review_rate, checkpoints / "lowo")
    results, winner = worlds.select_candidate(data, worlds.candidate_grid(), selection)
    selection_seconds = time.time() - started
    policy = out_of_world_policy(args, data, winner)
    final = worlds.with_trees(winner.candidate, winner.trees)
    models = fit_final_models(final, data, args.models, checkpoints)

    report = worlds_report(args, infos, starts)
    report["selection"] = selection_report(results, winner, selection, selection_seconds)
    report["policy"] = policy
    report["lightgbm"] = final_model_report(models, final)
    if args.development:
        report["development"] = development_report(args.development, development_scorers(args, models, policy))

    (out_dir / "game_training_report.json").write_text(json.dumps(report, indent=2))
    golden = export.synthetic_rows(data[features.FEATURES], 2000, train.SEED)
    model_path, _ = export.export(models, features.FEATURES, args.version, args.models_dir, golden, report)
    print_worlds_summary(report)
    print(f"model written to {model_path}")
    print(f"done in {time.time() - started:.0f}s")


def check_world_labels(parser, data):
    for world, part in data.groupby("world", sort=False):
        if part["is_fraud"].nunique() < 2:
            parser.error(f"world {world} needs both fraud and legitimate purchases")


def out_of_world_policy(args, data, winner):
    """Thresholds chosen on the pooled out-of-world scores of the winning
    candidate: every purchase is scored by the fold model that never saw its
    world, so the thresholds fit traffic the model does not know."""
    rows = data.loc[winner.scores.index]
    y, score, amount = rows["is_fraud"].to_numpy(), winner.scores.to_numpy(), rows["amount"].to_numpy()
    sample = worlds.equal_world_sample(rows, train.SEED)
    thresholds = export.choose_thresholds(
        sample["is_fraud"].to_numpy(), winner.scores.loc[sample.index].to_numpy(), args.review_rate,
        args.max_missed_fraud, sample["amount"].to_numpy(), target_precision=args.target_precision,
    )
    per_world = {}
    for world in rows["world"].unique():
        chosen = (rows["world"] == world).to_numpy()
        per_world[world] = export.policy_effect(y[chosen], score[chosen], thresholds, amount[chosen])
    return {
        **thresholds,
        "chosen_on": f"pooled out-of-world scores of {winner.candidate.name} at {winner.trees} trees, one seed: "
                     "each world scored by the model trained without it; the same number of rows is drawn from every world",
        "out_of_world_effect": export.policy_effect(y, score, thresholds, amount),
        "out_of_world_effect_per_world": per_world,
    }


def fit_final_models(candidate, data, count, checkpoints):
    """The final model is the average of models with different seeds, trained
    on every row of every world for the chosen tree count."""
    return [
        worlds.fit_model(candidate, data, seed, checkpoints / f"main-seed{seed}.joblib")
        for seed in train.AVERAGE_SEEDS[:count]
    ]


def worlds_report(args, infos, starts):
    """Starts the training report with the worlds it was trained on."""
    return {
        "features": features.FEATURES,
        "data": "simulated game traffic from several worlds",
        "worlds": infos,
        "history_starts": starts,
        "history_window_days": args.history_window,
        "weighting": "every world has the same total row weight in the loss; its history variants share it",
    }


def selection_report(results, winner, selection, seconds):
    return {
        "criterion": "worst held-out world PR-AUC, leaving one world out",
        "caveat": "the held-out worlds choose the tree count and the candidate, so these numbers are optimistic "
                  "and are not a test result",
        "seed": train.SEED,
        "max_trees": selection.max_trees,
        "tree_step": worlds.TREE_STEP,
        "review_rate": selection.review_rate,
        "fits": len(results) * len(winner.held_out),
        "seconds": round(seconds),
        "candidates": [candidate_report(result) for result in results],
        "chosen": candidate_report(winner),
    }


def candidate_report(result):
    candidate = result.candidate
    return {
        "name": candidate.name,
        "masked_inputs": list(candidate.masked),
        "params": candidate.params,
        "trees": result.trees,
        "worst_pr_auc": result.worst_pr_auc,
        "held_out": result.held_out,
    }


def final_model_report(models, candidate):
    importance = np.mean([m.booster_.feature_importance(importance_type="gain") for m in models], axis=0)
    return {
        "models": len(models),
        "trees_per_model": candidate.params["n_estimators"],
        "masked_inputs": list(candidate.masked),
        "params": candidate.params,
        "feature_importance_gain": {
            name: round(float(value), 1)
            for name, value in sorted(zip(features.FEATURES, importance), key=lambda item: -item[1])
        },
    }


def development_scorers(args, models, policy):
    """(version, policy, scoring function) of the final model and of the
    compared model."""
    scorers = [(args.version, policy, lambda frame: export.average_proba(models, frame[features.FEATURES]))]
    if args.compare:
        scorers.append(exported_model(args.compare))
    return scorers


def development_report(paths, scorers):
    """Scores each development dump with every scorer and its own policy."""
    return [development_dump_report(path, scorers) for path in paths]


def development_dump_report(path, scorers):
    frame = load(path)
    return {
        "file": Path(path).name,
        "purchases": int(len(frame)),
        "days": f"{int(frame['day'].min())}-{int(frame['day'].max())}",
        "fraud_rate": round(float(frame["is_fraud"].mean()), 4),
        "models": {version: scored_report(frame, score(frame), policy) for version, policy, score in scorers},
    }


def scored_report(frame, score, policy):
    first = (frame["day"] < DEVELOPMENT_FIRST_DAYS).to_numpy()
    return {
        "flag_at": policy["flag_at"],
        "all_days": part_report(frame, score, policy),
        f"first_{DEVELOPMENT_FIRST_DAYS}_days": part_report(frame[first], score[first], policy),
        "later_days": part_report(frame[~first], score[~first], policy),
    }


def part_report(frame, score, policy):
    y = frame["is_fraud"].to_numpy()
    flagged = score >= policy["flag_at"]
    effect = export.policy_effect(y, score, policy)
    return {
        "purchases": int(len(y)),
        "fraud_rate": round(float(y.mean()), 4) if len(y) else None,
        **ranking(y, score),
        "review_rate": effect["flagged_share"],
        "precision": effect["flagged_precision"],
        "fraud_caught": effect["flagged_fraud_caught"],
        "legit_flagged": round(float(flagged[y == 0].mean()), 4) if (y == 0).any() else None,
        "schemes": scheme_reports(frame, score, flagged),
    }


def ranking(y, score):
    if len(y) == 0 or y.min() == y.max():
        return {"roc_auc": None, "pr_auc": None}
    return {"roc_auc": round(float(roc_auc_score(y, score)), 4), "pr_auc": round(float(average_precision_score(y, score)), 4)}


def scheme_reports(frame, score, flagged):
    """Per fraud scheme: its fraud against every legitimate purchase."""
    y, pattern = frame["is_fraud"].to_numpy(), frame["pattern"].to_numpy()
    rows = []
    for scheme in sorted(set(pattern[y == 1])):
        fraud = (pattern == scheme) & (y == 1)
        chosen = fraud | (y == 0)
        rows.append({
            "scheme": scheme,
            "purchases": int(fraud.sum()),
            **ranking(y[chosen], score[chosen]),
            "fraud_caught": round(float(flagged[fraud].mean()), 4),
        })
    return rows


def print_worlds_summary(report):
    print("candidate                                          trees  worst PR-AUC  " + "  ".join(report["selection"]["chosen"]["held_out"]))
    for row in report["selection"]["candidates"]:
        per_world = "  ".join(f"{result['pr_auc']:.4f}" for result in row["held_out"].values())
        print(f"{row['name']:50s} {row['trees']:5d}  {row['worst_pr_auc']:12.4f}  {per_world}")
    policy = report["policy"]
    print(f"chosen {report['selection']['chosen']['name']}; flag_at {policy['flag_at']}, clear_below {policy['clear_below']}")
    print(json.dumps({"out_of_world_effect_per_world": policy["out_of_world_effect_per_world"]}, indent=2))
    for dump in report.get("development", []):
        for version, result in dump["models"].items():
            for part in ("all_days", f"first_{DEVELOPMENT_FIRST_DAYS}_days", "later_days"):
                r = result[part]
                print(f"development {dump['file']} {version} {part}: ROC-AUC {r['roc_auc']}, PR-AUC {r['pr_auc']}, "
                      f"caught {r['fraud_caught']}, precision {r['precision']}, review rate {r['review_rate']}")


def main():
    parser = build_parser()
    args = parser.parse_args()
    check_mode_and_fill_defaults(parser, args)
    if len(args.requests) == 1:
        train_one_world(parser, args)
    else:
        train_on_worlds(parser, args)


if __name__ == "__main__":
    main()
