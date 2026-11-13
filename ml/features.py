"""Builds model features from IEEE-CIS transactions or from score requests.

Every history feature uses only transactions that happened before the current one,
so the same values can be computed by the live scoring service.
"""
import ipaddress
import json
from pathlib import Path

import numpy as np
import pandas as pd

SECONDS_PER_DAY = 86400
NANOS_PER_SECOND = 10**9
NANOS_PER_HOUR = 3600 * NANOS_PER_SECOND
MISSING = -1.0
# Account events count only within these windows before the purchase, as in
# features.AccountLookback and features.LoginDevicesWindow of the service.
ACCOUNT_LOOKBACK_NANOS = 720 * NANOS_PER_HOUR
LOGIN_DEVICES_WINDOW_NANOS = 24 * NANOS_PER_HOUR

RAW_COLUMNS = [
    "TransactionID", "isFraud", "TransactionDT", "TransactionAmt", "ProductCD",
    "card1", "card2", "card3", "card4", "card5", "card6", "addr1", "addr2",
    "dist1", "P_emaildomain", "R_emaildomain", "D1",
]
DEVICE_COLUMNS = ["DeviceType", "DeviceInfo"]
# Client details from the identity file: operating system, browser and screen.
CLIENT_COLUMNS = ["id_30", "id_31", "id_33"]
# Score request fields IEEE-CIS has no column for. They are missing in the
# dataset and present in game traffic and in the test rows.
GAME_COLUMNS = ["ip_country", "device_id", "ip_address", "cvv_result", "avs_result", "three_ds_result"]
# The IP intelligence mock's default ranges, which the service embeds. The IP
# network inputs are the mock's answer, so training reads the same file.
IP_RANGES_PATH = Path(__file__).resolve().parent.parent / "backend" / "internal" / "ipintel" / "default_ranges.json"
ANONYMOUS_KINDS = {"vpn", "proxy", "tor"}

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
# The payment verification codes go from a pass (0) to a failure (the highest
# code), with a skipped check in between.
CVV_RESULTS = {"match": 0, "not_checked": 1, "mismatch": 2}
AVS_RESULTS = {"match": 0, "partial": 1, "not_checked": 2, "mismatch": 3}
THREE_DS_RESULTS = {"authenticated": 0, "attempted": 1, "not_enrolled": 2, "failed": 3}
ASCII_UPPER = str.maketrans("abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXYZ")

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
    "codes": [
        "card_issuer_code",
        "billing_region_code",
    ],
    "game": [
        "ip_country_differs_from_card",
        "device_id_is_new_for_player",
        "device_distinct_players",
        "billing_region_is_new_for_card",
    ],
    # Payment verification results and the IP intelligence answer. IEEE-CIS
    # has none of them, so they are missing for every IEEE-CIS purchase.
    "signals": [
        "cvv_result_code",
        "avs_result_code",
        "three_ds_result_code",
        "ip_is_hosting",
        "ip_is_anonymous",
    ],
    # The player's logins and password resets before the purchase. A world
    # without account events has them missing for every purchase.
    "account_events": [
        "hours_since_password_reset",
        "device_first_login_hours",
        "country_first_login_hours",
        "login_devices_24h",
    ],
}

ALL_FEATURES = BASE_FEATURES + [name for group in FEATURE_GROUPS.values() for name in group]
SELECTED_GROUPS = ["device", "shared", "distance", "account", "client", "codes", "game", "signals", "account_events"]
FEATURES = BASE_FEATURES + [name for group in SELECTED_GROUPS for name in FEATURE_GROUPS[group]]

WARM_UP_DAYS = 30


def load(path, identity_path=None):
    path = Path(path)
    wanted = set(RAW_COLUMNS + DEVICE_COLUMNS + CLIENT_COLUMNS + GAME_COLUMNS)
    df = pd.read_csv(path, usecols=lambda column: column in wanted, dtype={name: str for name in GAME_COLUMNS})
    identity_path = Path(identity_path) if identity_path else path.with_name("train_identity.csv")
    if identity_path.exists() and "DeviceInfo" not in df.columns:
        identity = pd.read_csv(identity_path, usecols=["TransactionID", *DEVICE_COLUMNS, *CLIENT_COLUMNS])
        df = df.merge(identity, on="TransactionID", how="left")
    for column in [*RAW_COLUMNS, *DEVICE_COLUMNS, *CLIENT_COLUMNS, *GAME_COLUMNS]:
        if column not in df.columns:
            df[column] = np.nan
    for name in REQUEST_CODE_COLUMNS.values():
        df[name] = without_infinity(df[name])
    df["amount_minor"] = np.round(df["TransactionAmt"] * 100).astype("int64")
    df["TransactionAmt"] = df["amount_minor"] / 100
    df = df.sort_values(["TransactionDT", "TransactionID"], kind="stable").reset_index(drop=True)
    df["day"] = df["TransactionDT"] // SECONDS_PER_DAY
    return df


