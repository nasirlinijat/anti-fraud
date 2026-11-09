"""Chooses the model policy thresholds and exports the model for the Go service."""
import json
import math
from pathlib import Path

import numpy as np
import pandas as pd


def flag_by_money(y, score, amount, review_cost):
    """Returns the flag threshold that maximizes the fraud money caught minus
    review_cost for every flagged purchase, and that net value. When no
    threshold gains money, the threshold is above every score."""
    order = np.argsort(-score, kind="stable")
    ranked = score[order]
    caught = np.cumsum(np.where(y[order] == 1, amount[order], 0.0))
    net = caught - review_cost * np.arange(1, len(ranked) + 1)
    # A threshold flags every purchase with the same score, so only the last
    # purchase of each score is a possible cut.
    net = np.where(np.r_[ranked[1:] != ranked[:-1], True], net, -np.inf)
    best = int(np.argmax(net))
    if net[best] <= 0:
        return float(np.nextafter(ranked[0], np.inf)), 0.0
    return float(ranked[best]), float(net[best])


def choose_thresholds(y_val, score_val, review_rate, max_missed_fraud, amount_val=None, review_cost=None):
    """Chooses flag_at by review rate (a share of legitimate purchases), or by
    money when review_cost is set; clear_below keeps max_missed_fraud."""
    if review_cost is None:
        flag_at = float(np.quantile(score_val[y_val == 0], 1 - review_rate))
        flag_by = "review_rate"
    else:
        flag_at, _ = flag_by_money(y_val, score_val, amount_val, review_cost)
        flag_by = "money"
    clear_below = float(np.quantile(score_val[y_val == 1], max_missed_fraud))
    return {
        "flag_at": round(flag_at, 6),
        "clear_below": round(min(clear_below, flag_at), 6),
        "flag_by": flag_by,
        "review_rate": review_rate,
        "review_cost": review_cost,
        "max_missed_fraud": max_missed_fraud,
    }


def policy_effect(y, score, thresholds, amount=None):
    flagged = score >= thresholds["flag_at"]
    cleared = score < thresholds["clear_below"]
    fraud = y == 1
    legit = ~fraud
    money = {}
    if amount is not None:
        total = max(float(amount[fraud].sum()), 1e-9)
        money = {
            "flagged_fraud_money_caught": round(float(amount[flagged & fraud].sum()) / total, 4),
            "fraud_money_missed_by_clearing": round(float(amount[cleared & fraud].sum()) / total, 4),
        }
    return {
        **money,
        "flagged_share": round(float(flagged.mean()), 4),
        "flagged_precision": round(float((flagged & fraud).sum() / max(int(flagged.sum()), 1)), 4),
        "flagged_fraud_caught": round(float((flagged & fraud).sum() / max(int(fraud.sum()), 1)), 4),
        "cleared_share": round(float(cleared.mean()), 4),
        "cleared_fraud_rate": round(float((cleared & fraud).sum() / max(int(cleared.sum()), 1)), 4),
        "cleared_legit_share": round(float((cleared & legit).sum() / max(int(legit.sum()), 1)), 4),
        "fraud_missed_by_clearing": round(float((cleared & fraud).sum() / max(int(fraud.sum()), 1)), 4),
    }


def synthetic_rows(reference, count, seed, missing_share=0.1):
    rng = np.random.default_rng(seed)
    rows = {}
    for name in reference.columns:
        values = reference[name].to_numpy()
        values = values[values != -1.0]
        low, high = (np.quantile(values, [0.01, 0.99]) if len(values) else (0.0, 1.0))
        column = rng.uniform(low, high, count)
        if np.all(np.mod(values, 1) == 0):
            column = np.round(column)
        column[rng.random(count) < missing_share] = -1.0
        rows[name] = column
    return pd.DataFrame(rows, columns=reference.columns)


def check_supported(node):
    if "split_feature" not in node:
        return
    if node.get("decision_type") != "<=":
        raise ValueError(f"unsupported decision type {node.get('decision_type')}")
    if node.get("missing_type") not in (None, "None"):
        raise ValueError(f"unsupported missing type {node.get('missing_type')}")
    check_supported(node["left_child"])
    check_supported(node["right_child"])


def tree_value(node, row):
    while "split_feature" in node:
        node = node["left_child"] if row[node["split_feature"]] <= node["threshold"] else node["right_child"]
    return node["leaf_value"]


def predict_from_dump(dump, rows):
    trees = [info["tree_structure"] for info in dump["tree_info"]]
    return np.array([1 / (1 + math.exp(-sum(tree_value(tree, row) for tree in trees))) for row in rows])


def flatten_tree(root):
    """Turns a nested tree into arrays: split feature (-1 for a leaf), threshold
    or leaf value, and the left and right child indexes."""
    feature, value, left, right = [], [], [], []

    def walk(node):
        index = len(feature)
        feature.append(node.get("split_feature", -1))
        value.append(node["threshold"] if "split_feature" in node else node["leaf_value"])
        left.append(-1)
        right.append(-1)
        if "split_feature" in node:
            left[index] = walk(node["left_child"])
            right[index] = walk(node["right_child"])
        return index

    walk(root)
    return np.array(feature), np.array(value, dtype=float), np.array(left), np.array(right)


