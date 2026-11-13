"""Combines a multi-world game model with the single-world game model and
exports the combination for the Go service.

The two kinds of model catch different fraud schemes: the single-world model
is strong on the simulator's velocity schemes, the multi-world model on
traffic it does not know. The combination scores a purchase with both and
keeps the higher percentile. Each model's percentile table and the thresholds
come from a reference set on which each model's scores are as out-of-sample
as possible, because thresholds chosen on scores of rows a model trained on
are too strict on new traffic.

Run it after both models are exported, with the worlds and history options
the multi-world model was trained with (from the ml folder):
    python combine_game.py data/game/world1.jsonl data/game/worldB.jsonl data/game/worldC.jsonl \
        --history-starts 0,20,40,60,80,100 --multi lgbm-game-v6 --single lgbm-game-v5
"""
import argparse
import json
from collections import namedtuple
from pathlib import Path

import numpy as np

import export
import features
import train
import train_game
import worlds

DEFAULT_SINGLE = "lgbm-game-v5"
DEFAULT_REVIEW_RATE = 0.01
# Quantiles of legitimate rows' percentiles shown per world in the report.
PERCENTILE_QUANTILES = [0.5, 0.9, 0.99]
# Rounding slack when the recomputed held-out results are compared with the
# multi-world report, which rounds them to four places.
REPORT_TOLERANCE = 1e-4

# A part of the combination: its version, model dump and training report.
Part = namedtuple("Part", ["version", "dump", "report"])
# The reference rows, each part's scores on them, the parts' percentile
# tables and the combined score.
Reference = namedtuple("Reference", ["rows", "scores", "tables", "combined"])


def build_parser():
    parser = argparse.ArgumentParser()
    parser.add_argument("requests", nargs="+",
                        help="the simulator dumps the multi-world model was trained on, in the same order")
    parser.add_argument("--multi", required=True, help="version of the multi-world model, such as lgbm-game-v7")
    parser.add_argument("--single", default=DEFAULT_SINGLE, help="version of the single-world model")
    parser.add_argument("--version", help="version of the combination, default <multi>-combined")
    parser.add_argument("--review-rate", type=float, default=DEFAULT_REVIEW_RATE,
                        help="share of legitimate reference purchases the combination flags; both models share it")
    parser.add_argument("--max-missed-fraud", type=float, default=train_game.DEFAULT_MAX_MISSED_FRAUD)
    parser.add_argument("--development", nargs="+", default=[],
                        help="labelled dumps that are only scored, never used for the tables or thresholds")
    parser.add_argument("--out", default="output", help="folder whose checkpoints hold the multi-world fold models")
    parser.add_argument("--models-dir", default="../backend/models",
                        help="folder with both exported models; the combination is written next to them")
    worlds.add_world_arguments(parser)
    return parser


def load_part(parser, models_dir, version):
    path = Path(models_dir) / f"{version}.json"
    if not path.exists() or not path.with_suffix(".report.json").exists():
        parser.error(f"{path} and its report are needed")
    return Part(version, json.loads(path.read_text()), json.loads(path.with_suffix(".report.json").read_text()))


def check_options(parser, args, multi, single):
    """Stops the run when the parts or the options do not match the
    multi-world training, so that the fold checkpoints are the ones its report
    describes. Returns the history start days."""
    starts = worlds.check_world_arguments(parser, args, args.requests)
    if "selection" not in multi.report:
        parser.error(f"{multi.version} has no leave-one-world-out selection; it is not a multi-world model")
    if "split_days" not in single.report:
        parser.error(f"{single.version} has no split days; it is not a single-world model")
    if multi.dump["feature_names"] != single.dump["feature_names"] or multi.dump["feature_names"] != features.FEATURES:
        parser.error(f"{multi.version} and {single.version} must both use the {len(features.FEATURES)} features of features.py")
    trained = {
        "worlds": [world["world"] for world in multi.report["worlds"]],
        "history starts": multi.report["history_starts"],
        "history window": multi.report["history_window_days"],
        "max trees": multi.report["selection"]["max_trees"],
    }
    given = {
        "worlds": [Path(path).stem for path in args.requests],
        "history starts": starts,
        "history window": args.history_window,
        "max trees": args.max_trees,
    }
    for name, value in trained.items():
        if given[name] != value:
            parser.error(f"{multi.version} was trained with {name} {value}, got {given[name]}")
    return starts


