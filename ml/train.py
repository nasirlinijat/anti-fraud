"""Trains a logistic regression baseline and a LightGBM model on IEEE-CIS.

Usage (from the ml folder):
    python train.py data/train_transaction.csv
"""
import argparse
import hashlib
import json
import os
import time
from pathlib import Path

import joblib
import lightgbm as lgb
import numpy as np
import pandas as pd
from sklearn.compose import ColumnTransformer
from sklearn.impute import SimpleImputer
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import average_precision_score, roc_auc_score
from sklearn.pipeline import make_pipeline
from sklearn.preprocessing import FunctionTransformer, OneHotEncoder, StandardScaler

import export
import features

SEED = 1234
AVERAGE_SEEDS = [SEED, 7, 99, 2024, 31337]
FALSE_POSITIVE_RATES = [0.001, 0.005, 0.01, 0.02]
# Features whose values are codes, not amounts or counts. The baseline encodes
# each code as its own category; codes seen fewer than BASELINE_MIN_CODE_COUNT
# times in training share one category.
CODE_FEATURES = [
    "product_code", "card_network", "card_type", "card_country", "card_category", "billing_country",
    "device_type", "os_family", "browser_family", "card_issuer_code", "billing_region_code",
]
BASELINE_MIN_CODE_COUNT = 100


def signed_log(x):
    x = np.asarray(x, dtype=float)
    return np.sign(x) * np.log1p(np.abs(x))


def baseline_model(feature_names=None):
    """Logistic regression with one-hot encoded codes. A missing number
    (features.MISSING) is replaced by the training median and marked by its own
    indicator column; a missing code is its own category."""
    names = features.FEATURES if feature_names is None else feature_names
    codes = [name for name in names if name in CODE_FEATURES]
    numbers = [name for name in names if name not in CODE_FEATURES]
    inputs = ColumnTransformer([
        ("numbers", make_pipeline(
            SimpleImputer(missing_values=features.MISSING, strategy="median", add_indicator=True),
            FunctionTransformer(signed_log),
            StandardScaler(),
        ), numbers),
        ("codes", OneHotEncoder(handle_unknown="infrequent_if_exist", min_frequency=BASELINE_MIN_CODE_COUNT), codes),
    ])
    return make_pipeline(inputs, LogisticRegression(max_iter=2000, random_state=SEED))


def all_numeric_baseline_model():
    """The earlier baseline, which reads codes and missing values as numbers.
    It stays in the report for comparison."""
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


def fingerprint(seed, params, *arrays):
    """Identifies one training run: the seed, the settings and the data."""
    digest = hashlib.sha256(json.dumps({"seed": seed, "params": params}, sort_keys=True, default=str).encode())
    for values in arrays:
        digest.update(np.ascontiguousarray(np.asarray(values, dtype=float)).tobytes())
    return digest.hexdigest()


def fit_lightgbm(seed, x_train, y_train, x_val, y_val, checkpoint=None):
    """Trains one LightGBM model with early stopping on validation.

    With checkpoint (a file path), the finished model is saved there. A later
    run with the same seed, settings and data loads it instead of training
    again, so a run that was interrupted continues after its finished models.
    """
    model = lightgbm_model()
    model.set_params(random_state=seed)
    key = fingerprint(seed, model.get_params(), x_train, y_train, x_val, y_val)
    if checkpoint is not None and Path(checkpoint).exists():
        saved = joblib.load(checkpoint)
        if saved["fingerprint"] == key:
            print(f"loaded the finished model from {checkpoint}")
            return saved["model"]
    model.fit(
        x_train, y_train,
        eval_X=x_val,
        eval_y=y_val,
        eval_metric="average_precision",
        callbacks=[lgb.early_stopping(200, verbose=False)],
    )
    if checkpoint is not None:
        checkpoint = Path(checkpoint)
        checkpoint.parent.mkdir(parents=True, exist_ok=True)
        # Written to a second file first, so a crash never leaves half a checkpoint.
        unfinished = checkpoint.with_name(checkpoint.name + ".tmp")
        joblib.dump({"fingerprint": key, "model": model}, unfinished)
        os.replace(unfinished, checkpoint)
    return model


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


def first_purchase_recall(y, score, first_purchase, rate=0.01):
    """Fraud caught on a player's first purchase at the threshold that flags
    rate of all legitimate purchases."""
    threshold = np.quantile(score[y == 0], 1 - rate)
    fraud = (y == 1) & first_purchase
    return round(float((score[fraud] > threshold).mean()), 4) if fraud.any() else None


