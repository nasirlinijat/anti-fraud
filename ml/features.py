"""Builds model features from IEEE-CIS transactions.

Every history feature uses only transactions that happened before the current one,
so the same values can be computed by the live scoring service.
"""
from pathlib import Path

import numpy as np
import pandas as pd

SECONDS_PER_DAY = 86400
MISSING = -1.0

RAW_COLUMNS = [
    "TransactionID", "isFraud", "TransactionDT", "TransactionAmt", "ProductCD",
    "card1", "card2", "card3", "card4", "card5", "card6", "addr1", "addr2",
    "dist1", "P_emaildomain", "R_emaildomain", "D1",
]
DEVICE_COLUMNS = ["DeviceType", "DeviceInfo"]

PRODUCT_CODES = {"W": 0, "H": 1, "C": 2, "S": 3, "R": 4}
CARD_NETWORKS = {"visa": 0, "mastercard": 1, "american express": 2, "discover": 3}
CARD_TYPES = {"debit": 0, "credit": 1, "debit or credit": 2, "charge card": 3}
DEVICE_TYPES = {"desktop": 0, "mobile": 1}

BASE_FEATURES = [
    "amount",
    "cents",
    "product_code",
    "card_network",
    "card_type",
    "player_txn_count",
    "player_avg_amount",
    "amount_to_avg",
    "secs_since_last",
    "secs_since_first",
    "card_uses_1h",
    "card_uses_24h",
    "player_distinct_cards",
    "email_is_new_for_player",
    "hour_of_day",
    "card_country",
    "card_category",
    "billing_country",
    "email_domain_share",
    "has_recipient_email",
    "recipient_email_differs",
    "player_max_amount",
    "player_uses_1h",
    "player_uses_24h",
    "player_same_amount_count",
    "player_distinct_emails",
    "email_players_share",
]

FEATURE_GROUPS = {
    "device": [
        "has_device_info",
        "device_type",
        "device_info_share",
        "device_players_share",
        "device_is_new_for_player",
        "player_distinct_devices",
    ],
    "shared": [
        "card_distinct_players",
        "card1_distinct_players",
        "card1_distinct_addresses",
        "card1_distinct_emails",
    ],
    "money": [
        "card_amount_1h",
        "card_amount_24h",
        "player_amount_24h",
    ],
    "distance": [
        "billing_distance",
    ],
}

ALL_FEATURES = BASE_FEATURES + [name for group in FEATURE_GROUPS.values() for name in group]
SELECTED_GROUPS = ["device", "shared", "distance"]
FEATURES = BASE_FEATURES + [name for group in SELECTED_GROUPS for name in FEATURE_GROUPS[group]]

WARM_UP_DAYS = 30


def load(path, identity_path=None):
    path = Path(path)
    wanted = set(RAW_COLUMNS + DEVICE_COLUMNS)
    df = pd.read_csv(path, usecols=lambda column: column in wanted)
    identity_path = Path(identity_path) if identity_path else path.with_name("train_identity.csv")
    if identity_path.exists() and "DeviceInfo" not in df.columns:
        identity = pd.read_csv(identity_path, usecols=["TransactionID", *DEVICE_COLUMNS])
        df = df.merge(identity, on="TransactionID", how="left")
    for column in [*RAW_COLUMNS, *DEVICE_COLUMNS]:
        if column not in df.columns:
            df[column] = np.nan
    df = df.sort_values(["TransactionDT", "TransactionID"], kind="stable").reset_index(drop=True)
    df["day"] = df["TransactionDT"] // SECONDS_PER_DAY
    return df


def add_keys(df):
    def text(column):
        return column.astype(str).fillna("none")

    first_day = text((df["day"] - df["D1"]).astype("Int64"))
    df["player_id"] = text(df["card1"]) + "_" + text(df["addr1"]) + "_" + first_day
    card_id = text(df["card1"])
    for column in ["card2", "card3", "card4", "card5", "card6"]:
        card_id = card_id + "|" + text(df[column])
    df["card_id"] = card_id
    df["email"] = df["P_emaildomain"].fillna("none")
    df["device"] = df["DeviceInfo"].fillna("none")
    df["card1_key"] = text(df["card1"])
    df["address"] = text(df["addr1"])
    return df


def window_counts(times, groups, window):
    counts = np.zeros(len(times), dtype=np.int64)
    for index in groups.groupby(groups, sort=False).indices.values():
        group_times = times[index]
        starts = np.searchsorted(group_times, group_times - window, side="right")
        counts[index] = np.arange(len(index)) - starts
    return counts


def window_sums(times, groups, values, window):
    sums = np.zeros(len(times), dtype=float)
    for index in groups.groupby(groups, sort=False).indices.values():
        group_times = times[index]
        totals = np.concatenate([[0.0], np.cumsum(values[index])])
        starts = np.searchsorted(group_times, group_times - window, side="right")
        positions = np.arange(len(index))
        sums[index] = totals[positions] - totals[starts]
    return sums


def distinct_before(df, entity, other):
    first_pair = ~df.duplicated([entity, other])
    return first_pair.groupby(df[entity], sort=False).cumsum() - first_pair


