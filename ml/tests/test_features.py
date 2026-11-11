import json
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


def test_device_features(raw):
    built = build_for(raw)
    assert built.loc[4, "device_is_new_for_player"] == 1
    assert built.loc[4, "device_type"] == 1
    assert built.loc[7, "player_distinct_devices"] == 2
    assert built.loc[7, "device_info_share"] == pytest.approx(0.5)
    assert built.loc[6, "device_type"] == features.MISSING
    assert built.loc[6, "player_distinct_devices"] == 0


def test_shared_entity_features(raw):
    built = build_for(raw)
    assert built.loc[4, "card_distinct_players"] == 0
    assert built.loc[4, "card1_distinct_players"] == 1
    assert built.loc[7, "card1_distinct_emails"] == 2


def test_account_age_comes_from_d1(raw):
    built = build_for(raw)
    assert built.loc[1, "account_age_days"] == 30
    assert built.loc[7, "account_age_days"] == 32
    assert built.loc[8, "account_age_days"] == features.MISSING


def test_client_features(raw):
    built = build_for(raw)
    assert built.loc[1, "os_family"] == 0 and built.loc[1, "browser_family"] == 0
    assert built.loc[4, "os_family"] == 1 and built.loc[4, "browser_family"] == 1
    assert built.loc[7, "os_share"] == pytest.approx(3 / 6)
    assert built.loc[7, "browser_share"] == pytest.approx(2 / 6)
    assert built.loc[7, "screen_area"] == 1366 * 768
    assert built.loc[3, "browser_is_new_for_player"] == 1
    assert built.loc[4, "os_is_new_for_player"] == 1
    assert built.loc[7, "browser_is_new_for_player"] == 0
    assert built.loc[7, "player_distinct_browsers"] == 3


def test_missing_client_details(raw):
    built = build_for(raw)
    assert built.loc[5, "has_client_info"] == 0
    for name in ("os_family", "browser_family", "os_share", "browser_share", "screen_share", "screen_area"):
        assert built.loc[5, name] == features.MISSING
    # A browser without an operating system or screen still counts as client details.
    assert built.loc[6, "has_client_info"] == 1
    assert built.loc[6, "os_share"] == features.MISSING
    assert built.loc[6, "browser_is_new_for_player"] == 1


def test_first_purchase_has_no_new_client_flags(raw):
    built = build_for(raw)
    assert built.loc[1, "os_is_new_for_player"] == 0
    assert built.loc[1, "browser_is_new_for_player"] == 0


def test_issuer_and_region_codes(raw):
    built = build_for(raw)
    assert built.loc[1, "card_issuer_code"] == 1001
    assert built.loc[5, "billing_region_code"] == 200
    assert built.loc[8, "billing_region_code"] == features.MISSING


def test_hour_of_day_is_not_a_model_input():
    assert "hour_of_day" not in features.FEATURES


def test_player_average_uses_minor_units(raw):
    built = build_for(raw)
    assert built.loc[7, "player_avg_amount"] == (5000 + 2000 + 5000 + 25000) / 100 / 4


def test_split_with_other_days():
    data = pd.DataFrame({"day": np.arange(0, 50)})
    train, validation, test = features.split(data, {"train": (1, 10), "validation": (20, 25), "test": (40, 49)})
    assert (train["day"].min(), train["day"].max()) == (1, 10)
    assert (validation["day"].min(), validation["day"].max()) == (20, 25)
    assert (test["day"].min(), test["day"].max()) == (40, 49)


