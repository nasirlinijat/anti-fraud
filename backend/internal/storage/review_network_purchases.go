package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const linkedPurchaseLimit = 50

// linkedPurchaseColumns are the columns scanLinkedPurchase reads, in order.
// They need the linkedPurchaseTables. A claim stays after the decision, so the
// claim holder is shown only while the purchase still awaits review.
const linkedPurchaseColumns = `p.id, p.transaction_id, p.event_time, p.amount_minor, d.outcome, r.decision,
       CASE WHEN d.outcome = 'review' AND r.purchase_id IS NULL THEN ca.username END AS claimed_by,
       d.outcome = 'review' AND r.purchase_id IS NULL AS awaiting_review,
       count(*) OVER () AS total`

const linkedPurchaseTables = networkLinkTables + `
    LEFT JOIN review_claims rc ON rc.purchase_id = p.id
    LEFT JOIN accounts ca ON ca.id = rc.account_id`

// linkedPurchaseOrder puts the newest purchases first. $5 is the page size.
const linkedPurchaseOrder = `
ORDER BY p.event_time DESC, p.id DESC
LIMIT $5`

// NetworkDevicePlayerPurchasesSQL lists the purchases of player $4 on device
// $1 between $2 and $3.
const NetworkDevicePlayerPurchasesSQL = `
SELECT ` + linkedPurchaseColumns + `
FROM ` + linkedPurchaseTables + `
WHERE p.device_id IS NOT NULL
  AND p.device_id = $1::text
  AND p.event_time BETWEEN $2::timestamptz AND $3::timestamptz
  AND p.player_id = $4::text` + linkedPurchaseOrder

// NetworkCardPlayerPurchasesSQL lists the purchases of player $4 with card $1
// between $2 and $3.
const NetworkCardPlayerPurchasesSQL = `
SELECT ` + linkedPurchaseColumns + `
FROM ` + linkedPurchaseTables + `
WHERE p.card_token = $1::text
  AND p.event_time BETWEEN $2::timestamptz AND $3::timestamptz
  AND p.player_id = $4::text` + linkedPurchaseOrder

// NetworkPlayerCardPurchasesSQL lists the purchases of player $1 with card $4
// between $2 and $3.
const NetworkPlayerCardPurchasesSQL = `
SELECT ` + linkedPurchaseColumns + `
FROM ` + linkedPurchaseTables + `
WHERE p.player_id = $1::text
  AND p.event_time BETWEEN $2::timestamptz AND $3::timestamptz
  AND p.card_token = $4::text` + linkedPurchaseOrder

// linkedPurchaseQueries holds the purchases query of each anchor, matching
// the network query that lists the linked entities of that anchor.
var linkedPurchaseQueries = map[models.BlocklistType]string{
	models.BlocklistDevice: NetworkDevicePlayerPurchasesSQL,
	models.BlocklistCard:   NetworkCardPlayerPurchasesSQL,
	models.BlocklistPlayer: NetworkPlayerCardPurchasesSQL,
}

// NetworkPurchases returns the purchases of a value linked to the purchase
// under review through the given anchor, within the network window, newest
// first. The anchor's own value is read from the purchase, so only entities
// the network links can be listed. A value the network would not link, such
// as the purchase's own player or card, or a device anchor of a purchase
// without a device, gives an empty list. It returns models.ErrReviewNotFound
// if the purchase is not under review.
func (s *ReviewStore) NetworkPurchases(ctx context.Context, purchaseID int64, anchor models.BlocklistType, value string) (models.LinkedPurchases, error) {
	seed, err := s.networkSeed(ctx, purchaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.LinkedPurchases{}, models.ErrReviewNotFound
	}
	if err != nil {
		return models.LinkedPurchases{}, fmt.Errorf("get network seed for case %d: %w", purchaseID, err)
	}
	result := models.LinkedPurchases{Anchor: anchor, Value: value, WindowDays: networkWindowDays, Purchases: []models.LinkedPurchase{}}
	link, found := findNetworkLink(seed, anchor)
	if !found || value == link.excluded {
		return result, nil
	}
	from := seed.eventTime.AddDate(0, 0, -networkWindowDays)
	to := seed.eventTime.AddDate(0, 0, networkWindowDays)
	result.Purchases, result.Total, err = s.linkedPurchases(ctx, link, value, from, to)
	if err != nil {
		return models.LinkedPurchases{}, fmt.Errorf("list %s linked purchases for case %d: %w", anchor, purchaseID, err)
	}
	return result, nil
}

func findNetworkLink(seed networkSeed, anchor models.BlocklistType) (networkLink, bool) {
	for _, link := range networkLinks(seed) {
		if link.anchor == anchor {
			return link, true
		}
	}
	return networkLink{}, false
}

func (s *ReviewStore) linkedPurchases(ctx context.Context, link networkLink, value string, from, to time.Time) ([]models.LinkedPurchase, int64, error) {
	rows, err := s.conn.Query(ctx, linkedPurchaseQueries[link.anchor], link.value, from, to, value, int32(linkedPurchaseLimit))
	if err != nil {
		return nil, 0, err
	}
	var total int64
	purchases, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.LinkedPurchase, error) {
		var p models.LinkedPurchase
		err := row.Scan(&p.PurchaseID, &p.TransactionID, &p.EventTime, &p.AmountMinor, &p.Outcome,
			&p.ReviewDecision, &p.ClaimedBy, &p.AwaitingReview, &total)
		p.EventTime = p.EventTime.UTC()
		return p, err
	})
	if err != nil {
		return nil, 0, err
	}
	return purchases, total, nil
}
