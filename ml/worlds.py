"""Trains the game model on several simulated worlds and compares settings by
leaving one world out.

A world is one simulator dump with its own players, cards, devices, field
values and fraud schemes. Features are built per world, because history never
crosses worlds. A model that is only checked inside the world it was trained
on learns that world's levels, such as how long players have been around and
how much they spend. Scoring each world with a model that never saw it
measures what carries over to traffic from an unknown source.
"""
from collections import namedtuple
from pathlib import Path

import numpy as np
import pandas as pd
from sklearn.metrics import average_precision_score, roc_auc_score

import features
import train

# Trees between two points of a held-out world's PR-AUC curve; the tree count
# of a candidate is a multiple of it.
TREE_STEP = 25
DEFAULT_MAX_TREES = 1000
# Days kept after a history start other than 0.
DEFAULT_HISTORY_WINDOW = 14
NANOS_PER_DAY = features.SECONDS_PER_DAY * features.NANOS_PER_SECOND

CODE_INPUTS = ["card_issuer_code", "card_category", "billing_region_code", "billing_country", "card_country"]
ACCOUNT_EVENT_INPUTS = features.FEATURE_GROUPS["account_events"]
# Inputs set to features.MISSING in every training row, so that no tree splits
# on them. The exported model still lists all features.FEATURES in order,
# because the service requires that. Each set adds to the one before: the codes
# carry almost no gain, secs_since_first and the absolute amounts carried most
# of the earlier model's gain and differ most between worlds.
MASK_SETS = {
    "none": [],
    "codes": CODE_INPUTS,
    "codes+history_age": CODE_INPUTS + ["secs_since_first"],
    "codes+history_age+amount_levels": CODE_INPUTS + ["secs_since_first", "amount", "player_max_amount", "player_avg_amount"],
}
# Input groups that the selected candidate is checked without, one at a time.
CHECKED_INPUT_GROUPS = {
    "account_events": ACCOUNT_EVENT_INPUTS,
    "signals": features.FEATURE_GROUPS["signals"],
}
# Changes to train.lightgbm_model(); "v5" keeps its settings.
REGULARIZATION = {
    "v5": {},
    "moderate": {"num_leaves": 15, "min_child_samples": 400, "colsample_bytree": 0.5},
    "strong": {"num_leaves": 7, "min_child_samples": 1500, "colsample_bytree": 0.5},
}

# One configuration of the grid: inputs masked in training and LightGBM settings.
Candidate = namedtuple("Candidate", ["name", "masked", "params"])
# One fold of leave one world out: the world left out and the model trained on the others.
Fold = namedtuple("Fold", ["world", "model"])
# The largest tree count tried, the review rate of the per-world results (a
# share of legitimate purchases) and the folder for finished models.
Selection = namedtuple("Selection", ["max_trees", "review_rate", "checkpoints"])
# The check of one input group: the candidate without it, the worlds whose
# purchases carry the group's inputs, and the mean held-out PR-AUC of those
# worlds with and without the group.
GroupCheck = namedtuple("GroupCheck", ["group", "result", "worlds", "with_group", "without_group"])
# A candidate's result: the tree count with the best worst-world PR-AUC, the
# per-world results at that count, the fold models and the out-of-world scores.
CandidateResult = namedtuple("CandidateResult", ["candidate", "trees", "worst_pr_auc", "held_out", "folds", "scores"])


def candidate_grid():
    """Every mask set with every regularization setting. The first candidate
    is the earlier model's configuration."""
    return [
        Candidate(f"{mask}/{setting}", MASK_SETS[mask], params)
        for mask in MASK_SETS
        for setting, params in REGULARIZATION.items()
    ]


def with_trees(candidate, trees):
    return candidate._replace(params={**candidate.params, "n_estimators": trees})


def add_world_arguments(parser):
    parser.add_argument(
        "--history-starts", default="0",
        help="comma-separated days, including 0 (the world as it is); for each later day S, the purchases before S "
             "are dropped before features are built, so the same players start again without history",
    )
    parser.add_argument("--history-window", type=int, default=DEFAULT_HISTORY_WINDOW,
                        help="days kept after each history start other than 0")
    parser.add_argument("--max-trees", type=int, default=DEFAULT_MAX_TREES,
                        help=f"largest tree count tried when leaving one world out, a multiple of {TREE_STEP}")


