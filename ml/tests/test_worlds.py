import json
import sys
from pathlib import Path

import lightgbm as lgb
import numpy as np
import pandas as pd
import pytest
from sklearn.metrics import average_precision_score

import export
import features
import train_game
import worlds

COMPARED_MODEL = Path(__file__).resolve().parents[2] / "backend" / "models" / "lgbm-game-v5.json"


def world_rows(world, count, seed, history_start=0):
    rng = np.random.default_rng(seed)
    rows = pd.DataFrame(rng.uniform(0, 10, (count, len(features.FEATURES))), columns=features.FEATURES)
    rows["is_fraud"] = (rows["amount_to_avg"] + rng.normal(0, 2, count) > 9).astype(int)
    rows["world"] = world
    rows["history_start"] = history_start
    return rows


def two_worlds():
    """World a with a history variant from day 40, and world b without one."""
    return pd.concat(
        [world_rows("a", 300, 1), world_rows("a", 100, 2, history_start=40), world_rows("b", 200, 3)],
        ignore_index=True,
    )


def test_every_world_has_the_same_total_weight():
    labels = ["a"] * 6 + ["b"] * 2 + ["c"] * 4

    weight = worlds.world_weights(labels)

    assert np.allclose(weight, [2 / 3] * 6 + [2.0] * 2 + [1.0] * 4)
    assert np.allclose(pd.Series(weight).groupby(labels).sum(), [4.0, 4.0, 4.0])


def write_purchases(path, purchases):
    """purchases holds (transaction ID, day, player ID, amount in minor units)."""
    lines = []
    for transaction_id, day, player_id, amount in purchases:
        request = {
            "transaction_id": transaction_id,
            "event_time": f"2026-01-{day + 1:02d}T12:00:00Z",
            "amount_minor": amount,
            "player_id": player_id,
            "card_token": f"card-{player_id}",
        }
        lines.append(json.dumps({"pattern": "normal", "is_fraud": False, "request": request}))
    path.write_text("\n".join(lines) + "\n")


def test_history_variant_starts_without_history_and_keeps_its_window(tmp_path):
    dump = tmp_path / "world.jsonl"
    write_purchases(dump, [("t1", 0, "p1", 100), ("t2", 1, "p1", 200), ("t3", 5, "p1", 300), ("t4", 6, "p1", 400), ("t5", 9, "p1", 500)])
    raw = features.load_requests(dump)

    variant = worlds.history_variant(raw, 5, 3)

    assert variant["day"].tolist() == [5, 6]
    assert variant["player_txn_count"].tolist() == [0.0, 1.0]
    assert variant["player_avg_amount"].tolist() == [features.MISSING, 3.0]
    assert variant["history_start"].tolist() == [5, 5]


def test_a_world_and_its_variants_stay_on_one_side_of_every_fold():
    data = two_worlds()

    folds = list(worlds.lowo_folds(data))

    assert [world for world, _, _ in folds] == ["a", "b"]
    for world, training, held_out in folds:
        assert world not in set(training["world"])
        assert set(held_out["world"]) == {world} and set(held_out["history_start"]) == {0}
        groups = data.groupby(["world", "history_start"]).size()
        trained = training.groupby(["world", "history_start"]).size()
        assert all(trained[key] == groups[key] for key in trained.index)
    assert len(folds[0][1]) == 200 and len(folds[1][1]) == 400


def test_masked_inputs_are_constant_in_training(monkeypatch):
    data = two_worlds()
    masked = worlds.MASK_SETS["codes+history_age+amount_levels"]
    candidate = worlds.with_trees(worlds.Candidate("test", masked, {"min_child_samples": 5}), 25)
    seen = {}
    fit = lgb.LGBMClassifier.fit

    def capture(self, x, y, **kwargs):
        seen["x"] = x.copy()
        return fit(self, x, y, **kwargs)

    monkeypatch.setattr(lgb.LGBMClassifier, "fit", capture)
    model = worlds.fit_model(candidate, data, 7)

    assert (seen["x"][masked] == features.MISSING).all().all()
    assert seen["x"]["amount_to_avg"].nunique() > 1
    splits = dict(zip(features.FEATURES, model.booster_.feature_importance(importance_type="split")))
    assert all(splits[name] == 0 for name in masked)
    assert splits["amount_to_avg"] > 0


def test_exported_model_keeps_every_feature_in_order(tmp_path):
    data = two_worlds()
    candidate = worlds.with_trees(worlds.Candidate("test", worlds.MASK_SETS["codes+history_age"], {"min_child_samples": 5}), 25)
    model = worlds.fit_model(candidate, data, 7)
    golden = export.synthetic_rows(data[features.FEATURES], 50, 1)

    path, _ = export.export([model], features.FEATURES, "test-worlds", tmp_path, golden, {})

    assert json.loads(path.read_text())["feature_names"] == features.FEATURES


class ConstantModel:
    def __init__(self, value):
        self.value = value

    def predict_proba(self, x, num_iteration=None):
        score = np.full(len(x), self.value)
        return np.column_stack([1 - score, score])


