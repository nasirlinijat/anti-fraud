"""Compares feature groups by training one LightGBM model per feature set.

Usage (from the ml folder):
    python experiment.py data/train_transaction.csv
"""
import argparse
import json
import time
from pathlib import Path

import lightgbm as lgb

import features
from train import evaluate, first_fraud_only, lightgbm_model


def feature_sets():
    sets = {"base": list(features.BASE_FEATURES)}
    for name, group in features.FEATURE_GROUPS.items():
        sets[f"base + {name}"] = features.BASE_FEATURES + group
    sets["all"] = list(features.ALL_FEATURES)
    for name in features.FEATURE_GROUPS:
        sets[f"all - {name}"] = [f for f in features.ALL_FEATURES if f not in features.FEATURE_GROUPS[name]]
    return sets


def run(columns, split, keep):
    """Trains on one feature set. split holds the training, validation and
    test days; keep marks the test purchases counted in the first-fraud result."""
    train, validation, test = split
    model = lightgbm_model()
    model.fit(
        train[columns], train["is_fraud"],
        eval_X=validation[columns], eval_y=validation["is_fraud"],
        eval_metric="average_precision",
        callbacks=[lgb.early_stopping(200, verbose=False)],
    )
    score = model.predict_proba(test[columns])[:, 1]
    y = test["is_fraud"].to_numpy()
    result = evaluate(y, score)
    first = evaluate(y[keep], score[keep])
    return {
        "features": len(columns),
        "trees": int(model.best_iteration_),
        "roc_auc": result["roc_auc"],
        "pr_auc": result["pr_auc"],
        "first_fraud_pr_auc": first["pr_auc"],
        "operating_points": {
            str(point["false_positive_rate"]): {"precision": point["precision"], "caught": point["fraud_caught"]}
            for point in result["operating_points"]
        },
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("csv")
    parser.add_argument("--out", default="output/experiments.json")
    args = parser.parse_args()

    started = time.time()
    data = features.build(features.load(args.csv))
    keep = first_fraud_only(data)
    train, validation, test = features.split(data)
    test_keep = keep.loc[test.index].to_numpy()
    print(f"features built in {time.time() - started:.0f}s")

    results = {}
    print(f"{'feature set':22s}{'n':>4}{'ROC-AUC':>9}{'PR-AUC':>8}{'1st-PR':>8}{'P@0.1%':>8}{'P@0.5%':>8}{'P@1%':>8}{'C@1%':>7}")
    for name, columns in feature_sets().items():
        r = run(columns, (train, validation, test), test_keep)
        results[name] = r
        op = r["operating_points"]
        print(f"{name:22s}{r['features']:>4}{r['roc_auc']:>9.4f}{r['pr_auc']:>8.4f}{r['first_fraud_pr_auc']:>8.4f}"
              f"{op['0.001']['precision']:>8.3f}{op['0.005']['precision']:>8.3f}{op['0.01']['precision']:>8.3f}{op['0.01']['caught']:>7.3f}")

    Path(args.out).parent.mkdir(exist_ok=True)
    Path(args.out).write_text(json.dumps(results, indent=2))
    print(f"done in {time.time() - started:.0f}s")


if __name__ == "__main__":
    main()