def check_world_arguments(parser, args, paths):
    """Stops the run on settings the world training cannot use and returns the
    history start days."""
    if len(paths) < 2:
        parser.error("leaving one world out needs at least two worlds")
    if len({Path(path).stem for path in paths}) < len(paths):
        parser.error("every world needs its own file name, which names the world in the report")
    if args.max_trees < TREE_STEP or args.max_trees % TREE_STEP:
        parser.error(f"--max-trees must be a multiple of {TREE_STEP}")
    if args.history_window < 1:
        parser.error("--history-window must be at least 1 day")
    starts = sorted({int(part) for part in args.history_starts.split(",")})
    if 0 not in starts or starts[0] < 0:
        parser.error("--history-starts must include 0 and no negative day")
    return starts


def load_worlds(paths, starts, window, account_events_masked_copy=True):
    """Builds the features of every world and its history variants. Returns
    one frame with a world, a history_start and an account_events_masked
    column, and a description of each world. With account_events_masked_copy,
    a world with account events also gets a copy without them (see
    without_account_events)."""
    frames, infos = [], []
    for path in paths:
        frame, info = load_world(path, starts, window, account_events_masked_copy)
        frames.append(frame)
        infos.append(info)
        print(f"world {info['world']}: {info['purchases']} purchases, {len(frame)} rows with variants")
    return pd.concat(frames, ignore_index=True), infos


def load_world(path, starts, window, account_events_masked_copy):
    raw, events = features.load_requests(path), features.load_account_events(path)
    last_day = int(raw["day"].max())
    # A start is used only when its whole window fits in the world.
    used = [start for start in starts if start == 0 or start + window - 1 <= last_day]
    parts = [history_variant(raw, start, window, events) for start in used]
    if account_events_masked_copy and len(events):
        parts.append(without_account_events(parts[0]))
    frame = pd.concat(parts, ignore_index=True)
    frame["world"] = Path(path).stem
    skipped = [start for start in starts if start not in used]
    return frame, world_info(path, raw, events, parts, skipped)


def history_variant(raw, start, window, events=None):
    """The world's features as if the service had started on day start: the
    purchases and account events before it are dropped before features are
    built. Start 0 keeps every day; a later start keeps window days."""
    kept = raw[raw["day"] >= start].reset_index(drop=True)
    data = features.build(kept.copy(), events_since(events, raw, start))
    data["pattern"] = kept["pattern"].to_numpy()
    data["history_start"] = start
    data["account_events_masked"] = False
    if start == 0:
        return data
    return data[data["day"] < start + window].reset_index(drop=True)


def events_since(events, raw, start):
    """The account events from midnight UTC of day start of the world on."""
    if events is None:
        return None
    first_midnight = raw["event_ns"].min() // NANOS_PER_DAY * NANOS_PER_DAY
    return events[events["event_ns"] >= first_midnight + start * NANOS_PER_DAY]


def without_account_events(rows):
    """The world as a service without account events would see it: the
    account event inputs of every purchase are missing.

    Only the simulator worlds have account events. Without this copy, missing
    account inputs would mostly mean "another world", and a tree could split
    on them to learn that world's fraud rate instead of fraud. The copy puts
    the same purchases on both sides of the split, so the account inputs can
    only add what the events say, and the model still learns the simulator's
    scenarios for traffic without account events. The copy shares its world's
    weight and is never scored."""
    copy = rows.copy()
    copy[ACCOUNT_EVENT_INPUTS] = features.MISSING
    copy["account_events_masked"] = True
    return copy


def world_info(path, raw, events, parts, skipped):
    return {
        "file": Path(path).name,
        "world": Path(path).stem,
        "purchases": int(len(raw)),
        "account_events": int(len(events)),
        "days": f"{int(raw['day'].min())}-{int(raw['day'].max())}",
        "fraud_rate": round(float(raw["isFraud"].mean()), 4),
        "history_variants": [
            {
                "history_start": int(part["history_start"].iloc[0]),
                "account_events_masked": bool(part["account_events_masked"].iloc[0]),
                "days": f"{int(part['day'].min())}-{int(part['day'].max())}",
                "rows": int(len(part)),
                "fraud_rate": round(float(part["is_fraud"].mean()), 4),
            }
            for part in parts
        ],
        "skipped_history_starts": skipped,
    }


