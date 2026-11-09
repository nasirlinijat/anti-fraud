"""Trains a logistic regression baseline and a LightGBM model on IEEE-CIS.

Usage (from the ml folder):
    python train.py data/train_transaction.csv
"""
import argparse
import json
import time
from pathlib import Path

import lightgbm as lgb
import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import average_precision_score, roc_auc_score
from sklearn.pipeline import make_pipeline
from sklearn.preprocessing import FunctionTransformer, StandardScaler

import export
import features

SEED = 1234
FALSE_POSITIVE_RATES = [0.001, 0.005, 0.01, 0.02]


def signed_log(x):
    x = np.asarray(x, dtype=float)
    return np.sign(x) * np.log1p(np.abs(x))


def baseline_model():
    return make_pipeline(
        FunctionTransformer(signed_log),
        StandardScaler(),
        LogisticRegression(max_iter=2000, random_state=SEED),
    )


def lightgbm_model():
    return lgb.LGBMClassifier(
        n_estimators=3000,
        learning_rate=0.03,
        num_leaves=31,
        min_child_samples=100,
        subsample=0.8,
        subsample_freq=1,
        colsample_bytree=0.8,
        reg_lambda=1.0,
        use_missing=False,
        random_state=SEED,
        n_jobs=-1,
        verbose=-1,
    )


def at_false_positive_rate(y, score, rate, amount=None):
    threshold = np.quantile(score[y == 0], 1 - rate)
    flagged = score > threshold
    true_positives = int((flagged & (y == 1)).sum())
    flagged_count = int(flagged.sum())
    point = {
        "false_positive_rate": rate,
        "threshold": round(float(threshold), 6),
        "fraud_caught": round(true_positives / max(int((y == 1).sum()), 1), 4),
        "precision": round(true_positives / max(flagged_count, 1), 4),
        "flagged": flagged_count,
    }
    if amount is not None:
        fraud = y == 1
        total = max(float(amount[fraud].sum()), 1e-9)
        point["fraud_money_caught"] = round(float(amount[flagged & fraud].sum()) / total, 4)
    return point


def evaluate(y, score, amount=None):
    return {
        "rows": int(len(y)),
        "fraud_rate": round(float(y.mean()), 4),
        "roc_auc": round(float(roc_auc_score(y, score)), 4),
        "pr_auc": round(float(average_precision_score(y, score)), 4),
        "operating_points": [at_false_positive_rate(y, score, rate, amount) for rate in FALSE_POSITIVE_RATES],
    }


def first_fraud_only(data):
    earlier_fraud = data.groupby("player_id", sort=False)["is_fraud"].cumsum() - data["is_fraud"]
    return ~((data["is_fraud"] == 1) & (earlier_fraud > 0))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("csv")
    parser.add_argument("--out", default="output")
    parser.add_argument("--version", default="lgbm-ieee-v4")
    parser.add_argument("--models-dir", default="../backend/models")
    parser.add_argument("--review-rate", type=float, default=0.005)
    parser.add_argument("--max-missed-fraud", type=float, default=0.02)
    parser.add_argument(
        "--review-cost", type=float,
        help="cost of reviewing one purchase, in the purchase currency; when set, "
             "flag_at maximizes fraud money caught minus review cost instead of using --review-rate",
    )
    args = parser.parse_args()
    out_dir = Path(args.out)
    out_dir.mkdir(exist_ok=True)

    started = time.time()
    data = features.build(features.load(args.csv))
    keep = first_fraud_only(data)
    train, validation, test = features.split(data)
    print(f"features built in {time.time() - started:.0f}s: train {len(train)}, validation {len(validation)}, test {len(test)}")

    x_train, y_train = train[features.FEATURES], train["is_fraud"].to_numpy()
    x_val, y_val = validation[features.FEATURES], validation["is_fraud"].to_numpy()
    x_test, y_test = test[features.FEATURES], test["is_fraud"].to_numpy()
    test_keep = keep.loc[test.index].to_numpy()
    val_amount, test_amount = validation["amount"].to_numpy(), test["amount"].to_numpy()

    report = {"features": features.FEATURES, "split_days": {"history_only": f"1-{features.WARM_UP_DAYS}", "train": f"{features.WARM_UP_DAYS + 1}-120", "validation": "136-152", "test": "153-183"}}

    baseline = baseline_model().fit(x_train, y_train)
    baseline_test = baseline.predict_proba(x_test)[:, 1]
    report["logistic_regression"] = {
        "validation": evaluate(y_val, baseline.predict_proba(x_val)[:, 1], val_amount),
        "test": evaluate(y_test, baseline_test, test_amount),
        "test_first_fraud_only": evaluate(y_test[test_keep], baseline_test[test_keep], test_amount[test_keep]),
    }

    model = lightgbm_model()
    model.fit(
        x_train, y_train,
        eval_X=x_val,
        eval_y=y_val,
        eval_metric="average_precision",
        callbacks=[lgb.early_stopping(200, verbose=False)],
    )
    model_test = model.predict_proba(x_test)[:, 1]
    importance = model.booster_.feature_importance(importance_type="gain")
    report["lightgbm"] = {
        "best_iteration": int(model.best_iteration_),
        "validation": evaluate(y_val, model.predict_proba(x_val)[:, 1], val_amount),
        "test": evaluate(y_test, model_test, test_amount),
        "test_first_fraud_only": evaluate(y_test[test_keep], model_test[test_keep], test_amount[test_keep]),
        "feature_importance_gain": {
            name: round(float(value), 1)
            for name, value in sorted(zip(features.FEATURES, importance), key=lambda item: -item[1])
        },
    }

    model_val = model.predict_proba(x_val)[:, 1]
    thresholds = export.choose_thresholds(
        y_val, model_val, args.review_rate, args.max_missed_fraud, val_amount, args.review_cost
    )
    report["policy"] = {
        **thresholds,
        "validation_effect": export.policy_effect(y_val, model_val, thresholds, val_amount),
        "test_effect": export.policy_effect(y_test, model_test, thresholds, test_amount),
    }

    (out_dir / "training_report.json").write_text(json.dumps(report, indent=2))
    golden = export.synthetic_rows(x_train, 2000, SEED)
    model_path = export.export(model, features.FEATURES, args.version, args.models_dir, golden, report)

    print(json.dumps({name: report[name]["test"] for name in ("logistic_regression", "lightgbm")}, indent=2))
    print(json.dumps({"policy": report["policy"], "export": report["export"]}, indent=2))
    print(f"model written to {model_path}")
    print(f"done in {time.time() - started:.0f}s")


if __name__ == "__main__":
    main()