def add_keys(df):
    """Adds the keys history is counted by. IEEE-CIS has no player or card ID,
    so they are built from its columns; purchases loaded from score requests
    already carry their own."""
    def text(column):
        return column.astype(str).fillna("none")

    if "player_id" not in df.columns:
        first_day = text((df["day"] - df["D1"]).astype("Int64"))
        df["player_id"] = text(df["card1"]) + "_" + text(df["addr1"]) + "_" + first_day
    if "card_id" not in df.columns:
        card_id = text(df["card1"])
        for column in ["card2", "card3", "card4", "card5", "card6"]:
            card_id = card_id + "|" + text(df[column])
        df["card_id"] = card_id
    df["email"] = df["P_emaildomain"].fillna("none")
    df["device"] = df["DeviceInfo"].fillna("none")
    if "card1_key" not in df.columns:
        df["card1_key"] = text(df["card1"])
    if "address" not in df.columns:
        df["address"] = text(df["addr1"])
    for column in GAME_COLUMNS:
        if column not in df.columns:
            df[column] = np.nan
    if "event_ns" not in df.columns:
        # The replay sends an IEEE-CIS TransactionDT as seconds since the Unix epoch.
        df["event_ns"] = df["TransactionDT"].astype("int64") * NANOS_PER_SECOND
    if "card_country_text" not in df.columns:
        # The card country as the score request carries it: 150.0 is sent as "150".
        df["card_country_text"] = df["card3"].map(lambda code: None if pd.isna(code) else format(code, "g"))
    return df


# Score request fields and the IEEE-CIS column each one stands for.
REQUEST_TEXT_COLUMNS = {
    "product_code": "ProductCD", "card_network": "card4", "card_type": "card6",
    "email_domain": "P_emaildomain", "recipient_email_domain": "R_emaildomain",
    "device_type": "DeviceType", "device_name": "DeviceInfo",
    "client_os": "id_30", "client_browser": "id_31", "screen_resolution": "id_33",
}
# Codes the service reads as numbers; text that is not a number is missing.
REQUEST_CODE_COLUMNS = {
    "card_issuer": "card1", "card_country": "card3", "card_category": "card5",
    "billing_region": "addr1", "billing_country": "addr2",
}


def without_infinity(codes):
    """pandas reads the text "inf" as infinity, but the service reads it as
    missing, so the model must see the same -1."""
    return codes.replace([np.inf, -np.inf], np.nan)


def load_requests(path):
    """Reads purchases written by the simulator's -dump option: one JSON object
    per line with the score request, its pattern and its fraud label. Returns
    them in the columns build() reads, with the request's own player and card
    IDs, plus the pattern. day counts whole days since midnight UTC of the
    first purchase."""
    records = []
    with open(path, encoding="utf-8") as lines:
        for line in lines:
            item = json.loads(line)
            record = item["request"]
            record["pattern"] = item["pattern"]
            record["isFraud"] = int(item["is_fraud"])
            records.append(record)
    raw = pd.DataFrame.from_records(records)

    def column(name):
        if name not in raw.columns:
            return pd.Series(np.nan, index=raw.index, dtype=object)
        # The service trims text fields and reads empty text as missing.
        return raw[name].map(lambda value: (value.strip() or None) if isinstance(value, str) else value)

    event_time = pd.to_datetime(raw["event_time"], utc=True)
    seconds = (event_time - event_time.min().normalize()).dt.total_seconds()
    created = pd.to_datetime(column("account_created_at"), utc=True)
    df = pd.DataFrame({
        "TransactionID": raw["transaction_id"].astype(str),
        "isFraud": raw["isFraud"],
        "pattern": raw["pattern"],
        "TransactionDT": seconds.astype("int64"),
        "amount_minor": raw["amount_minor"].astype("int64"),
        "TransactionAmt": raw["amount_minor"] / 100,
        "player_id": raw["player_id"].astype(str),
        "card_id": raw["card_token"].astype(str),
        "card2": np.nan,
        "dist1": pd.to_numeric(column("billing_distance"), errors="coerce"),
        # Whole days, as the service counts the account age.
        "D1": np.floor((event_time - created).dt.total_seconds() / SECONDS_PER_DAY),
        # A missing issuer or region is one more key, as in add_keys: purchases
        # without one share their history.
        "card1_key": column("card_issuer").astype(str).fillna("none"),
        "address": column("billing_region").astype(str).fillna("none"),
        "card_country_text": column("card_country"),
        "event_ns": epoch_nanos(event_time),
        **{name: column(name) for name in GAME_COLUMNS},
    })
    for field, name in REQUEST_TEXT_COLUMNS.items():
        df[name] = column(field)
    for field, name in REQUEST_CODE_COLUMNS.items():
        df[name] = without_infinity(pd.to_numeric(column(field), errors="coerce"))
    df = df.sort_values(["TransactionDT", "TransactionID"], kind="stable").reset_index(drop=True)
    df["day"] = df["TransactionDT"] // SECONDS_PER_DAY
    return df