def world_weights(worlds):
    """Row weights that give every world the same total weight, whatever its
    size. The weights average 1."""
    labels = pd.Series(np.asarray(worlds))
    rows_in_world = labels.map(labels.value_counts()).to_numpy(dtype=float)
    return len(labels) / (labels.nunique() * rows_in_world)


def masked_inputs(rows, masked):
    x = rows[features.FEATURES].copy()
    if masked:
        x[list(masked)] = features.MISSING
    return x


def fit_model(candidate, rows, seed, checkpoint=None):
    """Trains one model on rows with every world weighted equally, for the
    number of trees in candidate.params; nothing is held back for early
    stopping. A finished model is saved at checkpoint and loaded by a later
    run with the same seed, settings and data."""
    model, (x, y, weight), key = training_run(candidate, rows, seed)
    saved = train.load_checkpoint(checkpoint, key)
    if saved is not None:
        return saved
    model.fit(x, y, sample_weight=weight)
    train.save_checkpoint(checkpoint, key, model)
    return model


def training_run(candidate, rows, seed):
    """The unfitted model, its training inputs (x, y, weight) and the
    fingerprint that names the run in a checkpoint."""
    model = train.lightgbm_model()
    model.set_params(**candidate.params, random_state=seed)
    x, y, weight = masked_inputs(rows, candidate.masked), rows["is_fraud"].to_numpy(), world_weights(rows["world"])
    return model, (x, y, weight), train.fingerprint(seed, model.get_params(), x, y, weight)


def fold_checkpoint(checkpoints, candidate, world):
    """The file that holds the candidate's model trained without world."""
    return Path(checkpoints) / f"{candidate.name.replace('/', '-')}-without-{world}.joblib"


def equal_world_sample(rows, seed):
    """The same number of rows from every world, drawn without replacement:
    as many as the smallest world has. Thresholds chosen on it fit every world
    equally, where pooled rows would mostly fit the largest world."""
    size = int(rows["world"].value_counts().min())
    return rows.groupby("world", sort=False).sample(n=size, random_state=seed)


def purchase_rows(data):
    """Each purchase once, with the history and account events its world
    gives it. The history variants and the copies without account events are
    only trained on."""
    return data[(data["history_start"] == 0) & ~data["account_events_masked"]]


def held_out_rows(data, world):
    """The rows a world is scored on."""
    rows = purchase_rows(data)
    return rows[rows["world"] == world]


def lowo_folds(data):
    """For each world: the world, the rows of every other world with their
    variants, and the world's held-out rows. A variant always stays on its
    world's side."""
    for world in data["world"].unique():
        yield world, data[data["world"] != world], held_out_rows(data, world)


def held_out_curve(model, x, y):
    """PR-AUC on x after every TREE_STEP trees. Raw scores are summed tree
    range by tree range, which gives the full model's raw score at the end."""
    booster = model.booster_
    raw = np.zeros(len(x))
    curve = []
    for start in range(0, booster.current_iteration(), TREE_STEP):
        raw += booster.predict(x, start_iteration=start, num_iteration=TREE_STEP, raw_score=True)
        curve.append(average_precision_score(y, raw))
    return np.array(curve)


def best_tree_count(curves):
    """The tree count whose worst held-out PR-AUC is highest; the first such
    count on a tie."""
    worst = np.min(np.vstack(list(curves.values())), axis=0)
    return (int(np.argmax(worst)) + 1) * TREE_STEP


def evaluate_candidate(candidate, data, selection):
    """Leaves each world out in turn, chooses the tree count on the held-out
    worlds and scores every held-out row with the model that never saw its
    world. The result is optimistic for the held-out worlds, because they
    choose the tree count."""
    folds, curves = [], {}
    for world, training, held_out in lowo_folds(data):
        checkpoint = fold_checkpoint(selection.checkpoints, candidate, world)
        model = fit_model(with_trees(candidate, selection.max_trees), training, train.SEED, checkpoint)
        folds.append(Fold(world, model))
        curves[world] = held_out_curve(model, masked_inputs(held_out, candidate.masked), held_out["is_fraud"].to_numpy())
    trees = best_tree_count(curves)
    scores = out_of_world_scores(folds, data, candidate, trees)
    held_out = per_world_results(data.loc[scores.index], scores.to_numpy(), selection.review_rate)
    worst = min(result["pr_auc"] for result in held_out.values())
    return CandidateResult(candidate, trees, worst, held_out, folds, scores)


