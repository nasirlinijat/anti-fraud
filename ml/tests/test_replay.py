from pathlib import Path

import numpy as np

import features
import replay

TESTDATA = Path(__file__).resolve().parent.parent / "testdata"


def test_requests_use_the_api_format():
    data = features.load(TESTDATA / "transactions.csv")
    requests = replay.requests_from(data)
    assert len(requests) == len(data)

    first = requests[0]
    assert first == {
        "transaction_id": "1",
        "event_time": "2018-01-10T00:00:00Z",
        "amount_minor": 5000,
        "player_id": first["player_id"],
        "card_token": first["card_token"],
        "card_issuer": "1001",
        "card_network": "visa",
        "card_type": "debit",
        "card_country": "150",
        "card_category": "226",
        "product_code": "W",
        "billing_region": "100",
        "billing_country": "87",
        "billing_distance": 10.0,
        "email_domain": "gmail.com",
        "device_type": "desktop",
        "device_name": "Windows",
        "client_os": "Windows 10",
        "client_browser": "chrome 63.0",
        "screen_resolution": "1920x1080",
        "account_created_at": "2017-12-11T00:00:00Z",
    }
    assert requests[4]["amount_minor"] == 10000
    assert "email_domain" not in requests[7] and "device_name" not in requests[7]
    assert "client_os" not in requests[7] and "account_created_at" not in requests[7]
    assert requests[5]["client_browser"] == "chrome 62.0" and "client_os" not in requests[5]


def test_requests_keep_the_player_and_card_keys():
    data = features.load(TESTDATA / "transactions.csv")
    requests = replay.requests_from(data)
    keyed = features.add_keys(data.copy())
    assert [r["player_id"] for r in requests] == keyed["player_id"].tolist()
    assert [r["card_token"] for r in requests] == keyed["card_id"].tolist()
    assert requests[0]["player_id"] == requests[1]["player_id"]


def test_rule_outcome_uses_the_default_thresholds():
    assert replay.rule_outcome(np.array([0, 49, 50, 79, 80])).tolist() == ["allow", "allow", "review", "review", "block"]


def test_labels_csv_keeps_truth_out_of_requests(tmp_path):
    import pandas as pd
    frame = pd.DataFrame({"transaction_id": ["one"], "is_fraud": [1]})
    replay.write_labels(frame, tmp_path / "labels.csv")
    assert list(pd.read_csv(tmp_path / "labels.csv")) == ["transaction_id", "is_fraud"]
    frame["pattern"] = "stolen_card"
    replay.write_labels(frame, tmp_path / "labels.csv")
    saved = pd.read_csv(tmp_path / "labels.csv")
    assert list(saved) == ["transaction_id", "is_fraud", "scenario"]
    assert saved.loc[0, "scenario"] == "stolen_card"
