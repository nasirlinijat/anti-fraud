package features

import (
	"context"
	"fmt"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// Store reads purchase history for the features.
type Store struct {
	conn platform.DBTX
}

func NewStore(conn platform.DBTX) *Store {
	return &Store{conn: conn}
}

// PlayerHistorySQL summarizes the player's purchases that come before the given
// one ($1 is its event time, $9 its transaction ID and $10 its device ID).
const PlayerHistorySQL = `
SELECT
    count(*) AS purchases,
    coalesce(sum(amount_minor), 0)::bigint AS amount_total_minor,
    coalesce(max(amount_minor), 0)::bigint AS amount_max_minor,
    min(event_time)::timestamptz AS first_time,
    max(event_time)::timestamptz AS last_time,
    count(*) FILTER (WHERE event_time > $1::timestamptz - interval '3600 seconds') AS uses_1h,
    count(*) FILTER (WHERE event_time > $1::timestamptz - interval '86400 seconds') AS uses_24h,
    count(*) FILTER (WHERE amount_minor = $2::bigint) AS same_amount,
    count(DISTINCT card_token) AS distinct_cards,
    count(DISTINCT coalesce(email_domain, 'none')) AS distinct_emails,
    count(*) FILTER (WHERE coalesce(email_domain, 'none') = $3::text) AS email_uses,
    count(DISTINCT device_name) AS distinct_devices,
    count(*) FILTER (WHERE device_name = $4::text) AS device_uses,
    count(*) FILTER (WHERE client_os = $5::text) AS os_uses,
    count(*) FILTER (WHERE client_browser = $6::text) AS browser_uses,
    count(*) FILTER (WHERE screen_resolution = $7::text) AS screen_uses,
    count(DISTINCT client_browser) AS distinct_browsers,
    count(*) FILTER (WHERE device_id = $10::text) AS device_id_uses
FROM purchases
WHERE player_id = $8
  AND (event_time, transaction_id) < ($1::timestamptz, $9::text)`

// CardHistorySQL summarizes the card's purchases that come before the given
// one. $4 is its billing region; a missing region is grouped as 'none'.
const CardHistorySQL = `
SELECT
    count(*) FILTER (WHERE event_time > $1::timestamptz - interval '3600 seconds') AS uses_1h,
    count(*) FILTER (WHERE event_time > $1::timestamptz - interval '86400 seconds') AS uses_24h,
    count(DISTINCT player_id) AS players,
    count(*) AS purchases,
    count(*) FILTER (WHERE coalesce(billing_region, 'none') = $4::text) AS region_uses
FROM purchases
WHERE card_token = $2
  AND (event_time, transaction_id) < ($1::timestamptz, $3::text)`

// DeviceIDHistorySQL counts the players who used the device ID before the
// given purchase ($2 is its event time and $3 its transaction ID).
const DeviceIDHistorySQL = `
SELECT count(DISTINCT player_id) AS players
FROM purchases
WHERE device_id = $1::text
  AND (event_time, transaction_id) < ($2::timestamptz, $3::text)`

// IssuerHistorySQL summarizes the card issuer's purchases that come before the
// given one. A missing issuer is grouped as the empty string. The UNION ALL
// lets the index serve the rows it covers.
const IssuerHistorySQL = `
WITH issuer_purchases AS (
    SELECT player_id, billing_region, email_domain
    FROM purchases
    WHERE coalesce(card_issuer, '') = $1::text
      AND (event_time, transaction_id) < ($2::timestamptz, $3::text)
      AND issuer_history_fits_index(card_issuer, transaction_id, player_id, billing_region, email_domain)
    UNION ALL
    SELECT player_id, billing_region, email_domain
    FROM purchases
    WHERE coalesce(card_issuer, '') = $1::text
      AND (event_time, transaction_id) < ($2::timestamptz, $3::text)
      AND NOT issuer_history_fits_index(card_issuer, transaction_id, player_id, billing_region, email_domain)
)
SELECT
    count(DISTINCT player_id) AS players,
    count(DISTINCT coalesce(billing_region, 'none')) AS billing_regions,
    count(DISTINCT coalesce(email_domain, 'none')) AS email_domains
FROM issuer_purchases`

// FeatureCountsSQL reads the running totals for all purchases, the email domain,
// the device name, the operating system, the browser, and the screen
// resolution. A NULL key matches nothing.
const FeatureCountsSQL = `
SELECT kind, key, purchases, players
FROM feature_counts
WHERE (kind = 'all' AND key = '')
   OR (kind = 'email' AND key = $1::text)
   OR (kind = 'device' AND key = $2::text)
   OR (kind = 'os' AND key = $3::text)
   OR (kind = 'browser' AND key = $4::text)
   OR (kind = 'screen' AND key = $5::text)`

// History reads the history of the purchase. Purchase history means saved
// purchases that come before in (event_time, transaction_id) order; the
// running totals in All, Email and Device count every saved purchase, so
// purchases must be scored in event time order.
func (s *Store) History(ctx context.Context, in Input) (History, error) {
	at := in.EventTime.UTC()
	var h History

	var err error
	h.Player, err = s.playerHistory(ctx, in, at)
	if err != nil {
		return History{}, fmt.Errorf("read player history: %w", err)
	}

	err = s.conn.QueryRow(ctx, CardHistorySQL, at, in.CardToken, in.TransactionID, in.RegionKey()).
		Scan(&h.Card.Uses1h, &h.Card.Uses24h, &h.Card.Players, &h.Card.Purchases, &h.Card.RegionUses)
	if err != nil {
		return History{}, fmt.Errorf("read card history: %w", err)
	}

	h.DeviceIDPlayers, err = s.deviceIDPlayers(ctx, in, at)
	if err != nil {
		return History{}, fmt.Errorf("read device ID history: %w", err)
	}

	err = s.conn.QueryRow(ctx, IssuerHistorySQL, issuerKey(in.CardIssuer), at, in.TransactionID).
		Scan(&h.Issuer.Players, &h.Issuer.BillingRegions, &h.Issuer.EmailDomains)
	if err != nil {
		return History{}, fmt.Errorf("read card issuer history: %w", err)
	}

	if err := s.readCounts(ctx, in, &h); err != nil {
		return History{}, fmt.Errorf("read feature counts: %w", err)
	}
	return h, nil
}

// playerHistory summarizes the player's earlier purchases.
func (s *Store) playerHistory(ctx context.Context, in Input, at time.Time) (PlayerHistory, error) {
	var p PlayerHistory
	var first, last *time.Time
	err := s.conn.QueryRow(ctx, PlayerHistorySQL,
		at, in.AmountMinor, in.EmailKey(), in.DeviceName, in.ClientOS, in.ClientBrowser, in.ScreenResolution,
		in.PlayerID, in.TransactionID, in.DeviceID,
	).Scan(
		&p.Purchases, &p.AmountTotalMinor, &p.AmountMaxMinor, &first, &last, &p.Uses1h, &p.Uses24h,
		&p.SameAmount, &p.DistinctCards, &p.DistinctEmails, &p.EmailUses, &p.DistinctDevices, &p.DeviceUses,
		&p.OSUses, &p.BrowserUses, &p.ScreenUses, &p.DistinctBrowsers, &p.DeviceIDUses,
	)
	if err != nil {
		return PlayerHistory{}, err
	}
	// A player without earlier purchases has no first or last time.
	if first != nil {
		p.First = *first
	}
	if last != nil {
		p.Last = *last
	}
	return p, nil
}

// readCounts reads the running totals that match the purchase into h.
func (s *Store) readCounts(ctx context.Context, in Input, h *History) error {
	rows, err := s.conn.Query(ctx, FeatureCountsSQL,
		in.EmailKey(), in.DeviceName, in.ClientOS, in.ClientBrowser, in.ScreenResolution)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, key string
		var count Count
		if err := rows.Scan(&kind, &key, &count.Purchases, &count.Players); err != nil {
			return err
		}
		switch kind {
		case "all":
			h.All = count
		case "email":
			h.Email = count
		case "device":
			h.Device = count
		case "os":
			h.OS = count
		case "browser":
			h.Browser = count
		case "screen":
			h.Screen = count
		}
	}
	return rows.Err()
}