def fill_defaults(parser, args):
    if args.version is None:
        args.version = f"{args.multi}-combined"
    if args.version in (args.multi, args.single):
        parser.error("--version must differ from both parts")


def out_of_world_scores(data, multi, checkpoints):
    """The multi-world model's leave-one-world-out scores of every world's
    held-out rows, recreated from the saved fold models of its chosen
    configuration and checked against its report."""
    selection = multi.report["selection"]
    chosen = selection["chosen"]
    candidate = worlds.Candidate(chosen["name"], chosen["masked_inputs"], chosen["params"])
    folds = saved_folds(data, candidate, selection, checkpoints)
    scores = worlds.out_of_world_scores(folds, data, candidate, chosen["trees"])
    check_held_out_results(data, scores, selection)
    return scores


def saved_folds(data, candidate, selection, checkpoints):
    """Loads every fold model of the candidate. A missing fold or one whose
    fingerprint differs stops the run instead of training it again: a fold
    trained here could differ from the one that chose the configuration."""
    sized = worlds.with_trees(candidate, selection["max_trees"])
    folds = []
    for world, training, _ in worlds.lowo_folds(data):
        path = worlds.fold_checkpoint(checkpoints, candidate, world)
        _, _, key = worlds.training_run(sized, training, selection["seed"])
        model = train.load_checkpoint(path, key)
        if model is None:
            raise SystemExit(
                f"no fold model without {world} with a matching fingerprint at {path}; run train_game.py with the "
                "same worlds and options to train it, then run this again"
            )
        folds.append(worlds.Fold(world, model))
    print(f"loaded {len(folds)} fold models of {candidate.name} from checkpoints, none retrained")
    return folds


def check_held_out_results(data, scores, selection):
    """Stops the run unless the recreated scores give the held-out results the
    multi-world report shows for its chosen configuration."""
    rows = data.loc[scores.index]
    results = worlds.per_world_results(rows, scores.to_numpy(), selection["review_rate"])
    for world, expected in selection["chosen"]["held_out"].items():
        for metric in ("roc_auc", "pr_auc"):
            if abs(results[world][metric] - expected[metric]) > REPORT_TOLERANCE:
                raise SystemExit(f"recreated {metric} of {world} is {results[world][metric]}, the report has {expected[metric]}")


def unseen_by_single(rows, single):
    """Rows the single-world model never trained on: every row of another
    world, and the rows of its own world in its validation and test days."""
    world = Path(single.report["source"]["file"]).stem
    kept_days = np.zeros(len(rows), dtype=bool)
    for name in ("validation", "test"):
        first, last = train_game.days_between(single.report["split_days"][name], None)
        kept_days |= rows["day"].between(first, last).to_numpy()
    return (rows["world"] != world).to_numpy() | kept_days


def reference_rows(data, single):
    """The same number of rows from every world, each purchase once, without
    the single-world model's training rows."""
    rows = worlds.purchase_rows(data)
    return worlds.equal_world_sample(rows[unseen_by_single(rows, single)], train.SEED)


def max_percentile(tables, scores):
    """Highest percentile among the parts, as the service combines them."""
    return np.max([export.percentile(table, score) for table, score in zip(tables, scores)], axis=0)


def build_reference(data, oow_scores, single):
    """Scores the reference rows with each part, builds each part's
    percentile table from its own reference scores and combines them."""
    rows = reference_rows(data, single)
    scores = [oow_scores.loc[rows.index].to_numpy(), export.predict_fast(single.dump, rows[features.FEATURES].to_numpy())]
    tables = [export.percentile_table(score) for score in scores]
    return Reference(rows, scores, tables, max_percentile(tables, scores))