def test_load_export_joins_labels_and_counts_days(tmp_path):
    expected = pd.read_csv(TESTDATA / "expected_features.csv")
    raw = features.load(TESTDATA / "transactions.csv")
    start = pd.Timestamp("2017-12-01T00:00:00Z")
    export = expected[["TransactionID", *features.FEATURES]].rename(columns={"TransactionID": "transaction_id"})
    export.insert(1, "event_time", [(start + pd.Timedelta(seconds=int(s))).strftime("%Y-%m-%dT%H:%M:%SZ") for s in raw["TransactionDT"]])
    export.insert(2, "player_id", ["p1"] * 4 + ["p2"] * 2 + ["p1", "p3"])
    export.insert(3, "model_score", "")
    export.to_csv(tmp_path / "inputs.csv", index=False)
    pd.DataFrame({"transaction_id": [str(i) for i in range(1, 8)], "is_fraud": [0, 0, 0, 1, 0, 0, 0]}).to_csv(tmp_path / "labels.csv", index=False)

    data, unlabelled = features.load_export(tmp_path / "inputs.csv", tmp_path / "labels.csv", start)
    assert unlabelled == 1
    assert len(data) == 7 and data["is_fraud"].tolist() == [0, 0, 0, 1, 0, 0, 0]
    assert data["day"].tolist() == (raw["TransactionDT"].head(7) // features.SECONDS_PER_DAY).tolist()
    pd.testing.assert_frame_equal(data[features.FEATURES], expected[features.FEATURES].head(7).astype(float))


def test_load_export_rejects_another_feature_list(tmp_path):
    pd.DataFrame({"transaction_id": ["1"], "event_time": ["2017-12-01T00:00:00Z"], "player_id": ["p"], "model_score": [""], "hour_of_day": [1.0]}).to_csv(tmp_path / "inputs.csv", index=False)
    pd.DataFrame({"transaction_id": ["1"], "is_fraud": [0]}).to_csv(tmp_path / "labels.csv", index=False)
    with pytest.raises(ValueError):
        features.load_export(tmp_path / "inputs.csv", tmp_path / "labels.csv", "2017-12-01T00:00:00Z")


def test_split_follows_days_and_skips_warm_up():
    data = pd.DataFrame({"day": np.arange(0, 184)})
    train, validation, test = features.split(data)
    assert train["day"].min() == features.WARM_UP_DAYS + 1
    assert train["day"].max() == 120
    assert validation["day"].min() == 136 and validation["day"].max() == 152
    assert test["day"].min() == 153
    assert set(train.index).isdisjoint(validation.index) and set(validation.index).isdisjoint(test.index)


def write_dump(path, purchases):
    lines = []
    for index, (pattern, fraud, request) in enumerate(purchases):
        request = {"transaction_id": f"sim-txn-{index}", "card_issuer": "1500", "card_network": "visa",
                   "card_country": "AZ", "card_category": "226", "billing_region": "310", "email_domain": "gmail.com",
                   "device_type": "mobile", "device_name": "Pixel 7", **request}
        lines.append(json.dumps({"pattern": pattern, "is_fraud": fraud, "request": request}))
    path.write_text("\n".join(lines) + "\n")
    return path


def test_load_requests_keeps_the_request_ids_and_labels(tmp_path):
    dump = write_dump(tmp_path / "dump.jsonl", [
        ("normal", False, {"event_time": "2026-01-01T10:00:00Z", "amount_minor": 1999, "player_id": "p1",
                           "card_token": "c1", "account_created_at": "2025-12-22T12:00:00Z"}),
        ("stolen_card", True, {"event_time": "2026-01-03T09:30:00Z", "amount_minor": 20000, "player_id": "p2",
                               "card_token": "c1", "billing_distance": 12.5}),
        ("normal", False, {"event_time": "2026-01-02T08:00:00Z", "amount_minor": 500, "player_id": "p1",
                           "card_token": "c1", "account_created_at": "2025-12-22T12:00:00Z"}),
    ])
    raw = features.load_requests(dump)

    assert list(raw["TransactionID"]) == ["sim-txn-0", "sim-txn-2", "sim-txn-1"]
    assert list(raw["player_id"]) == ["p1", "p1", "p2"] and list(raw["card_id"]) == ["c1", "c1", "c1"]
    assert list(raw["pattern"]) == ["normal", "normal", "stolen_card"] and list(raw["isFraud"]) == [0, 0, 1]
    assert list(raw["day"]) == [0, 1, 2] and raw["TransactionDT"].iloc[0] == 10 * 3600
    # Whole days between the account's creation and the purchase; missing without it.
    assert raw["D1"].iloc[0] == 9 and raw["D1"].iloc[1] == 10 and np.isnan(raw["D1"].iloc[2])
    # A code that is not a number is missing, as in the service.
    assert raw["card3"].isna().all() and list(raw["card1"]) == [1500, 1500, 1500]

    built = features.build(raw.copy())
    assert list(built["player_txn_count"]) == [0, 1, 0]
    # Players seen with the card before, the buyer included.
    assert list(built["card_distinct_players"]) == [0, 1, 1]
    assert list(built["card_country"]) == [-1, -1, -1] and list(built["card_issuer_code"]) == [1500, 1500, 1500]
    assert list(built["billing_distance"]) == [-1, -1, 12.5]
    assert list(built["player_id"]) == ["p1", "p1", "p2"] and list(built["is_fraud"]) == [0, 0, 1]