// deviceIDPlayers counts the earlier players on the purchase's device ID. A
// purchase without a device ID has none, and the query is skipped.
func (s *Store) deviceIDPlayers(ctx context.Context, in Input, at time.Time) (int64, error) {
	if in.DeviceID == nil {
		return 0, nil
	}
	var players int64
	err := s.conn.QueryRow(ctx, DeviceIDHistorySQL, *in.DeviceID, at, in.TransactionID).Scan(&players)
	return players, err
}

// addFeatureCount adds one purchase to a running total. $3 is 1 when the
// player had no earlier purchase counted in this total.
const addFeatureCount = `
INSERT INTO feature_counts (kind, key, purchases, players)
VALUES ($1, $2, 1, $3::bigint)
ON CONFLICT (kind, key) DO UPDATE
SET purchases = feature_counts.purchases + 1,
    players = feature_counts.players + excluded.players`

// AddToCounts adds a saved purchase to the running totals. h is the history
// read before the purchase was saved; it tells whether the player is new to
// each total. Call it in the transaction that saves the purchase.
func AddToCounts(ctx context.Context, conn platform.DBTX, in Input, h History) error {
	add := func(kind, key string, newPlayer bool) error {
		var n int64
		if newPlayer {
			n = 1
		}
		if _, err := conn.Exec(ctx, addFeatureCount, kind, key, n); err != nil {
			return fmt.Errorf("add to %s feature count: %w", kind, err)
		}
		return nil
	}

	if err := add("all", "", h.Player.Purchases == 0); err != nil {
		return err
	}
	if err := add("email", in.EmailKey(), h.Player.EmailUses == 0); err != nil {
		return err
	}
	optional := []struct {
		kind      string
		key       *string
		newPlayer bool
	}{
		{"device", in.DeviceName, h.Player.DeviceUses == 0},
		{"os", in.ClientOS, h.Player.OSUses == 0},
		{"browser", in.ClientBrowser, h.Player.BrowserUses == 0},
		{"screen", in.ScreenResolution, h.Player.ScreenUses == 0},
	}
	for _, o := range optional {
		if o.key == nil {
			continue
		}
		if err := add(o.kind, *o.key, o.newPlayer); err != nil {
			return err
		}
	}
	return nil
}

func issuerKey(issuer *string) string {
	if issuer == nil {
		return ""
	}
	return *issuer
}