def reference_effect(rows, score, thresholds):
    """The policy effect with the share of legitimate rows flagged, the share
    review_rate sets over the whole reference set."""
    y = rows["is_fraud"].to_numpy()
    return {
        **export.policy_effect(y, score, thresholds, rows["amount"].to_numpy()),
        "legit_flagged": round(float((score[y == 0] >= thresholds["flag_at"]).mean()), 4),
    }


def effect_per_world(rows, score, thresholds):
    effects = {}
    for world in rows["world"].unique():
        chosen = (rows["world"] == world).to_numpy()
        effects[world] = reference_effect(rows[chosen], score[chosen], thresholds)
    return effects


def reference_policy(args, reference):
    """Thresholds chosen on the combined reference scores, with their effect
    on the reference rows overall and per world."""
    rows, combined = reference.rows, reference.combined
    y, amount = rows["is_fraud"].to_numpy(), rows["amount"].to_numpy()
    thresholds = export.choose_thresholds(y, combined, args.review_rate, args.max_missed_fraud, amount)
    # The service refuses to start with such a policy, so the export stops here instead.
    if not 0 <= thresholds["clear_below"] < thresholds["flag_at"] <= 1:
        raise SystemExit(f"thresholds {thresholds} need 0 <= clear_below < flag_at <= 1; lower --max-missed-fraud")
    return {
        **thresholds,
        "player_blend_weight": 0,
        "chosen_on": "combined scores of the reference set (see reference_set): flag_at is the quantile of the "
                     "legitimate rows at 1 - review_rate, clear_below the quantile of the fraud rows at max_missed_fraud",
        "reference_effect": reference_effect(rows, combined, thresholds),
        "reference_effect_per_world": effect_per_world(rows, combined, thresholds),
    }


def part_sources(multi, single):
    """Which rows and scores each part's percentile table is built from."""
    selection = multi.report["selection"]
    chosen = selection["chosen"]
    days = single.report["split_days"]
    world = Path(single.report["source"]["file"]).stem
    return [
        {
            "version": multi.version,
            "table_source": f"leave-one-world-out scores of {chosen['name']} at {chosen['trees']} trees, seed "
                            f"{selection['seed']}: each reference row is scored by the fold model trained without its world",
        },
        {
            "version": single.version,
            "table_source": f"scores of the exported {single.version}: rows of {world} are kept only from its "
                            f"validation ({days['validation']}) and test ({days['test']}) days, because it trained on "
                            f"days {days['train']}; every row of the other worlds is unseen by it",
        },
    ]


def reference_description(reference, starts, window):
    rows = reference.rows
    return {
        "rows": "history start 0 rows only, so each purchase counts once; the single-world model's training rows "
                "are removed; then the same number of rows is drawn from every world (the size of the smallest)",
        "seed": train.SEED,
        "rows_per_world": {world: int(count) for world, count in rows["world"].value_counts(sort=False).items()},
        "fraud_per_world": {world: int(count) for world, count in rows.groupby("world", sort=False)["is_fraud"].sum().items()},
        "days_per_world": {
            world: f"{int(part['day'].min())}-{int(part['day'].max())}" for world, part in rows.groupby("world", sort=False)
        },
        "multi_world_training": {"history_starts": starts, "history_window_days": window},
    }


def final_part_percentiles(reference, parts, flag_at):
    """For each part, per world: percentiles of the legitimate reference rows
    from the scores its table was built from and from the exported model.
    The exported multi-world model trained on these rows, so its numbers are
    in-sample and only show whether it scores higher or lower than its folds."""
    rows = reference.rows
    result = {}
    for part, table, source in zip(parts, reference.tables, reference.scores):
        final = export.predict_fast(part.dump, rows[features.FEATURES].to_numpy())
        sources = {"table_source": export.percentile(table, source), "exported_model": export.percentile(table, final)}
        result[part.version] = legit_percentiles_per_world(rows, sources, flag_at)
    return result


