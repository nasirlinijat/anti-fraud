"""Writes the expected feature values for the synthetic test transactions and
their account events.

Usage (from the ml folder):
    python make_fixture.py
"""
from pathlib import Path

import pandas as pd

import features

TESTDATA = Path(__file__).parent / "testdata"


def expected_features():
    raw = features.load(TESTDATA / "transactions.csv")
    built = features.build(raw.copy(), account_events(raw))
    return built[features.FEATURES].assign(TransactionID=raw["TransactionID"].to_numpy())


def account_events(raw):
    """The test account events. Each belongs to the player of the purchase in
    its player_transaction column, because the test rows build player IDs from
    IEEE-CIS columns and the Go test builds them its own way; an event without
    one belongs to a player with no purchases. event_seconds counts seconds
    since the Unix epoch, as TransactionDT does in the replay."""
    events = pd.read_csv(TESTDATA / "account_events.csv", dtype=str, keep_default_na=False)
    players = features.add_keys(raw.copy()).set_index("TransactionID")["player_id"]
    events["player_id"] = events["player_transaction"].map(lambda purchase: players[int(purchase)] if purchase else "no-purchases")
    events["event_time"] = pd.to_datetime(events["event_seconds"].astype("int64"), unit="s", utc=True)
    return features.account_event_frame(events)


def main():
    table = expected_features()
    table[["TransactionID", *features.FEATURES]].to_csv(TESTDATA / "expected_features.csv", index=False)
    print(table[["TransactionID", *features.FEATURES]].to_string(index=False))


if __name__ == "__main__":
    main()
