from pathlib import Path

import numpy as np
import pandas as pd
import pytest

import features
import make_fixture

TESTDATA = Path(__file__).resolve().parent.parent / "testdata"


@pytest.fixture
def raw():
    return features.load(TESTDATA / "transactions.csv")


def build_for(raw_rows):
    built = features.build(raw_rows.copy())
    return built.assign(TransactionID=raw_rows["TransactionID"].to_numpy()).set_index("TransactionID")


def test_matches_expected_fixture():
    expected = pd.read_csv(TESTDATA / "expected_features.csv").set_index("TransactionID")
    actual = make_fixture.expected_features().set_index("TransactionID")
    pd.testing.assert_frame_equal(actual[features.FEATURES], expected[features.FEATURES], check_exact=False, rtol=1e-9)


def test_features_never_change_when_later_rows_are_added(raw):
    full = build_for(raw)
    for count in range(1, len(raw)):
        prefix = build_for(raw.iloc[:count])
        pd.testing.assert_frame_equal(prefix[features.FEATURES], full.loc[prefix.index, features.FEATURES])


def test_first_purchase_of_a_player_has_no_history(raw):
    first = build_for(raw).loc[1]
    assert first["player_txn_count"] == 0
    assert first["player_avg_amount"] == features.MISSING
    assert first["secs_since_last"] == features.MISSING
    assert first["card_uses_1h"] == 0


def test_window_excludes_a_purchase_exactly_one_hour_earlier(raw):
    row = build_for(raw).loc[3]
    assert row["card_uses_1h"] == 1
    assert row["card_uses_24h"] == 2


def test_purchases_in_the_same_second_are_ordered_by_transaction_id(raw):
    built = build_for(raw)
    assert built.loc[5, "player_txn_count"] == 0
    assert built.loc[6, "player_txn_count"] == 1
    assert built.loc[6, "secs_since_last"] == 0


def test_new_card_and_email_are_detected(raw):
    row = build_for(raw).loc[4]
    assert row["card_uses_1h"] == 0
    assert row["player_distinct_cards"] == 1
    assert row["email_is_new_for_player"] == 1
    assert row["recipient_email_differs"] == 1


def test_missing_fields_become_minus_one(raw):
    row = build_for(raw).loc[8]
    for name in ("card_country", "card_category", "billing_country"):
        assert row[name] == features.MISSING
    assert not build_for(raw)[features.FEATURES].isna().any().any()


def test_split_follows_days_and_skips_warm_up():
    data = pd.DataFrame({"day": np.arange(0, 184)})
    train, validation, test = features.split(data)
    assert train["day"].min() == features.WARM_UP_DAYS + 1
    assert train["day"].max() == 120
    assert validation["day"].min() == 136 and validation["day"].max() == 152
    assert test["day"].min() == 153
    assert set(train.index).isdisjoint(validation.index) and set(validation.index).isdisjoint(test.index)