def build(df):
    df = add_keys(df)
    out = pd.DataFrame(index=df.index)
    amount = df["TransactionAmt"]

    out["amount"] = amount
    out["cents"] = (amount - np.floor(amount)).round(3)
    out["product_code"] = df["ProductCD"].map(PRODUCT_CODES)
    out["card_network"] = df["card4"].map(CARD_NETWORKS)
    out["card_type"] = df["card6"].map(CARD_TYPES)

    by_player = df.groupby("player_id", sort=False)
    out["player_txn_count"] = by_player.cumcount()
    previous_total = by_player["TransactionAmt"].cumsum() - amount
    out["player_avg_amount"] = (previous_total / out["player_txn_count"]).where(out["player_txn_count"] > 0)
    out["amount_to_avg"] = (amount / out["player_avg_amount"]).where(out["player_avg_amount"] > 0)

    times = df["TransactionDT"]
    out["secs_since_last"] = times - by_player["TransactionDT"].shift(1)
    out["secs_since_first"] = (times - by_player["TransactionDT"].transform("first")).where(out["player_txn_count"] > 0)

    time_values = times.to_numpy()
    out["card_uses_1h"] = window_counts(time_values, df["card_id"], 3600)
    out["card_uses_24h"] = window_counts(time_values, df["card_id"], SECONDS_PER_DAY)

    first_card_use = ~df.duplicated(["player_id", "card_id"])
    out["player_distinct_cards"] = first_card_use.groupby(df["player_id"], sort=False).cumsum() - first_card_use

    first_email_use = ~df.duplicated(["player_id", "email"])
    out["email_is_new_for_player"] = (first_email_use & (out["player_txn_count"] > 0)).astype(int)

    out["hour_of_day"] = (times // 3600) % 24
    out["card_country"] = df["card3"]
    out["card_category"] = df["card5"]
    out["billing_country"] = df["addr2"]
    earlier_rows = pd.Series(np.arange(len(df)), index=df.index)
    out["email_domain_share"] = (df.groupby("email", sort=False).cumcount() / earlier_rows).where(earlier_rows > 0)
    out["has_recipient_email"] = df["R_emaildomain"].notna().astype(int)
    out["recipient_email_differs"] = (
        df["R_emaildomain"].notna() & (df["R_emaildomain"] != df["P_emaildomain"])
    ).astype(int)

    out["player_max_amount"] = by_player["TransactionAmt"].transform(lambda s: s.cummax().shift(1))
    out["player_uses_1h"] = window_counts(time_values, df["player_id"], 3600)
    out["player_uses_24h"] = window_counts(time_values, df["player_id"], SECONDS_PER_DAY)
    out["player_same_amount_count"] = df.groupby(["player_id", "TransactionAmt"], sort=False).cumcount()
    out["player_distinct_emails"] = first_email_use.groupby(df["player_id"], sort=False).cumsum() - first_email_use
    first_player_for_email = ~df.duplicated(["email", "player_id"])
    email_players = first_player_for_email.groupby(df["email"], sort=False).cumsum() - first_player_for_email
    first_seen_player = ~df.duplicated(["player_id"])
    players_seen = first_seen_player.cumsum() - first_seen_player
    out["email_players_share"] = (email_players / players_seen).where(players_seen > 0)

    has_device = df["DeviceType"].notna() | df["DeviceInfo"].notna()
    out["has_device_info"] = has_device.astype(int)
    out["device_type"] = df["DeviceType"].map(DEVICE_TYPES)
    known_device = df["DeviceInfo"].notna()
    out["device_info_share"] = (df.groupby("device", sort=False).cumcount() / earlier_rows).where(known_device & (earlier_rows > 0))
    device_players = distinct_before(df, "device", "player_id")
    out["device_players_share"] = (device_players / players_seen).where(known_device & (players_seen > 0))
    first_device_use = ~df.duplicated(["player_id", "device"])
    out["device_is_new_for_player"] = (first_device_use & known_device & (out["player_txn_count"] > 0)).astype(int)
    first_known_device = first_device_use & known_device
    out["player_distinct_devices"] = first_known_device.groupby(df["player_id"], sort=False).cumsum() - first_known_device

    out["card_distinct_players"] = distinct_before(df, "card_id", "player_id")
    out["card1_distinct_players"] = distinct_before(df, "card1_key", "player_id")
    out["card1_distinct_addresses"] = distinct_before(df, "card1_key", "address")
    out["card1_distinct_emails"] = distinct_before(df, "card1_key", "email")

    amounts = amount.to_numpy(dtype=float)
    out["card_amount_1h"] = window_sums(time_values, df["card_id"], amounts, 3600)
    out["card_amount_24h"] = window_sums(time_values, df["card_id"], amounts, SECONDS_PER_DAY)
    out["player_amount_24h"] = window_sums(time_values, df["player_id"], amounts, SECONDS_PER_DAY)

    out["billing_distance"] = df["dist1"]

    out = out[ALL_FEATURES].astype(float).fillna(MISSING)
    out["is_fraud"] = df["isFraud"].to_numpy()
    out["day"] = df["day"].to_numpy()
    out["player_id"] = df["player_id"].to_numpy()
    return out


def split(data):
    train = data[(data["day"] > WARM_UP_DAYS) & (data["day"] <= 120)]
    validation = data[(data["day"] >= 136) & (data["day"] <= 152)]
    test = data[data["day"] >= 153]
    return train, validation, test