def out_of_world_scores(folds, data, candidate, trees):
    """Every world's held-out rows scored by the fold model that was trained
    without that world, indexed like data."""
    parts = []
    for fold in folds:
        rows = held_out_rows(data, fold.world)
        score = fold.model.predict_proba(masked_inputs(rows, candidate.masked), num_iteration=trees)[:, 1]
        parts.append(pd.Series(score, index=rows.index))
    return pd.concat(parts).sort_index()


def per_world_results(rows, score, review_rate):
    results = {}
    for world in rows["world"].unique():
        chosen = (rows["world"] == world).to_numpy()
        results[world] = world_result(rows["is_fraud"].to_numpy()[chosen], score[chosen], review_rate)
    return results


def world_result(y, score, review_rate):
    """Ranking quality and the fraud caught when review_rate of the world's
    legitimate purchases are flagged."""
    point = train.at_false_positive_rate(y, score, review_rate)
    return {
        "purchases": int(len(y)),
        "fraud_rate": round(float(y.mean()), 4),
        "roc_auc": round(float(roc_auc_score(y, score)), 4),
        "pr_auc": round(float(average_precision_score(y, score)), 4),
        "fraud_caught_at_review_rate": point["fraud_caught"],
        "precision_at_review_rate": point["precision"],
    }


def select_candidate(data, candidates, selection):
    """Evaluates every candidate and returns the results with the winner: the
    highest worst-world PR-AUC, the earlier candidate on a tie."""
    results = []
    for candidate in candidates:
        result = evaluate_candidate(candidate, data, selection)
        results.append(result)
        print(f"{candidate.name}: {result.trees} trees, worst held-out PR-AUC {result.worst_pr_auc:.4f}")
    return results, max(results, key=lambda result: result.worst_pr_auc)


def check_input_groups(data, winner, selection):
    """Evaluates the selected candidate without each input group in
    CHECKED_INPUT_GROUPS that some world carries. A group is dropped when the
    worlds whose purchases carry its inputs score a higher mean held-out
    PR-AUC without it; the other worlds see the inputs missing either way, so
    they cannot judge the group. Returns the checks and the chosen result: the
    winner, or the winner without every dropped group."""
    checks = []
    for group, names in CHECKED_INPUT_GROUPS.items():
        carrying = worlds_with_inputs(data, names)
        if carrying:
            checks.append(group_check(data, winner, group, carrying, selection))
    dropped = [check.group for check in checks if check.without_group > check.with_group]
    if not dropped:
        return checks, winner
    if len(dropped) == 1:
        return checks, next(check.result for check in checks if check.group == dropped[0])
    return checks, evaluate_candidate(without_groups(winner.candidate, dropped), data, selection)


def group_check(data, winner, group, carrying, selection):
    result = evaluate_candidate(without_groups(winner.candidate, [group]), data, selection)
    print(f"{result.candidate.name}: {result.trees} trees, worst held-out PR-AUC {result.worst_pr_auc:.4f}")

    def mean_pr_auc(candidate_result):
        return round(float(np.mean([candidate_result.held_out[world]["pr_auc"] for world in carrying])), 4)

    return GroupCheck(group, result, carrying, mean_pr_auc(winner), mean_pr_auc(result))


def without_groups(candidate, groups):
    """The candidate with the inputs of the groups masked as well."""
    masked = list(candidate.masked)
    for group in groups:
        masked += [name for name in CHECKED_INPUT_GROUPS[group] if name not in masked]
    return Candidate(f"{candidate.name}-" + "-".join(groups), masked, candidate.params)


def worlds_with_inputs(data, names):
    """The worlds where some purchase has one of the inputs."""
    rows = purchase_rows(data)
    carrying = (rows[names] != features.MISSING).any(axis=1)
    return list(rows.loc[carrying, "world"].unique())
