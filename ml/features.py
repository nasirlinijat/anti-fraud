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
# Client details from the identity file: operating system, browser and screen.
CLIENT_COLUMNS = ["id_30", "id_31", "id_33"]

PRODUCT_CODES = {"W": 0, "H": 1, "C": 2, "S": 3, "R": 4}
CARD_NETWORKS = {"visa": 0, "mastercard": 1, "american express": 2, "discover": 3}
CARD_TYPES = {"debit": 0, "credit": 1, "debit or credit": 2, "charge card": 3}
DEVICE_TYPES = {"desktop": 0, "mobile": 1}
# Keyed by the first word of the value, in lower case.
OS_FAMILIES = {"windows": 0, "ios": 1, "mac": 2, "android": 3, "linux": 4}
BROWSER_FAMILIES = {
    "chrome": 0, "mobile": 1, "safari": 2, "ie": 3, "edge": 4,
    "firefox": 5, "samsung": 6, "android": 7, "opera": 8,
}

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
    "account": [
        "account_age_days",
    ],
    "client": [
        "has_client_info",
        "os_family",
        "browser_family",
        "os_share",
        "browser_share",
        "screen_share",
        "screen_area",
        "os_is_new_for_player",
        "browser_is_new_for_player",
        "player_distinct_browsers",
    ],
}

ALL_FEATURES = BASE_FEATURES + [name for group in FEATURE_GROUPS.values() for name in group]
SELECTED_GROUPS = ["device", "shared", "distance", "account", "client"]
FEATURES = BASE_FEATURES + [name for group in SELECTED_GROUPS for name in FEATURE_GROUPS[group]]

WARM_UP_DAYS = 30


def load(path, identity_path=None):
    path = Path(path)
    wanted = set(RAW_COLUMNS + DEVICE_COLUMNS + CLIENT_COLUMNS)
    df = pd.read_csv(path, usecols=lambda column: column in wanted)
    identity_path = Path(identity_path) if identity_path else path.with_name("train_identity.csv")
    if identity_path.exists() and "DeviceInfo" not in df.columns:
        identity = pd.read_csv(identity_path, usecols=["TransactionID", *DEVICE_COLUMNS, *CLIENT_COLUMNS])
        df = df.merge(identity, on="TransactionID", how="left")
    for column in [*RAW_COLUMNS, *DEVICE_COLUMNS, *CLIENT_COLUMNS]:
        if column not in df.columns:
            df[column] = np.nan
    df["amount_minor"] = np.round(df["TransactionAmt"] * 100).astype("int64")
    df["TransactionAmt"] = df["amount_minor"] / 100
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


def first_word(column):
    return column.str.lower().str.split(" ").str[0]


def build(df):
    df = add_keys(df)
    out = pd.DataFrame(index=df.index)
    amount = df["TransactionAmt"]

    out["amount"] = amount
    out["cents"] = (df["amount_minor"] % 100) / 100
    out["product_code"] = df["ProductCD"].map(PRODUCT_CODES)
    out["card_network"] = df["card4"].map(CARD_NETWORKS)
    out["card_type"] = df["card6"].map(CARD_TYPES)

    by_player = df.groupby("player_id", sort=False)
    out["player_txn_count"] = by_player.cumcount()
    # Summed in minor units, so the average equals the service's integer sum divided once.
    previous_total = by_player["amount_minor"].cumsum() - df["amount_minor"]
    out["player_avg_amount"] = (previous_total / 100 / out["player_txn_count"]).where(out["player_txn_count"] > 0)
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

    out["account_age_days"] = df["D1"]

    os_name, browser, screen = df["id_30"], df["id_31"], df["id_33"]
    out["has_client_info"] = (os_name.notna() | browser.notna() | screen.notna()).astype(int)
    out["os_family"] = first_word(os_name).map(OS_FAMILIES)
    out["browser_family"] = first_word(browser).map(BROWSER_FAMILIES)
    for name, column in [("os", os_name), ("browser", browser), ("screen", screen)]:
        key = column.fillna("")
        out[f"{name}_share"] = (key.groupby(key, sort=False).cumcount() / earlier_rows).where(column.notna() & (earlier_rows > 0))
    size = screen.str.extract(r"^(\d+)x(\d+)$").astype(float)
    out["screen_area"] = size[0] * size[1]
    for name, column in [("os", os_name), ("browser", browser)]:
        first_use = ~pd.DataFrame({"player": df["player_id"], "value": column.fillna("")}).duplicated()
        known = column.notna()
        out[f"{name}_is_new_for_player"] = (first_use & known & (out["player_txn_count"] > 0)).astype(int)
        if name == "browser":
            first_known = first_use & known
            out["player_distinct_browsers"] = first_known.groupby(df["player_id"], sort=False).cumsum() - first_known

    out = out[ALL_FEATURES].astype(float).fillna(MISSING)
    out["is_fraud"] = df["isFraud"].to_numpy()
    out["day"] = df["day"].to_numpy()
    out["player_id"] = df["player_id"].to_numpy()
    return out


# Train, validation and test days for IEEE-CIS: days 1-30 only build history,
# and days 121-135 keep training and validation apart.
SPLIT_DAYS = {"train": (WARM_UP_DAYS + 1, 120), "validation": (136, 152), "test": (153, 183)}


def split(data, days=None):
    days = days or SPLIT_DAYS

    def between(first, last):
        return data[(data["day"] >= first) & (data["day"] <= last)]

    return between(*days["train"]), between(*days["validation"]), between(*days["test"])


def load_export(inputs_path, labels_path, day_zero):
    """Reads model inputs exported by the service (backend/cmd/export-inputs)
    and joins fraud labels from a separate CSV with transaction_id and
    is_fraud. Purchases without a label are left out. day counts whole days
    since day_zero, so the IEEE-CIS replay keeps its day numbers."""
    inputs = pd.read_csv(inputs_path, dtype={"transaction_id": str, "player_id": str})
    missing = [name for name in FEATURES if name not in inputs.columns]
    if missing:
        raise ValueError(f"export has no columns {missing}; it was made for another feature list")
    labels = pd.read_csv(labels_path, dtype={"transaction_id": str})[["transaction_id", "is_fraud"]]
    data = inputs.merge(labels, on="transaction_id", how="inner")
    event_time = pd.to_datetime(data["event_time"], utc=True)
    data["day"] = ((event_time - pd.Timestamp(day_zero)).dt.total_seconds() // SECONDS_PER_DAY).astype(int)
    data = data.sort_values(["event_time", "transaction_id"], kind="stable").reset_index(drop=True)
    out = data[FEATURES].astype(float)
    out["is_fraud"] = data["is_fraud"].astype(int).to_numpy()
    out["day"] = data["day"].to_numpy()
    out["player_id"] = data["player_id"].to_numpy()
    return out, len(inputs) - len(data)
