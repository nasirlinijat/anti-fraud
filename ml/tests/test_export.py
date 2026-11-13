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


@pytest.mark.parametrize("version", ["lgbm-ieee-v5", "lgbm-game-v9"])
def test_fast_reader_matches_exported_golden_rows(version):
    models = Path(__file__).resolve().parents[2] / "backend" / "models"
    dump = json.loads((models / f"{version}.json").read_text())
    golden = pd.read_csv(models / f"{version}.golden.csv")
    scores = export.predict_fast(dump, golden[dump["feature_names"]].to_numpy())
    assert np.max(np.abs(scores - golden["expected"].to_numpy())) < 1e-9


def test_flag_by_money_picks_the_best_cut():
    y = np.array([1, 0, 1, 0, 0, 1])
    score = np.array([0.9, 0.8, 0.7, 0.6, 0.5, 0.1])
    amount = np.array([100.0, 50.0, 30.0, 20.0, 10.0, 5.0])
    # Net after each cut with review cost 10: 90, 80, 100, 90, 80, 75.
    flag_at, net = export.flag_by_money(y, score, amount, 10)
    assert flag_at == 0.7 and net == 100


def test_flag_by_money_flags_nothing_when_reviews_cost_more():
    y = np.array([1, 0])
    score = np.array([0.9, 0.2])
    flag_at, net = export.flag_by_money(y, score, np.array([5.0, 5.0]), 10)
    assert flag_at > 0.9 and net == 0


def test_flag_by_money_keeps_equal_scores_together():
    y = np.array([1, 0, 0])
    score = np.array([0.8, 0.8, 0.3])
    flag_at, net = export.flag_by_money(y, score, np.array([100.0, 1.0, 1.0]), 10)
    assert flag_at == 0.8 and net == 80


def test_choose_thresholds_by_money_or_rate():
    rng = np.random.default_rng(1)
    y = (rng.random(5000) < 0.05).astype(int)
    score = np.clip(rng.normal(0.2 + 0.4 * y, 0.15), 0, 1)
    amount = rng.uniform(1, 200, 5000)
    by_rate = export.choose_thresholds(y, score, 0.01, 0.02)
    by_money = export.choose_thresholds(y, score, 0.01, 0.02, amount, review_cost=5)
    assert by_rate["flag_by"] == "review_rate" and by_money["flag_by"] == "money"
    assert by_money["flag_at"] == round(export.flag_by_money(y, score, amount, 5)[0], 6)
    assert by_money["clear_below"] <= by_money["flag_at"]
    effect = export.policy_effect(y, score, by_money, amount)
    assert 0 <= effect["flagged_fraud_money_caught"] <= 1


def test_flag_by_precision_picks_the_lowest_cut_that_reaches_the_target():
    y = np.array([1, 1, 0, 1, 0, 0, 1])
    score = np.array([0.9, 0.8, 0.7, 0.6, 0.5, 0.4, 0.3])
    # Precision after each cut: 1, 1, 0.67, 0.75, 0.6, 0.5, 0.57.
    assert export.flag_by_precision(y, score, 0.75) == 0.6
    assert export.flag_by_precision(y, score, 0.55) == 0.3
    assert export.flag_by_precision(y, score, 1.0) == 0.8


def test_flag_by_precision_flags_nothing_when_the_target_is_out_of_reach():
    y = np.array([0, 1])
    score = np.array([0.9, 0.2])
    assert export.flag_by_precision(y, score, 0.9) > 0.9


def test_flag_by_precision_keeps_equal_scores_together():
    y = np.array([1, 0, 0])
    score = np.array([0.8, 0.8, 0.3])
    assert export.flag_by_precision(y, score, 0.6) > 0.8
    assert export.flag_by_precision(y, score, 0.5) == 0.8


def test_choose_thresholds_by_precision():
    rng = np.random.default_rng(2)
    y = (rng.random(5000) < 0.05).astype(int)
    score = np.clip(rng.normal(0.2 + 0.4 * y, 0.15), 0, 1)
    thresholds = export.choose_thresholds(y, score, 0.01, 0.02, target_precision=0.8)
    assert thresholds["flag_by"] == "precision" and thresholds["target_precision"] == 0.8
    assert export.policy_effect(y, score, thresholds)["flagged_precision"] >= 0.8


def test_precision_targets_report_test_effect():
    rng = np.random.default_rng(3)
    y = (rng.random(4000) < 0.05).astype(int)
    score = np.clip(rng.normal(0.2 + 0.4 * y, 0.15), 0, 1)
    rows = export.precision_targets(y[:2000], score[:2000], y[2000:], score[2000:], rng.uniform(1, 200, 2000))
    assert [row["target_precision"] for row in rows] == export.PRECISION_TARGETS
    caught = [row["test_fraud_caught"] for row in rows]
    assert caught == sorted(caught, reverse=True)


def test_percentile_counts_the_table_strictly_below():
    table = np.array([0.1, 0.1, 0.2, 0.5])
    assert export.percentile(table, np.array([0.05, 0.1, 0.2, 0.3, 0.9])).tolist() == [0, 0, 0.5, 0.75, 1]


def test_combined_score_takes_the_higher_percentile(model_and_rows):
    model, rows, _ = model_and_rows
    dump = model.booster_.dump_model()
    values = rows.to_numpy()
    scores = export.predict_fast(dump, values)
    low_table = export.percentile_table(scores)
    high_table = export.percentile_table(scores / 2)
    combined = export.combined_score([(dump, low_table), (dump, high_table)], values)
    assert np.all(combined == np.maximum(export.percentile(low_table, scores), export.percentile(high_table, scores)))
    assert np.all(combined >= export.percentile(low_table, scores))


def test_averaged_export_matches_the_models(model_and_rows, tmp_path):
    model, rows, labels = model_and_rows
    other = lgb.LGBMClassifier(n_estimators=30, num_leaves=5, use_missing=False, random_state=3, verbose=-1)
    other.fit(rows, labels, eval_X=rows, eval_y=labels)
    report = {}
    path, dump = export.export([model, other], ["a", "b", "c"], "avg-v1", tmp_path, rows.head(50), report)
    raw = (model.predict(rows, raw_score=True) + other.predict(rows, raw_score=True)) / 2
    assert np.max(np.abs(export.predict_fast(dump, rows.to_numpy()) - 1 / (1 + np.exp(-raw)))) < 1e-9
    assert report["export"]["trees"] == len(model.booster_.dump_model()["tree_info"]) + 30
    assert report["export"]["tree_reader_max_difference"] < 1e-9


def test_blend_with_history_uses_only_earlier_scores():
    scores = [0.2, 0.8, 0.4, 0.6, 1.0]
    players = ["a", "b", "a", "a", "b"]
    got = export.blend_with_history(scores, players, 0.5)
    want = [0.2, 0.8, 0.5 * 0.4 + 0.5 * 0.2, 0.5 * 0.6 + 0.5 * (0.2 + 0.4) / 2, 0.5 * 1.0 + 0.5 * 0.8]
    assert np.allclose(got, want, atol=1e-15)
    assert np.array_equal(export.blend_with_history(scores, players, 0), scores)


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
    path, _ = export.export(model, ["a", "b", "c"], "test-v1", tmp_path, rows.head(50), report)
    dump = json.loads(path.read_text())
    assert dump["model_version"] == "test-v1"
    assert len(pd.read_csv(tmp_path / "test-v1.golden.csv")) == 50
    assert report["export"]["tree_reader_max_difference"] < 1e-9
