import json
import sys

import numpy as np
import pandas as pd
import pytest

import combine_game
import export
import features
import train_game
import worlds
from test_worlds import write_world

SINGLE_SPLIT = {"train": "0-9", "validation": "12-15", "test": "16-19"}
WORLD_OPTIONS = ["--history-starts", "0,5", "--history-window", "5", "--max-trees", "25"]


def single_part(world_file):
    return combine_game.Part("single-test", {}, {"source": {"file": world_file}, "split_days": SINGLE_SPLIT})


def test_reference_rows_drop_the_single_world_training_days_and_history_variants():
    day = np.tile(np.arange(20), 3)
    data = pd.DataFrame({
        "world": ["a"] * 40 + ["b"] * 20,
        "day": day,
        "history_start": [0] * 20 + [5] * 20 + [0] * 20,
        "is_fraud": 0,
    })

    rows = combine_game.reference_rows(data, single_part("a.jsonl"))

    assert rows["world"].value_counts().to_dict() == {"a": 8, "b": 8}
    assert sorted(rows.loc[rows["world"] == "a", "day"]) == [12, 13, 14, 15, 16, 17, 18, 19]
    assert set(rows["history_start"]) == {0}


def test_combined_score_is_the_highest_part_percentile():
    tables = [np.array([0.0, 1.0, 2.0, 3.0]), np.array([10.0, 20.0, 30.0, 40.0])]
    scores = [np.array([2.5, 0.0, 1.5]), np.array([15.0, 45.0, 5.0])]

    combined = combine_game.max_percentile(tables, scores)

    assert combined.tolist() == [0.75, 1.0, 0.5]


def test_missing_fold_models_stop_the_run_instead_of_training(tmp_path):
    data = pd.concat([world_frame(tmp_path, "a", 1), world_frame(tmp_path, "b", 2)], ignore_index=True)
    selection = {"max_trees": 25, "seed": 1234}

    with pytest.raises(SystemExit, match="no fold model"):
        combine_game.saved_folds(data, worlds.Candidate("none/v5", [], {}), selection, tmp_path / "empty")


def world_frame(tmp_path, name, seed):
    write_world(tmp_path / f"{name}.jsonl", name, seed)
    frame = train_game.load(tmp_path / f"{name}.jsonl")
    frame["world"], frame["history_start"] = name, 0
    return frame


@pytest.fixture(scope="module")
def trained_parts(tmp_path_factory):
    """Two small worlds, a multi-world model trained on both with its fold
    checkpoints, and a single-world model trained on the early days of a."""
    root = tmp_path_factory.mktemp("parts")
    for name, seed in [("a", 1), ("b", 2), ("dev1", 3), ("dev2", 4)]:
        write_world(root / f"{name}.jsonl", name, seed)
    argv = ["train_game.py", str(root / "a.jsonl"), str(root / "b.jsonl"), *WORLD_OPTIONS, "--models", "1",
            "--compare", "", "--version", "multi-test", "--out", str(root / "out"), "--models-dir", str(root / "models")]
    with pytest.MonkeyPatch.context() as patch:
        patch.setattr(sys, "argv", argv)
        train_game.main()
    export_single_model(root)
    return root


def export_single_model(root):
    frame = train_game.load(root / "a.jsonl")
    frame["world"] = "a"
    training = frame[frame["day"] <= 9]
    candidate = worlds.with_trees(worlds.Candidate("single", [], {"min_child_samples": 5}), 25)
    model = worlds.fit_model(candidate, training, 7)
    report = {"source": {"file": "a.jsonl"}, "split_days": SINGLE_SPLIT, "policy": {"flag_at": 0.5, "clear_below": 0.1}}
    golden = export.synthetic_rows(training[features.FEATURES], 50, 1)
    export.export([model], features.FEATURES, "single-test", root / "models", golden, report)


def combine(root, run, extra):
    """Runs the combination into its own models folder and returns that folder."""
    models = root / run
    models.mkdir()
    for version in ("multi-test", "single-test"):
        for suffix in (".json", ".report.json"):
            (models / f"{version}{suffix}").write_bytes((root / "models" / f"{version}{suffix}").read_bytes())
    argv = ["combine_game.py", str(root / "a.jsonl"), str(root / "b.jsonl"), *WORLD_OPTIONS, "--multi", "multi-test",
            "--single", "single-test", "--out", str(root / "out"), "--models-dir", str(models),
            # The small worlds separate fraud so well that the default would put clear_below at flag_at.
            "--max-missed-fraud", "0", *extra]
    with pytest.MonkeyPatch.context() as patch:
        patch.setattr(sys, "argv", argv)
        combine_game.main()
    return models


def exported_bytes(models):
    return [(models / f"multi-test-combined{suffix}").read_bytes() for suffix in (".json", ".golden.csv")]


def test_development_dumps_do_not_change_the_combination(trained_parts):
    dev1, dev2 = str(trained_parts / "dev1.jsonl"), str(trained_parts / "dev2.jsonl")

    without = combine(trained_parts, "without", [])
    with_both = combine(trained_parts, "with", ["--development", dev1, dev2])

    assert exported_bytes(without) == exported_bytes(with_both)
    report = json.loads((with_both / "multi-test-combined.report.json").read_text())
    assert [dump["file"] for dump in report["development"]] == ["dev1.jsonl", "dev2.jsonl"]
    assert set(report["development"][0]["models"]) == {"multi-test-combined", "multi-test", "single-test"}


def test_written_combination_has_what_the_service_loads(trained_parts):
    models = combine(trained_parts, "service", [])

    combination = json.loads((models / "multi-test-combined.json").read_text())
    policy = json.loads((models / "multi-test-combined.report.json").read_text())["policy"]

    assert combination["model_version"] == "multi-test-combined"
    assert combination["combination"] == "max_percentile"
    assert combination["feature_names"] == features.FEATURES
    assert [part["file"] for part in combination["models"]] == ["multi-test.json", "single-test.json"]
    assert all(np.all(np.diff(part["percentiles"]) >= 0) for part in combination["models"])
    assert 0 <= policy["clear_below"] < policy["flag_at"] <= 1
    assert policy["player_blend_weight"] == 0


def test_golden_rows_score_the_highest_percentile_of_the_parts(trained_parts):
    models = combine(trained_parts, "golden", [])

    combination = json.loads((models / "multi-test-combined.json").read_text())
    golden = pd.read_csv(models / "multi-test-combined.golden.csv")
    rows = golden[features.FEATURES].to_numpy()
    percentiles = [
        export.percentile(np.array(part["percentiles"]), export.predict_fast(json.loads((models / part["file"]).read_text()), rows))
        for part in combination["models"]
    ]

    assert np.array_equal(golden["expected"].to_numpy(), np.maximum(*percentiles))


def test_reference_set_keeps_only_unseen_days_of_the_single_world(trained_parts):
    models = combine(trained_parts, "reference", [])

    report = json.loads((models / "multi-test-combined.report.json").read_text())

    first_day = int(report["reference_set"]["days_per_world"]["a"].split("-")[0])
    assert first_day >= 12
    assert len(set(report["reference_set"]["rows_per_world"].values())) == 1


def test_options_that_differ_from_the_multi_world_training_stop_the_run(trained_parts):
    with pytest.raises(SystemExit):
        combine(trained_parts, "mismatch", ["--history-window", "4"])
