import json
from pathlib import Path

import lightgbm as lgb
import numpy as np
import pandas as pd
import pytest

import export


@pytest.fixture(scope="module")
def model_and_rows():
    rng = np.random.default_rng(7)
    rows = pd.DataFrame(rng.normal(size=(2000, 3)), columns=["a", "b", "c"])
    labels = ((rows["a"] + rows["b"] * 0.5 + rng.normal(scale=0.5, size=2000)) > 1).astype(int)
    model = lgb.LGBMClassifier(n_estimators=40, num_leaves=7, use_missing=False, random_state=7, verbose=-1)
    model.fit(rows, labels, eval_X=rows, eval_y=labels)
    return model, rows, labels.to_numpy()


def test_tree_reader_matches_lightgbm(model_and_rows):
    model, rows, _ = model_and_rows
    dump = model.booster_.dump_model()
    reproduced = export.predict_from_dump(dump, rows.to_numpy())
    assert np.max(np.abs(reproduced - model.predict_proba(rows)[:, 1])) < 1e-9


def test_fast_reader_matches_tree_reader(model_and_rows):
    model, rows, _ = model_and_rows
    dump = model.booster_.dump_model()
    values = rows.to_numpy()
    assert np.max(np.abs(export.predict_fast(dump, values) - export.predict_from_dump(dump, values))) < 1e-12


def test_fast_reader_matches_exported_golden_rows():
    models = Path(__file__).resolve().parents[2] / "backend" / "models"
    dump = json.loads((models / "lgbm-ieee-v3.json").read_text())
    golden = pd.read_csv(models / "lgbm-ieee-v3.golden.csv")
    scores = export.predict_fast(dump, golden[dump["feature_names"]].to_numpy())
    assert np.max(np.abs(scores - golden["expected"].to_numpy())) < 1e-9


def test_unsupported_split_is_rejected():
    node = {"split_feature": 0, "decision_type": "==", "left_child": {"leaf_value": 0}, "right_child": {"leaf_value": 1}}
    with pytest.raises(ValueError):
        export.check_supported(node)


def test_missing_value_handling_is_rejected():
    node = {"split_feature": 0, "decision_type": "<=", "missing_type": "NaN",
            "left_child": {"leaf_value": 0}, "right_child": {"leaf_value": 1}}
    with pytest.raises(ValueError):
        export.check_supported(node)


def test_thresholds_follow_review_rate_and_missed_fraud():
    rng = np.random.default_rng(3)
    labels = np.r_[np.zeros(10000), np.ones(500)]
    scores = np.r_[rng.uniform(0, 0.6, 10000), rng.uniform(0.3, 1, 500)]
    thresholds = export.choose_thresholds(labels, scores, review_rate=0.01, max_missed_fraud=0.02)
    assert np.mean(scores[labels == 0] >= thresholds["flag_at"]) == pytest.approx(0.01, abs=0.002)
    assert np.mean(scores[labels == 1] < thresholds["clear_below"]) <= 0.02 + 1e-9
    assert thresholds["clear_below"] <= thresholds["flag_at"]


def test_synthetic_rows_stay_in_range_and_include_missing_values():
    reference = pd.DataFrame({"count": [0.0, 1.0, 2.0, 5.0, 9.0] * 40, "amount": np.linspace(1.5, 300.25, 200)})
    rows = export.synthetic_rows(reference, 500, seed=1)
    assert list(rows.columns) == ["count", "amount"]
    counts = rows.loc[rows["count"] != -1.0, "count"]
    amounts = rows.loc[rows["amount"] != -1.0, "amount"]
    assert (counts % 1 == 0).all()
    assert amounts.between(reference["amount"].min(), reference["amount"].max()).all()
    assert (rows == -1.0).any().all()


def test_export_writes_model_rows_and_report(model_and_rows, tmp_path):
    model, rows, _ = model_and_rows
    report = {}
    path = export.export(model, ["a", "b", "c"], "test-v1", tmp_path, rows.head(50), report)
    dump = json.loads(path.read_text())
    assert dump["model_version"] == "test-v1"
    assert len(pd.read_csv(tmp_path / "test-v1.golden.csv")) == 50
    assert report["export"]["tree_reader_max_difference"] < 1e-9