def test_thresholds_come_from_out_of_world_scores_only():
    data = two_worlds()
    data.loc[data["world"] == "a", "is_fraud"] = 1
    data.loc[data.index[data["world"] == "a"][:150], "is_fraud"] = 0
    folds = [worlds.Fold("a", ConstantModel(0.2)), worlds.Fold("b", ConstantModel(0.7))]
    candidate = worlds.with_trees(worlds.Candidate("test", [], {}), 25)

    scores = worlds.out_of_world_scores(folds, data, candidate, 25)
    winner = worlds.CandidateResult(candidate, 25, 0.0, {}, folds, scores)
    args = train_game.build_parser().parse_args(["a.jsonl", "b.jsonl"])
    policy = train_game.out_of_world_policy(args, data, winner)

    held_out = (data["history_start"] == 0).to_numpy()
    assert scores.index.tolist() == data.index[held_out].tolist()
    assert set(scores[data["world"] == "a"]) == {0.2} and set(scores[data["world"] == "b"]) == {0.7}
    assert policy["flag_at"] == 0.7 and policy["clear_below"] == 0.2
    assert policy["out_of_world_effect_per_world"]["b"]["flagged_share"] == 1.0


def test_held_out_curve_ends_at_the_full_model_score():
    data = world_rows("a", 600, 4)
    candidate = worlds.with_trees(worlds.Candidate("test", [], {"min_child_samples": 5}), 50)
    model = worlds.fit_model(candidate, data, 7)
    x, y = data[features.FEATURES], data["is_fraud"].to_numpy()

    curve = worlds.held_out_curve(model, x, y)

    assert len(curve) == 2
    assert curve[0] == pytest.approx(average_precision_score(y, model.predict_proba(x, num_iteration=25)[:, 1]), abs=1e-12)
    assert curve[1] == pytest.approx(average_precision_score(y, model.predict_proba(x)[:, 1]), abs=1e-12)


def test_best_tree_count_maximizes_the_worst_world():
    curves = {"a": np.array([0.5, 0.9, 0.95]), "b": np.array([0.6, 0.7, 0.4])}

    assert worlds.best_tree_count(curves) == 2 * worlds.TREE_STEP


def write_world(path, prefix, seed):
    """A small simulator-style dump: fraud buys large amounts on new cards."""
    rng = np.random.default_rng(seed)
    lines = []
    for index, second in enumerate(np.sort(rng.integers(0, 20 * 86400, 700))):
        fraud = bool(rng.random() < 0.15)
        player = int(rng.integers(0, 40))
        request = {
            "transaction_id": f"{prefix}-{index}",
            "event_time": (pd.Timestamp("2026-01-01T00:00:00Z") + pd.Timedelta(seconds=int(second))).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "amount_minor": int(rng.integers(20000, 90000) if fraud else rng.integers(100, 30000)),
            "player_id": f"{prefix}-p{player}",
            "card_token": f"{prefix}-c{index if fraud else player}",
        }
        lines.append(json.dumps({"pattern": "stolen" if fraud else "normal", "is_fraud": fraud, "request": request}))
    path.write_text("\n".join(lines) + "\n")


def trained_model_bytes(monkeypatch, tmp_path, run, extra):
    out = tmp_path / run
    argv = [
        "train_game.py", str(tmp_path / "a.jsonl"), str(tmp_path / "b.jsonl"),
        "--history-starts", "0,5", "--history-window", "5", "--max-trees", "25", "--models", "1",
        "--compare", str(COMPARED_MODEL), "--out", str(out), "--models-dir", str(out / "models"), *extra,
    ]
    monkeypatch.setattr(sys, "argv", argv)
    train_game.main()
    return (out / "models" / "lgbm-game-v6.json").read_bytes()


def test_development_dumps_do_not_change_the_model(monkeypatch, tmp_path):
    for name, seed in [("a", 1), ("b", 2), ("dev1", 3), ("dev2", 4)]:
        write_world(tmp_path / f"{name}.jsonl", name, seed)
    dev1, dev2 = str(tmp_path / "dev1.jsonl"), str(tmp_path / "dev2.jsonl")

    without = trained_model_bytes(monkeypatch, tmp_path, "without", [])
    with_both = trained_model_bytes(monkeypatch, tmp_path, "with", ["--development", dev1, dev2])
    reversed_order = trained_model_bytes(monkeypatch, tmp_path, "reversed", ["--development", dev2, dev1])

    assert without == with_both == reversed_order
    report = json.loads((tmp_path / "with" / "models" / "lgbm-game-v6.report.json").read_text())
    assert [dump["file"] for dump in report["development"]] == ["dev1.jsonl", "dev2.jsonl"]
    assert set(report["development"][0]["models"]) == {"lgbm-game-v6", "lgbm-game-v5"}


def test_equal_world_sample_takes_the_smallest_world_size_from_each():
    rows = pd.DataFrame({"world": ["a"] * 50 + ["b"] * 8 + ["c"] * 20, "value": range(78)})

    sample = worlds.equal_world_sample(rows, seed=1)

    assert sample["world"].value_counts().to_dict() == {"a": 8, "b": 8, "c": 8}
    assert sample.index.is_unique