def first_fraud_only(data):
    earlier_fraud = data.groupby("player_id", sort=False)["is_fraud"].cumsum() - data["is_fraud"]
    return ~((data["is_fraud"] == 1) & (earlier_fraud > 0))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("csv", nargs="?", help="IEEE-CIS train_transaction.csv")
    parser.add_argument("--out", default="output")
    parser.add_argument("--version", default="lgbm-ieee-v5")
    parser.add_argument("--models-dir", default="../backend/models")
    parser.add_argument("--review-rate", type=float, default=0.005)
    parser.add_argument("--max-missed-fraud", type=float, default=0.02)
    parser.add_argument("--models", type=int, default=5, help="number of models averaged into the main model (1 to 5)")
    parser.add_argument("--player-blend", type=float, default=0.5,
                        help="weight of the player's earlier average score in the final score (0 turns blending off)")
    parser.add_argument("--inputs", help="train from model inputs exported by the service instead of the IEEE-CIS CSV")
    parser.add_argument("--labels", help="CSV with transaction_id and is_fraud, needed with --inputs")
    parser.add_argument("--day-zero", default="2017-12-01T00:00:00Z",
                        help="with --inputs, the time of day 0 (the replay's start by default)")
    parser.add_argument("--train-days", default=None, help="first-last day used for training, for example 31-120")
    parser.add_argument("--validation-days", default=None, help="first-last validation day")
    parser.add_argument("--test-days", default=None, help="first-last test day")
    parser.add_argument(
        "--review-cost", type=float,
        help="cost of reviewing one purchase, in the purchase currency; when set, "
             "flag_at maximizes fraud money caught minus review cost instead of using --review-rate",
    )
    parser.add_argument(
        "--target-precision", type=float,
        help="share of flagged purchases that must be fraud on validation; when set, flag_at is the "
             "lowest threshold that reaches it instead of using --review-rate or --review-cost",
    )
    args = parser.parse_args()
    out_dir = Path(args.out)
    out_dir.mkdir(exist_ok=True)

    days = dict(features.SPLIT_DAYS)
    for name, value in [("train", args.train_days), ("validation", args.validation_days), ("test", args.test_days)]:
        if value:
            first, last = (int(part) for part in value.split("-"))
            days[name] = (first, last)

    started = time.time()
    if args.inputs:
        if not args.labels:
            parser.error("--inputs needs --labels")
        data, unlabelled = features.load_export(args.inputs, args.labels, args.day_zero)
        print(f"read {len(data)} exported purchases with labels; {unlabelled} without a label left out")
    else:
        data = features.build(features.load(args.csv))
    keep = first_fraud_only(data)
    train, validation, test = features.split(data, days)
    print(f"features built in {time.time() - started:.0f}s: train {len(train)}, validation {len(validation)}, test {len(test)}")

    x_train, y_train = train[features.FEATURES], train["is_fraud"].to_numpy()
    x_val, y_val = validation[features.FEATURES], validation["is_fraud"].to_numpy()
    x_test, y_test = test[features.FEATURES], test["is_fraud"].to_numpy()
    test_keep = keep.loc[test.index].to_numpy()
    val_amount, test_amount = validation["amount"].to_numpy(), test["amount"].to_numpy()

    report = {
        "features": features.FEATURES,
        "data": "service export" if args.inputs else "IEEE-CIS",
        "split_days": {name: f"{first}-{last}" for name, (first, last) in days.items()},
    }

    for name, make in [("logistic_regression", baseline_model), ("logistic_regression_all_numeric", all_numeric_baseline_model)]:
        baseline = make().fit(x_train, y_train)
        baseline_test = baseline.predict_proba(x_test)[:, 1]
        report[name] = {
            "validation": evaluate(y_val, baseline.predict_proba(x_val)[:, 1], val_amount),
            "test": evaluate(y_test, baseline_test, test_amount),
            "test_first_fraud_only": evaluate(y_test[test_keep], baseline_test[test_keep], test_amount[test_keep]),
        }

    # The main model is the average of several models trained with different
    # random seeds; averaging smooths out each model's chance splits.
    # Every finished model is saved, so an interrupted run continues after it.
    checkpoints = out_dir / "checkpoints" / args.version
    models = [
        fit_lightgbm(seed, x_train, y_train, x_val, y_val, checkpoints / f"main-seed{seed}.joblib")
        for seed in AVERAGE_SEEDS[: args.models]
    ]
    single_test = models[0].predict_proba(x_test)[:, 1]
    model_val = export.average_proba(models, x_val)
    model_test = export.average_proba(models, x_test)
    importance = np.mean([m.booster_.feature_importance(importance_type="gain") for m in models], axis=0)
    report["lightgbm"] = {
        "models": len(models),
        "best_iterations": [int(m.best_iteration_) for m in models],
        "single_model_test": evaluate(y_test, single_test, test_amount),
        "validation": evaluate(y_val, model_val, val_amount),
        "test": evaluate(y_test, model_test, test_amount),
        "test_first_fraud_only": evaluate(y_test[test_keep], model_test[test_keep], test_amount[test_keep]),
        "feature_importance_gain": {
            name: round(float(value), 1)
            for name, value in sorted(zip(features.FEATURES, importance), key=lambda item: -item[1])
        },
    }

    thresholds = export.choose_thresholds(
        y_val, model_val, args.review_rate, args.max_missed_fraud, val_amount, args.review_cost, args.target_precision
    )
    report["policy"] = {
        **thresholds,
        "validation_effect": export.policy_effect(y_val, model_val, thresholds, val_amount),
        "test_effect": export.policy_effect(y_test, model_test, thresholds, test_amount),
    }

    (out_dir / "training_report.json").write_text(json.dumps(report, indent=2))
    golden = export.synthetic_rows(x_train, 2000, SEED)
    model_path, model_dump = export.export(models, features.FEATURES, args.version, args.models_dir, golden, report)

    # A second model learns only each customer's first fraud: IEEE marks a
    # customer's later purchases as fraud after a chargeback, and those are
    # easy to recognize but not new fraud.
    keep_train = keep.loc[train.index].to_numpy()
    keep_val = keep.loc[validation.index].to_numpy()
    first = fit_lightgbm(
        SEED, x_train[keep_train], y_train[keep_train], x_val[keep_val], y_val[keep_val], checkpoints / "first.joblib"
    )
    first_val = first.predict_proba(x_val)[:, 1]
    first_test = first.predict_proba(x_test)[:, 1]
    first_report = {
        "features": features.FEATURES,
        "labels": "first fraud of each customer only",
        "lightgbm": {
            "best_iteration": int(first.best_iteration_),
            "test": evaluate(y_test, first_test, test_amount),
            "test_first_fraud_only": evaluate(y_test[test_keep], first_test[test_keep], test_amount[test_keep]),
        },
    }
    first_version = f"{args.version}-first"
    _, first_dump = export.export(first, features.FEATURES, first_version, args.models_dir, golden, first_report)

    tables = [export.percentile_table(model_val), export.percentile_table(first_val)]
    combined_val = np.maximum(export.percentile(tables[0], model_val), export.percentile(tables[1], first_val))
    combined_test = np.maximum(export.percentile(tables[0], model_test), export.percentile(tables[1], first_test))
    # Blend the main model's score with the average of the player's earlier
    # main-model scores, then combine. Blending the combined score instead
    # lowered precision in tests. Only purchases after the training days have
    # honest scores, so the history of earlier scores starts there, as it
    # would in a fresh service.
    unblended_test = combined_test
    window = data[data["day"] > days["train"][1]]
    window_main = export.average_proba(models, window[features.FEATURES])
    blended_main = pd.Series(
        export.blend_with_history(window_main, window["player_id"].to_numpy(), args.player_blend), index=window.index
    )
    main_val, main_test = blended_main.loc[validation.index].to_numpy(), blended_main.loc[test.index].to_numpy()
    tables = [export.percentile_table(main_val), export.percentile_table(first_val)]
    combined_val = np.maximum(export.percentile(tables[0], main_val), export.percentile(tables[1], first_val))
    combined_test = np.maximum(export.percentile(tables[0], main_test), export.percentile(tables[1], first_test))
    combined_thresholds = export.choose_thresholds(
        y_val, combined_val, args.review_rate, args.max_missed_fraud, val_amount, args.review_cost, args.target_precision
    )
    combined_thresholds["player_blend_weight"] = args.player_blend
    first_purchase = test["player_txn_count"].to_numpy() == 0
    combined_report = {
        "features": features.FEATURES,
        "combination": "highest percentile of the main and first-fraud models",
        "comparison_at_1pct": {
            name: {
                **{k: v for k, v in at_false_positive_rate(y_test, score, 0.01, test_amount).items() if k != "threshold"},
                "first_purchase_fraud_caught": first_purchase_recall(y_test, score, first_purchase),
            }
            for name, score in [("main", model_test), ("combined", unblended_test), ("combined_blended", combined_test)]
        },
        "test_unblended": evaluate(y_test, unblended_test, test_amount),
        "test_first_fraud_only_unblended": evaluate(y_test[test_keep], unblended_test[test_keep], test_amount[test_keep]),
        "test": evaluate(y_test, combined_test, test_amount),
        "test_first_fraud_only": evaluate(y_test[test_keep], combined_test[test_keep], test_amount[test_keep]),
        "precision_targets": export.precision_targets(y_val, combined_val, y_test, combined_test, test_amount),
        "policy": {
            **combined_thresholds,
            "validation_effect": export.policy_effect(y_val, combined_val, combined_thresholds, val_amount),
            "test_effect": export.policy_effect(y_test, combined_test, combined_thresholds, test_amount),
        },
    }
    combined_path = export.export_combined(
        f"{args.version}-combined", args.models_dir,
        [(args.version, model_dump, tables[0]), (first_version, first_dump, tables[1])],
        golden, combined_report,
    )

    print(json.dumps(
        {name: report[name]["test"] for name in ("logistic_regression", "logistic_regression_all_numeric", "lightgbm")},
        indent=2,
    ))
    print(json.dumps({"policy": report["policy"], "export": report["export"]}, indent=2))
    print(json.dumps({k: combined_report[k] for k in ("comparison_at_1pct", "precision_targets", "policy")}, indent=2))
    print(f"models written to {model_path} and {combined_path}")
    print(f"done in {time.time() - started:.0f}s")


if __name__ == "__main__":
    main()
