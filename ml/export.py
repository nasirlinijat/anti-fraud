"""Chooses the model policy thresholds and exports the model for the Go service."""
import json
import math
from pathlib import Path

import numpy as np
import pandas as pd


def choose_thresholds(y_val, score_val, review_rate, max_missed_fraud):
    flag_at = float(np.quantile(score_val[y_val == 0], 1 - review_rate))
    clear_below = float(np.quantile(score_val[y_val == 1], max_missed_fraud))
    return {
        "flag_at": round(flag_at, 6),
        "clear_below": round(min(clear_below, flag_at), 6),
        "review_rate": review_rate,
        "max_missed_fraud": max_missed_fraud,
    }


def policy_effect(y, score, thresholds):
    flagged = score >= thresholds["flag_at"]
    cleared = score < thresholds["clear_below"]
    fraud = y == 1
    legit = ~fraud
    return {
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


def export(model, feature_names, version, out_dir, golden_rows, report):
    out_dir = Path(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    dump = model.booster_.dump_model(num_iteration=model.best_iteration_)
    if dump["feature_names"] != feature_names:
        raise ValueError("model features do not match the feature list")
    for info in dump["tree_info"]:
        check_supported(info["tree_structure"])
    dump["model_version"] = version

    expected = model.predict_proba(golden_rows)[:, 1]
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
    return out_dir / f"{version}.json"
