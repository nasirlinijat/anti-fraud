"""Writes the expected feature values for the synthetic test transactions.

Usage (from the ml folder):
    python make_fixture.py
"""
from pathlib import Path

import features

TESTDATA = Path(__file__).parent / "testdata"


def expected_features():
    raw = features.load(TESTDATA / "transactions.csv")
    built = features.build(raw)
    return built[features.FEATURES].assign(TransactionID=raw["TransactionID"].to_numpy())


def main():
    table = expected_features()
    table[["TransactionID", *features.FEATURES]].to_csv(TESTDATA / "expected_features.csv", index=False)
    print(table[["TransactionID", *features.FEATURES]].to_string(index=False))


if __name__ == "__main__":
    main()