def legit_percentiles_per_world(rows, sources, flag_at):
    """sources maps a name to the percentiles of every row."""
    legit = (rows["is_fraud"] == 0).to_numpy()
    result = {}
    for world in rows["world"].unique():
        chosen = legit & (rows["world"] == world).to_numpy()
        result[world] = {name: percentile_summary(values[chosen], flag_at) for name, values in sources.items()}
    return result


def percentile_summary(values, flag_at):
    return {
        **{f"q{int(q * 100)}": round(float(np.quantile(values, q)), 4) for q in PERCENTILE_QUANTILES},
        "share_at_or_above_flag_at": round(float((values >= flag_at).mean()), 4),
    }


def combined_report(multi, single, reference, policy):
    """The report written next to the combination; the service reads its policy."""
    return {
        "features": features.FEATURES,
        "data": "simulated game traffic from several worlds",
        "combination": f"highest percentile of {multi.version} (multi-world) and {single.version} (single-world)",
        "parts": part_sources(multi, single),
        "policy": policy,
        "caveats": [
            f"the percentile table of {multi.version} comes from one-seed fold models, each trained on one world "
            "fewer, while the exported part is the final model averaged over several seeds and trained on every "
            "world; its scores can run higher or lower, so the review rate on new traffic can differ from review_rate",
            f"the held-out worlds also chose {multi.version}'s configuration and tree count, so its reference scores "
            "are slightly optimistic",
            f"{single.version}'s validation days chose its tree count by early stopping, and its world's reference "
            "rows come only from late days, when players have long histories",
        ],
    }


def development_scorers(args, parts, policy):
    """(version, policy, scoring function) of the combination and of each part
    with the policy of its own report."""
    pairs = [(part.dump, table) for part, table in parts]
    combined = (args.version, policy, lambda frame: export.combined_score(pairs, frame[features.FEATURES].to_numpy()))
    return [combined] + [train_game.exported_model(Path(args.models_dir) / f"{part.version}.json") for part, _ in parts]


def print_summary(report):
    policy = report["policy"]
    print(f"flag_at {policy['flag_at']}, clear_below {policy['clear_below']}")
    print(json.dumps({"reference_set": report["reference_set"]["rows_per_world"],
                      "reference_effect_per_world": policy["reference_effect_per_world"]}, indent=2))
    for dump in report.get("development", []):
        for version, result in dump["models"].items():
            for part in ("all_days", f"first_{train_game.DEVELOPMENT_FIRST_DAYS}_days", "later_days"):
                r = result[part]
                print(f"development {dump['file']} {version} {part}: ROC-AUC {r['roc_auc']}, PR-AUC {r['pr_auc']}, "
                      f"caught {r['fraud_caught']}, precision {r['precision']}, review rate {r['review_rate']}")


def main():
    parser = build_parser()
    args = parser.parse_args()
    fill_defaults(parser, args)
    multi, single = load_part(parser, args.models_dir, args.multi), load_part(parser, args.models_dir, args.single)
    starts = check_options(parser, args, multi, single)

    data, _ = worlds.load_worlds(args.requests, starts, args.history_window)
    oow_scores = out_of_world_scores(data, multi, Path(args.out) / "checkpoints" / args.multi / "lowo")
    reference = build_reference(data, oow_scores, single)
    policy = reference_policy(args, reference)

    report = combined_report(multi, single, reference, policy)
    report["reference_set"] = reference_description(reference, starts, args.history_window)
    report["final_part_percentiles"] = final_part_percentiles(reference, [multi, single], policy["flag_at"])
    parts = list(zip([multi, single], reference.tables))
    # The development dumps are read only after the tables and thresholds are final.
    if args.development:
        report["development"] = train_game.development_report(args.development, development_scorers(args, parts, policy))

    golden = export.synthetic_rows(reference.rows[features.FEATURES], 2000, train.SEED)
    path = export.export_combined(
        args.version, args.models_dir, [(part.version, part.dump, table) for part, table in parts], golden, report
    )
    print_summary(report)
    print(f"combination written to {path}")


if __name__ == "__main__":
    main()