def predict_fast(dump, rows):
    """Same scores as predict_from_dump, computed for all rows at once."""
    rows = np.asarray(rows, dtype=float)
    total = np.zeros(len(rows))
    for info in dump["tree_info"]:
        feature, value, left, right = flatten_tree(info["tree_structure"])
        node = np.zeros(len(rows), dtype=np.int64)
        active = np.nonzero(feature[node] >= 0)[0]
        while len(active):
            current = node[active]
            go_left = rows[active, feature[current]] <= value[current]
            node[active] = np.where(go_left, left[current], right[current])
            active = active[feature[node[active]] >= 0]
        total += value[node]
    return 1 / (1 + np.exp(-total))


# Points in a percentile table; a score's percentile has a step of 1/10001.
PERCENTILE_POINTS = 10001
# A table value counts as below a score only when it is lower by more than
# this. Python and Go may compute the same score one bit apart, and many
# purchases share exactly the score of a table value.
PERCENTILE_TOLERANCE = 1e-12


def percentile_table(scores, points=PERCENTILE_POINTS):
    """Quantiles of a model's validation scores, used to put scores of
    different models on the same 0-1 scale."""
    return np.quantile(scores, np.linspace(0, 1, points))


def percentile(table, scores):
    """Share of the table below each score by more than PERCENTILE_TOLERANCE.
    A score shared by many purchases at the bottom of the table (a model's
    "nothing suspicious") gets 0, so it never hides the other model's low
    score."""
    return np.searchsorted(table, np.asarray(scores) - PERCENTILE_TOLERANCE, side="left") / len(table)


def combined_score(parts, rows):
    """Highest percentile among the models. parts holds (dump, table) pairs."""
    return np.max([percentile(table, predict_fast(dump, rows)) for dump, table in parts], axis=0)


def export_combined(version, out_dir, parts, golden_rows, report):
    """Writes a combination of exported models: parts holds (model version,
    dump, percentile table). The service scores a purchase with every model and
    keeps the highest percentile."""
    out_dir = Path(out_dir)
    combination = {
        "model_version": version,
        "combination": "max_percentile",
        "feature_names": parts[0][1]["feature_names"],
        "models": [{"file": f"{name}.json", "percentiles": [float(x) for x in table]} for name, _, table in parts],
    }
    if any(dump["feature_names"] != combination["feature_names"] for _, dump, _ in parts):
        raise ValueError("combined models must use the same features")
    expected = combined_score([(dump, table) for _, dump, table in parts], golden_rows.to_numpy())
    report["export"] = {"version": version, "models": [name for name, _, _ in parts], "golden_rows": len(golden_rows)}
    (out_dir / f"{version}.json").write_text(json.dumps(combination))
    golden_rows.assign(expected=expected).to_csv(out_dir / f"{version}.golden.csv", index=False)
    (out_dir / f"{version}.report.json").write_text(json.dumps(report, indent=2))
    return out_dir / f"{version}.json"


def scale_leaves(node, factor):
    if "split_feature" in node:
        scale_leaves(node["left_child"], factor)
        scale_leaves(node["right_child"], factor)
    else:
        node["leaf_value"] *= factor


def average_dump(models):
    """One model dump whose score is the sigmoid of the average raw score of
    the models: every tree of every model, with leaf values divided by the
    number of models. The Go reader needs no change."""
    dumps = [m.booster_.dump_model(num_iteration=m.best_iteration_) for m in models]
    trees = []
    for dump in dumps:
        for info in dump["tree_info"]:
            scale_leaves(info["tree_structure"], 1 / len(models))
            trees.append(info)
    merged = dumps[0]
    merged["tree_info"] = trees
    for index, info in enumerate(merged["tree_info"]):
        info["tree_index"] = index
    return merged


def average_proba(models, rows):
    """Sigmoid of the average raw score, the score of average_dump."""
    raw = np.mean([m.predict(rows, raw_score=True, num_iteration=m.best_iteration_) for m in models], axis=0)
    return 1 / (1 + np.exp(-raw))


def export(model, feature_names, version, out_dir, golden_rows, report):
    """Exports one LightGBM model, or a list of models averaged into one file."""
    out_dir = Path(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    if isinstance(model, list):
        dump = average_dump(model)
        expected = average_proba(model, golden_rows)
    else:
        dump = model.booster_.dump_model(num_iteration=model.best_iteration_)
        expected = model.predict_proba(golden_rows)[:, 1]
    if dump["feature_names"] != feature_names:
        raise ValueError("model features do not match the feature list")
    for info in dump["tree_info"]:
        check_supported(info["tree_structure"])
    dump["model_version"] = version

    reproduced = predict_from_dump(dump, golden_rows.to_numpy())
    max_difference = float(np.max(np.abs(expected - reproduced)))
    if max_difference > 1e-9:
        raise ValueError(f"tree reader differs from LightGBM by {max_difference}")

    report["export"] = {
        "version": version,
        "trees": len(dump["tree_info"]),
        "golden_rows": len(golden_rows),
        "tree_reader_max_difference": max_difference,
    }

    (out_dir / f"{version}.json").write_text(json.dumps(dump))
    golden_rows.assign(expected=expected).to_csv(out_dir / f"{version}.golden.csv", index=False)
    (out_dir / f"{version}.report.json").write_text(json.dumps(report, indent=2))
    return out_dir / f"{version}.json", dump