def epoch_nanos(times):
    """Nanoseconds since the Unix epoch of UTC timestamps, whatever their
    resolution."""
    return ((times - pd.Timestamp(0, tz="UTC")) // pd.Timedelta(1, unit="ns")).astype("int64")


def account_events_path(requests_path):
    """The account events of a simulator dump are written next to it:
    world1.jsonl has world1.account_events.jsonl."""
    path = Path(requests_path)
    return path.with_name(f"{path.stem}.account_events.jsonl")


def load_account_events(requests_path):
    """Reads the account events written next to a dump, one POST
    /account-events request per line. A dump without the file has no account
    events, and its purchases get the account inputs of a player without any."""
    path = account_events_path(requests_path)
    if not path.exists():
        return account_event_frame(pd.DataFrame(columns=["player_id", "event_type", "event_time"]))
    with open(path, encoding="utf-8") as lines:
        raw = pd.DataFrame.from_records([json.loads(line) for line in lines if line.strip()])
    return account_event_frame(raw)


def account_event_frame(raw):
    """Account events in the columns account_features() reads, normalized as
    the service saves them: text trimmed, empty text missing, and the IP
    country in upper case."""
    def column(name):
        if name not in raw.columns:
            return pd.Series(None, index=raw.index, dtype=object)
        return raw[name].map(lambda value: (value.strip() or None) if isinstance(value, str) else None)

    return pd.DataFrame({
        "player_id": column("player_id"),
        "event_type": column("event_type"),
        "event_ns": epoch_nanos(pd.to_datetime(raw["event_time"], utc=True)),
        "device_id": column("device_id"),
        "ip_country": column("ip_country").map(ascii_upper, na_action="ignore"),
    })


def ascii_upper(text):
    """Upper case for ASCII letters only, as the service compares a purchase's
    IP country with the countries of account events."""
    return text.translate(ASCII_UPPER)


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


def game_features(df, player_has_history):
    """Returns the features that need the IP country or the device ID, and the
    card's billing regions. df already has the keys from add_keys."""
    ip_country, card_country, device_id = df["ip_country"], df["card_country_text"], df["device_id"]
    both_countries = ip_country.notna() & card_country.notna()
    known_device_id = device_id.notna()
    first_device_id_use = ~df.duplicated(["player_id", "device_id"])
    # A missing billing region is one more region, as in card1_distinct_addresses.
    first_region_use = ~df.duplicated(["card_id", "address"])
    card_has_history = df.groupby("card_id", sort=False).cumcount() > 0
    return {
        "ip_country_differs_from_card": (ip_country != card_country).astype(float).where(both_countries),
        "device_id_is_new_for_player": (first_device_id_use & known_device_id & player_has_history).astype(int),
        "device_distinct_players": distinct_before(df, "device_id", "player_id").where(known_device_id),
        "billing_region_is_new_for_card": (first_region_use & card_has_history).astype(int),
    }


def ip_ranges(path=IP_RANGES_PATH):
    """Reads a ranges file in the format the IP intelligence mock reads:
    (network, kind) pairs."""
    entries = json.loads(Path(path).read_text())
    return [(ipaddress.ip_network(entry["cidr"]), entry["kind"]) for entry in entries]


def ip_kinds(text, ranges):
    """The kinds of every range that contains the address, as the mock looks
    it up: an IPv4 address written in IPv6 form is looked up as IPv4. None for
    a missing or invalid address, which the service would not look up."""
    if not isinstance(text, str):
        return None
    try:
        address = ipaddress.ip_address(text)
    except ValueError:
        return None
    if address.version == 6 and address.ipv4_mapped is not None:
        address = address.ipv4_mapped
    if address.version == 6 and address.scope_id:
        # Go's netip never places an address with an IPv6 zone in a range.
        return set()
    return {kind for network, kind in ranges if address in network}


def signal_features(df, ranges):
    """Returns the payment verification codes and the IP network flags. A
    flag is missing when the purchase has no IP address, as when the service
    makes no lookup or the lookup fails."""
    kinds = df["ip_address"].map(lambda text: ip_kinds(text, ranges))
    looked_up = kinds.notna()
    return {
        "cvv_result_code": df["cvv_result"].map(CVV_RESULTS),
        "avs_result_code": df["avs_result"].map(AVS_RESULTS),
        "three_ds_result_code": df["three_ds_result"].map(THREE_DS_RESULTS),
        "ip_is_hosting": kinds.map(lambda found: "hosting" in found, na_action="ignore").astype(float).where(looked_up),
        "ip_is_anonymous": kinds.map(lambda found: bool(found & ANONYMOUS_KINDS), na_action="ignore").astype(float).where(looked_up),
    }


def events_in_window(events, keys, times, window):
    """For each purchase, the index range [first, end) of its key's events
    after time - window and before time, in the key's events sorted by time.
    Returns the ranges and the sorted event times of each key. events has a
    key and an event_ns column; a purchase with a missing key has no events."""
    first = np.zeros(len(times), dtype=np.int64)
    end = np.zeros(len(times), dtype=np.int64)
    by_key = {key: np.sort(group["event_ns"].to_numpy()) for key, group in events.groupby("key", sort=False)}
    for key, index in pd.Series(keys).groupby(keys, sort=False).indices.items():
        event_times = by_key.get(key)
        if event_times is None:
            continue
        first[index] = np.searchsorted(event_times, times[index] - window, side="right")
        end[index] = np.searchsorted(event_times, times[index], side="left")
    return first, end, by_key


def hours_since_event(events, keys, times, pick_last):
    """Hours from the first (or, with pick_last, the last) event of the key in
    the lookback window to the purchase; MISSING without one."""
    first, end, by_key = events_in_window(events, keys, times, ACCOUNT_LOOKBACK_NANOS)
    hours = np.full(len(times), MISSING)
    found = np.flatnonzero(end > first)
    for i in found:
        event_times = by_key[keys[i]]
        at = event_times[end[i] - 1] if pick_last else event_times[first[i]]
        hours[i] = (times[i] - at) / NANOS_PER_HOUR
    return hours


def login_devices(logins, players, times):
    """The distinct device IDs of each player's logins in the 24 hours before
    the purchase."""
    logins = logins[logins["device_id"].notna()].sort_values("event_ns", kind="stable")
    counts = np.zeros(len(times))
    first, end, _ = events_in_window(logins.assign(key=logins["player_id"]), players, times, LOGIN_DEVICES_WINDOW_NANOS)
    devices = {key: group["device_id"].to_numpy() for key, group in logins.groupby("player_id", sort=False)}
    for i in np.flatnonzero(end > first):
        counts[i] = len(set(devices[players[i]][first[i]:end[i]]))
    return counts


def account_features(df, events):
    """Returns the inputs from the player's account events strictly before the
    purchase, within the lookback window. login_devices_24h is missing, like
    the others, for a player without any account event in the window, so a
    world without account events looks like players who have none."""
    players, times = df["player_id"].to_numpy(dtype=object), df["event_ns"].to_numpy()
    resets = events[events["event_type"] == "password_reset"]
    logins = events[events["event_type"] == "login"]
    first, end, _ = events_in_window(events.assign(key=events["player_id"]), players, times, ACCOUNT_LOOKBACK_NANOS)
    has_events = end > first
    device_keys = pair_keys(df["player_id"], df["device_id"])
    country_keys = pair_keys(df["player_id"], df["ip_country"].map(ascii_upper, na_action="ignore"))
    return {
        "hours_since_password_reset": hours_since_event(resets.assign(key=resets["player_id"]), players, times, True),
        "device_first_login_hours": hours_since_event(
            logins.assign(key=pair_keys(logins["player_id"], logins["device_id"])), device_keys, times, False),
        "country_first_login_hours": hours_since_event(
            logins.assign(key=pair_keys(logins["player_id"], logins["ip_country"])), country_keys, times, False),
        "login_devices_24h": np.where(has_events, login_devices(logins, players, times), MISSING),
    }


def pair_keys(players, values):
    """One key per player and value; missing where the value is missing."""
    return (players.astype(str) + "\x1f" + values.astype(str)).where(values.notna()).to_numpy(dtype=object)


def build(df, account_events=None):
    """Builds the model inputs of purchases sorted by time. account_events
    holds the account events of the same players (account_event_frame());
    without them every purchase gets the inputs of a player without account
    events."""
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
    # The issuer and billing region codes themselves, not only counts built from them.
    out["card_issuer_code"] = df["card1"]
    out["billing_region_code"] = df["addr1"]

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

    for name, values in game_features(df, out["player_txn_count"] > 0).items():
        out[name] = values
    for name, values in signal_features(df, ip_ranges()).items():
        out[name] = values
    if account_events is None:
        account_events = account_event_frame(pd.DataFrame(columns=["player_id", "event_type", "event_time"]))
    for name, values in account_features(df, account_events).items():
        out[name] = values

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
