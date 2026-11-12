package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// maxNewLoginCandidates bounds how many devices and how many countries one
// purchase checks for a first login in the window, so a player with logins
// from many devices cannot slow scoring down. The most recent first logins
// are checked.
const maxNewLoginCandidates = 50

// AccountEventStore saves account events and reads a player's account history.
type AccountEventStore struct {
	conn platform.DBTX
}

func NewAccountEventStore(conn platform.DBTX) *AccountEventStore {
	return &AccountEventStore{conn: conn}
}

const insertAccountEvent = `
INSERT INTO account_events (event_id, player_id, event_type, event_time, device_id, ip_country)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING`

// savedAccountEventMatches reports whether the saved event with ID $1 has the
// same fields as $2 to $6, which bind accountEventArgs.
const savedAccountEventMatches = `
SELECT ROW(player_id, event_type, event_time, device_id, ip_country)
    IS NOT DISTINCT FROM ROW($2::text, $3::text, $4::timestamptz, $5::text, $6::text)
FROM account_events
WHERE event_id = $1`

// Save stores an account event and reports whether it is new. A retry with
// the same fields returns false. An event that reuses a saved ID with
// different fields returns models.ErrDuplicateAccountEvent.
func (s *AccountEventStore) Save(ctx context.Context, e models.AccountEvent) (bool, error) {
	args := accountEventArgs(e)
	tag, err := s.conn.Exec(ctx, insertAccountEvent, args...)
	if err != nil {
		return false, fmt.Errorf("insert account event %s: %w", e.EventID, err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	// The insert waited for the conflicting event to commit, so this new
	// statement can read it.
	var matches bool
	if err := s.conn.QueryRow(ctx, savedAccountEventMatches, args...).Scan(&matches); err != nil {
		return false, fmt.Errorf("read saved account event %s: %w", e.EventID, err)
	}
	if !matches {
		return false, models.ErrDuplicateAccountEvent
	}
	return false, nil
}

// accountEventArgs binds the event fields in column order. Nil optional
// fields bind NULL.
func accountEventArgs(e models.AccountEvent) []any {
	return []any{e.EventID, e.PlayerID, string(e.EventType), e.EventTime.UTC(), e.DeviceID, e.IPCountry}
}

// AccountHistorySQL reads the player's account history after $2 and before
// $3. It returns the latest password reset, and the first login from each
// device and from each country in the window when that device or country is
// new. Only the $4 devices and $4 countries whose first login in the window is
// most recent are checked, newest first.
//
// A device or country is new when no earlier login and no earlier trusted
// purchase of the player used it, but some earlier login or trusted purchase
// had a device or country to compare with. Purchases keep the IP country as
// sent, so they are compared in upper case. Logins repeated from the same
// device or country later in the window can never be new, so only the first
// one is checked. Every check is an index seek, so the cost does not grow
// with the player's history. OFFSET 0 keeps the purchase checks as seeks:
// without it the planner can hash all of the player's purchases and every
// decision instead.
const AccountHistorySQL = `
WITH device_logins AS (
    SELECT device_id, min(event_time) AS first_login
    FROM account_events
    WHERE player_id = $1::text AND event_type = 'login' AND device_id IS NOT NULL
      AND event_time > $2::timestamptz AND event_time < $3::timestamptz
    GROUP BY device_id
    ORDER BY first_login DESC
    LIMIT $4
), country_logins AS (
    SELECT ip_country, min(event_time) AS first_login
    FROM account_events
    WHERE player_id = $1::text AND event_type = 'login' AND ip_country IS NOT NULL
      AND event_time > $2::timestamptz AND event_time < $3::timestamptz
    GROUP BY ip_country
    ORDER BY first_login DESC
    LIMIT $4
)
(
    SELECT 'password_reset' AS kind, event_time
    FROM account_events
    WHERE player_id = $1::text AND event_type = 'password_reset'
      AND event_time > $2::timestamptz AND event_time < $3::timestamptz
    ORDER BY event_time DESC
    LIMIT 1
)
UNION ALL
SELECT 'new_device', l.first_login
FROM device_logins l
WHERE NOT EXISTS (
        SELECT 1 FROM account_events e
        WHERE e.player_id = $1::text AND e.event_type = 'login'
          AND e.device_id = l.device_id AND e.event_time < l.first_login
    )
  AND NOT EXISTS (
        SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id = p.id
        WHERE p.player_id = $1::text AND p.device_id = l.device_id
          AND p.event_time < l.first_login AND ` + trustedDecision + `
          OFFSET 0
    )
  AND (
        EXISTS (
            SELECT 1 FROM account_events e
            WHERE e.player_id = $1::text AND e.event_type = 'login'
              AND e.device_id IS NOT NULL AND e.event_time < l.first_login
        )
        OR EXISTS (
            SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id = p.id
            WHERE p.player_id = $1::text AND p.device_id IS NOT NULL
              AND p.event_time < l.first_login AND ` + trustedDecision + `
            OFFSET 0
        )
    )
UNION ALL
SELECT 'new_country', l.first_login
FROM country_logins l
WHERE NOT EXISTS (
        SELECT 1 FROM account_events e
        WHERE e.player_id = $1::text AND e.event_type = 'login'
          AND e.ip_country = l.ip_country AND e.event_time < l.first_login
    )
  AND NOT EXISTS (
        SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id = p.id
        WHERE p.player_id = $1::text AND upper(p.ip_country) = l.ip_country
          AND p.event_time < l.first_login AND ` + trustedDecision + `
          OFFSET 0
    )
  AND (
        EXISTS (
            SELECT 1 FROM account_events e
            WHERE e.player_id = $1::text AND e.event_type = 'login'
              AND e.ip_country IS NOT NULL AND e.event_time < l.first_login
        )
        OR EXISTS (
            SELECT 1 FROM purchases p JOIN decisions d ON d.purchase_id = p.id
            WHERE p.player_id = $1::text AND p.ip_country IS NOT NULL
              AND p.event_time < l.first_login AND ` + trustedDecision + `
            OFFSET 0
        )
    )
ORDER BY 1, 2 DESC`

// History reads the player's account history after since and before the
// purchase time before. Events at or after the purchase time are never read.
func (s *AccountEventStore) History(ctx context.Context, playerID string, since, before time.Time) (scoring.AccountHistory, error) {
	rows, err := s.conn.Query(ctx, AccountHistorySQL, playerID, since.UTC(), before.UTC(), int32(maxNewLoginCandidates))
	if err != nil {
		return scoring.AccountHistory{}, fmt.Errorf("read account history: %w", err)
	}
	var (
		h    scoring.AccountHistory
		kind string
		at   time.Time
	)
	_, err = pgx.ForEachRow(rows, []any{&kind, &at}, func() error {
		return addToAccountHistory(&h, kind, at.UTC())
	})
	if err != nil {
		return scoring.AccountHistory{}, fmt.Errorf("read account history: %w", err)
	}
	return h, nil
}

// addToAccountHistory adds one AccountHistorySQL row.
func addToAccountHistory(h *scoring.AccountHistory, kind string, at time.Time) error {
	switch kind {
	case "password_reset":
		h.LastPasswordReset = &at
	case "new_device":
		h.NewDeviceLogins = append(h.NewDeviceLogins, at)
	case "new_country":
		h.NewCountryLogins = append(h.NewCountryLogins, at)
	default:
		return fmt.Errorf("unknown account history row %q", kind)
	}
	return nil
}
