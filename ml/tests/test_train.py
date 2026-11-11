import numpy as np
import pandas as pd

import features
import train


def baseline_rows(count, seed):
    rng = np.random.default_rng(seed)
    rows = pd.DataFrame(rng.uniform(0, 50, (count, len(features.FEATURES))), columns=features.FEATURES)
    for name in train.CODE_FEATURES:
        rows[name] = rng.integers(0, 4, count).astype(float)
    rows = rows.mask(rng.random(rows.shape) < 0.3, features.MISSING)
    return rows


def test_code_features_are_model_features():
    assert set(train.CODE_FEATURES) <= set(features.FEATURES)


def test_baseline_accepts_missing_values_and_unseen_codes():
    rows = baseline_rows(400, 1)
    y = np.random.default_rng(2).integers(0, 2, len(rows))
    model = train.baseline_model().fit(rows, y)

    later = baseline_rows(50, 3)
    later["card_issuer_code"] = 99999.0
    later.iloc[0] = features.MISSING
    score = model.predict_proba(later)[:, 1]
    assert score.shape == (50,) and np.all((score >= 0) & (score <= 1))


def test_baseline_marks_missing_numbers():
    rows = baseline_rows(200, 4)
    y = np.random.default_rng(5).integers(0, 2, len(rows))
    model = train.baseline_model().fit(rows, y)
    imputer = model[0].named_transformers_["numbers"][0]
    numbers = [name for name in features.FEATURES if name not in train.CODE_FEATURES]
    assert len(imputer.indicator_.features_) == len(numbers)


def checkpoint_data(seed):
    rng = np.random.default_rng(seed)
    x = pd.DataFrame(rng.normal(size=(600, 4)), columns=list("abcd"))
    y = (x["a"] + rng.normal(scale=0.5, size=600) > 0).astype(int).to_numpy()
    return x.iloc[:400], y[:400], x.iloc[400:], y[400:]


def test_finished_model_is_loaded_from_its_checkpoint(tmp_path, monkeypatch):
    x_train, y_train, x_val, y_val = checkpoint_data(1)
    checkpoint = tmp_path / "checkpoints" / "main-seed7.joblib"
    first = train.fit_lightgbm(7, x_train, y_train, x_val, y_val, checkpoint)
    assert checkpoint.exists() and not checkpoint.with_name(checkpoint.name + ".tmp").exists()

    def fail(*args, **kwargs):
        raise AssertionError("the model was trained again")

    monkeypatch.setattr(train.lgb.LGBMClassifier, "fit", fail)
    again = train.fit_lightgbm(7, x_train, y_train, x_val, y_val, checkpoint)
    assert np.array_equal(first.predict_proba(x_val), again.predict_proba(x_val))
    assert again.best_iteration_ == first.best_iteration_


def test_checkpoint_of_other_data_or_seed_is_not_used(tmp_path):
    x_train, y_train, x_val, y_val = checkpoint_data(1)
    checkpoint = tmp_path / "model.joblib"
    train.fit_lightgbm(7, x_train, y_train, x_val, y_val, checkpoint)
    key = train.fingerprint(7, train.lightgbm_model().set_params(random_state=7).get_params(), x_train, y_train, x_val, y_val)

    other_x, other_y, _, _ = checkpoint_data(2)
    assert train.fingerprint(7, train.lightgbm_model().set_params(random_state=7).get_params(), other_x, other_y, x_val, y_val) != key
    assert train.fingerprint(8, train.lightgbm_model().set_params(random_state=8).get_params(), x_train, y_train, x_val, y_val) != key

    import joblib
    train.fit_lightgbm(8, x_train, y_train, x_val, y_val, checkpoint)
    assert joblib.load(checkpoint)["fingerprint"] != key
