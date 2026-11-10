import numpy as np
import pandas as pd

import replay
import train_game


def test_by_scenario_counts_flags_per_scenario():
    frame = pd.DataFrame({
        "pattern": ["normal", "normal", "stolen_card", "stolen_card", "traveler"],
        "is_fraud": [0, 0, 1, 1, 0],
    })
    score = np.array([0.05, 0.6, 0.9, 0.3, 0.1])
    rows = train_game.by_scenario(frame, score, {"flag_at": 0.5, "clear_below": 0.2})
    assert rows == [
        {"scenario": "normal", "fraud": False, "purchases": 2, "flagged": 1, "flagged_share": 0.5, "below_clear_below_share": 0.5},
        {"scenario": "stolen_card", "fraud": True, "purchases": 2, "flagged": 1, "flagged_share": 0.5, "below_clear_below_share": 0.0},
        {"scenario": "traveler", "fraud": False, "purchases": 1, "flagged": 0, "flagged_share": 0.0, "below_clear_below_share": 1.0},
    ]


def test_scenario_outcomes_compare_rules_with_rules_and_model():
    rows = pd.DataFrame({
        "pattern": ["stolen_card", "stolen_card", "normal", "normal"],
        "is_fraud": [1, 1, 0, 0],
        "rule_score": [0, 60, 0, 90],
        "outcome": ["review", "review", "allow", "block"],
    })
    assert replay.scenario_outcomes(rows) == [
        {"scenario": "normal", "fraud": False, "purchases": 2, "rules_only_stopped": 0.5, "rules_and_model_stopped": 0.5},
        {"scenario": "stolen_card", "fraud": True, "purchases": 2, "rules_only_stopped": 0.5, "rules_and_model_stopped": 1.0},
    ]


def test_dumped_requests_follow_the_given_order(tmp_path):
    dump = tmp_path / "dump.jsonl"
    dump.write_text(
        '{"pattern": "normal", "is_fraud": false, "request": {"transaction_id": "a", "amount_minor": 1}}\n'
        '{"pattern": "normal", "is_fraud": false, "request": {"transaction_id": "b", "amount_minor": 2}}\n'
    )
    assert replay.dumped_requests(dump, ["b", "a"]) == [
        {"transaction_id": "b", "amount_minor": 2}, {"transaction_id": "a", "amount_minor": 1},
    ]
