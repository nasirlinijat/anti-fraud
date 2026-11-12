package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const (
	networkWindowDays = 30
	networkLinkLimit  = 30
)

// getNetworkSeed returns the values of a purchase that is still awaiting review.
const getNetworkSeed = `
SELECT p.player_id, p.card_token, p.device_id, p.event_time
FROM decisions d
JOIN purchases p ON p.id = d.purchase_id
WHERE d.purchase_id = $1
  AND d.outcome = 'review'
  AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = d.purchase_id)`

// listBlocklistedAnchors returns the types of the given device, card, and
// player values that are on the blocklist. A NULL device matches nothing.
const listBlocklistedAnchors = `
SELECT entry_type
FROM blocklist_entries
WHERE (entry_type, value) IN (('device', $1::text), ('card', $2::text), ('player', $3::text))`

// networkLinkAggregates summarize the purchases of one linked entity that share
// the anchor. They need the networkLinkTables.
const networkLinkAggregates = `count(*) AS purchases,
           count(*) FILTER (WHERE r.decision = 'reject') > 0 AS rejected,
           count(*) FILTER (WHERE d.outcome = 'block') > 0 AS blocked,
           count(*) FILTER (WHERE d.outcome = 'review' AND r.purchase_id IS NULL) > 0 AS in_review`

const networkLinkTables = `purchases p
    JOIN decisions d ON d.purchase_id = p.id
    LEFT JOIN review_resolutions r ON r.purchase_id = p.id`

// networkLinkedPage reads the linked CTE of a network query. $5 is the
// blocklist entry type of the linked values and $6 the page size. The status
// rank follows networkStatus, so the worst status comes first and the most
// purchases first within a status.
const networkLinkedPage = `
SELECT value, purchases, blocklisted, rejected, blocked, in_review, linked_total
FROM (
    SELECT l.value, l.purchases,
           EXISTS (SELECT 1 FROM blocklist_entries b WHERE b.entry_type = $5::text AND b.value = l.value) AS blocklisted,
           l.rejected, l.blocked, l.in_review,
           count(*) OVER () AS linked_total
    FROM linked l
) ranked
ORDER BY CASE WHEN blocklisted THEN 0 WHEN rejected THEN 1 WHEN blocked THEN 2 WHEN in_review THEN 3 ELSE 4 END,
         purchases DESC, value
LIMIT $6`

// NetworkDevicePlayersSQL lists the players other than $4 who made purchases
// on device $1 between $2 and $3.
const NetworkDevicePlayersSQL = `
WITH linked AS (
    SELECT p.player_id AS value, ` + networkLinkAggregates + `
    FROM ` + networkLinkTables + `
    WHERE p.device_id IS NOT NULL
      AND p.device_id = $1::text
      AND p.event_time BETWEEN $2::timestamptz AND $3::timestamptz
      AND p.player_id <> $4::text
    GROUP BY p.player_id
)` + networkLinkedPage

// NetworkCardPlayersSQL lists the players other than $4 who used card $1
// between $2 and $3.
const NetworkCardPlayersSQL = `
WITH linked AS (
    SELECT p.player_id AS value, ` + networkLinkAggregates + `
    FROM ` + networkLinkTables + `
    WHERE p.card_token = $1::text
      AND p.event_time BETWEEN $2::timestamptz AND $3::timestamptz
      AND p.player_id <> $4::text
    GROUP BY p.player_id
)` + networkLinkedPage

// NetworkPlayerCardsSQL lists the cards other than $4 that player $1 used
// between $2 and $3.
const NetworkPlayerCardsSQL = `
WITH linked AS (
    SELECT p.card_token AS value, ` + networkLinkAggregates + `
    FROM ` + networkLinkTables + `
    WHERE p.player_id = $1::text
      AND p.event_time BETWEEN $2::timestamptz AND $3::timestamptz
      AND p.card_token <> $4::text
    GROUP BY p.card_token
)` + networkLinkedPage

type networkSeed struct {
	playerID  string
	cardToken string
	deviceID  *string
	eventTime time.Time
}

// networkLink is one anchor of the purchase under review and the query that
// lists the entities sharing it, leaving out the purchase's own entity.
type networkLink struct {
	anchor     models.BlocklistType
	value      string
	query      string
	linkedType models.BlocklistType
	excluded   string
}

