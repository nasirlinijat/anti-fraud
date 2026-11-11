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