// Network returns the players and cards linked to the purchase with the given
// ID through its device, card, and player, if the purchase is under review.
// It returns models.ErrReviewNotFound if there is none.
func (s *ReviewStore) Network(ctx context.Context, purchaseID int64) (models.CaseNetwork, error) {
	seed, err := s.networkSeed(ctx, purchaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CaseNetwork{}, models.ErrReviewNotFound
	}
	if err != nil {
		return models.CaseNetwork{}, fmt.Errorf("get network seed for case %d: %w", purchaseID, err)
	}
	blocklisted, err := s.blocklistedAnchors(ctx, seed)
	if err != nil {
		return models.CaseNetwork{}, fmt.Errorf("check blocklisted anchors for case %d: %w", purchaseID, err)
	}
	from := seed.eventTime.AddDate(0, 0, -networkWindowDays)
	to := seed.eventTime.AddDate(0, 0, networkWindowDays)
	network := models.CaseNetwork{WindowDays: networkWindowDays}
	for _, link := range networkLinks(seed) {
		anchor, err := s.linkedEntities(ctx, link, from, to)
		if err != nil {
			return models.CaseNetwork{}, fmt.Errorf("list %s links for case %d: %w", link.anchor, purchaseID, err)
		}
		anchor.Blocklisted = blocklisted[link.anchor]
		network.Anchors = append(network.Anchors, anchor)
	}
	return network, nil
}

func (s *ReviewStore) networkSeed(ctx context.Context, purchaseID int64) (networkSeed, error) {
	var seed networkSeed
	err := s.conn.QueryRow(ctx, getNetworkSeed, purchaseID).Scan(&seed.playerID, &seed.cardToken, &seed.deviceID, &seed.eventTime)
	// The window is counted in UTC days, so a local daylight saving change
	// cannot shorten it.
	seed.eventTime = seed.eventTime.UTC()
	return seed, err
}

func (s *ReviewStore) blocklistedAnchors(ctx context.Context, seed networkSeed) (map[models.BlocklistType]bool, error) {
	rows, err := s.conn.Query(ctx, listBlocklistedAnchors, seed.deviceID, seed.cardToken, seed.playerID)
	if err != nil {
		return nil, err
	}
	types, err := pgx.CollectRows(rows, pgx.RowTo[models.BlocklistType])
	if err != nil {
		return nil, err
	}
	blocklisted := make(map[models.BlocklistType]bool, len(types))
	for _, t := range types {
		blocklisted[t] = true
	}
	return blocklisted, nil
}

func networkLinks(seed networkSeed) []networkLink {
	links := make([]networkLink, 0, 3)
	if seed.deviceID != nil {
		links = append(links, networkLink{
			anchor: models.BlocklistDevice, value: *seed.deviceID, query: NetworkDevicePlayersSQL,
			linkedType: models.BlocklistPlayer, excluded: seed.playerID,
		})
	}
	return append(links,
		networkLink{
			anchor: models.BlocklistCard, value: seed.cardToken, query: NetworkCardPlayersSQL,
			linkedType: models.BlocklistPlayer, excluded: seed.playerID,
		},
		networkLink{
			anchor: models.BlocklistPlayer, value: seed.playerID, query: NetworkPlayerCardsSQL,
			linkedType: models.BlocklistCard, excluded: seed.cardToken,
		},
	)
}

func (s *ReviewStore) linkedEntities(ctx context.Context, link networkLink, from, to time.Time) (models.NetworkAnchor, error) {
	anchor := models.NetworkAnchor{Type: link.anchor, Value: link.value}
	rows, err := s.conn.Query(ctx, link.query, link.value, from, to, link.excluded, string(link.linkedType), int32(networkLinkLimit))
	if err != nil {
		return models.NetworkAnchor{}, err
	}
	anchor.Linked, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.LinkedEntity, error) {
		e := models.LinkedEntity{Type: link.linkedType}
		var flags linkedFlags
		err := row.Scan(&e.Value, &e.Purchases, &flags.blocklisted, &flags.rejected, &flags.blocked, &flags.inReview, &anchor.LinkedTotal)
		e.Status = networkStatus(flags)
		return e, err
	})
	if err != nil {
		return models.NetworkAnchor{}, err
	}
	return anchor, nil
}

// linkedFlags record what is known about a linked entity's purchases that
// share the anchor.
type linkedFlags struct {
	blocklisted bool
	rejected    bool
	blocked     bool
	inReview    bool
}

// networkStatus returns the worst status the flags show. The order matches
// the sort in networkLinkedPage.
func networkStatus(f linkedFlags) models.NetworkStatus {
	switch {
	case f.blocklisted:
		return models.NetworkBlocklisted
	case f.rejected:
		return models.NetworkRejected
	case f.blocked:
		return models.NetworkBlocked
	case f.inReview:
		return models.NetworkInReview
	default:
		return models.NetworkClear
	}
}
