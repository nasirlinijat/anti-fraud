package features

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
)

// Store reads purchase history for the features.
type Store struct {
	conn db.DBTX
}

func NewStore(conn db.DBTX) *Store {
	return &Store{conn: conn}
}

// History reads the history of the purchase. Purchase history means saved
// purchases that come before in (event_time, transaction_id) order; the
// running totals in All, Email and Device count every saved purchase, so
// purchases must be scored in event time order.
func (s *Store) History(ctx context.Context, in Input) (History, error) {
	q := db.New(s.conn)
	at := pgtype.Timestamptz{Time: in.EventTime.UTC(), Valid: true}
	device := text(in.DeviceName)
	var h History

	player, err := q.PlayerHistory(ctx, db.PlayerHistoryParams{
		PlayerID:      in.PlayerID,
		EventTime:     at,
		TransactionID: in.TransactionID,
		AmountMinor:   in.AmountMinor,
		EmailDomain:   in.EmailKey(),
		DeviceName:    device,
	})
	if err != nil {
		return History{}, fmt.Errorf("read player history: %w", err)
	}
	h.Player = PlayerHistory{
		Purchases:        player.Purchases,
		AmountTotalMinor: player.AmountTotalMinor,
		AmountMaxMinor:   player.AmountMaxMinor,
		First:            timeOf(player.FirstTime),
		Last:             timeOf(player.LastTime),
		Uses1h:           player.Uses1h,
		Uses24h:          player.Uses24h,
		SameAmount:       player.SameAmount,
		DistinctCards:    player.DistinctCards,
		DistinctEmails:   player.DistinctEmails,
		EmailUses:        player.EmailUses,
		DistinctDevices:  player.DistinctDevices,
		DeviceUses:       player.DeviceUses,
	}

	card, err := q.CardHistory(ctx, db.CardHistoryParams{
		CardToken:     in.CardToken,
		EventTime:     at,
		TransactionID: in.TransactionID,
	})
	if err != nil {
		return History{}, fmt.Errorf("read card history: %w", err)
	}
	h.Card = CardHistory{Uses1h: card.Uses1h, Uses24h: card.Uses24h, Players: card.Players}

	issuer, err := q.IssuerHistory(ctx, db.IssuerHistoryParams{
		CardIssuer:    issuerKey(in.CardIssuer),
		EventTime:     at,
		TransactionID: in.TransactionID,
	})
	if err != nil {
		return History{}, fmt.Errorf("read card issuer history: %w", err)
	}
	h.Issuer = IssuerHistory{Players: issuer.Players, BillingRegions: issuer.BillingRegions, EmailDomains: issuer.EmailDomains}

	counts, err := q.FeatureCounts(ctx, db.FeatureCountsParams{EmailDomain: in.EmailKey(), DeviceName: device})
	if err != nil {
		return History{}, fmt.Errorf("read feature counts: %w", err)
	}
	for _, c := range counts {
		count := Count{Purchases: c.Purchases, Players: c.Players}
		switch c.Kind {
		case "all":
			h.All = count
		case "email":
			h.Email = count
		case "device":
			h.Device = count
		}
	}
	return h, nil
}

// AddToCounts adds a saved purchase to the running totals. h is the history
// read before the purchase was saved; it tells whether the player is new to
// each total. Call it in the transaction that saves the purchase.
func AddToCounts(ctx context.Context, conn db.DBTX, in Input, h History) error {
	q := db.New(conn)
	add := func(kind, key string, newPlayer bool) error {
		var n int64
		if newPlayer {
			n = 1
		}
		if err := q.AddFeatureCount(ctx, db.AddFeatureCountParams{Kind: kind, Key: key, NewPlayer: n}); err != nil {
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
	if in.DeviceName != nil {
		return add("device", *in.DeviceName, h.Player.DeviceUses == 0)
	}
	return nil
}

func issuerKey(issuer *string) string {
	if issuer == nil {
		return ""
	}
	return *issuer
}

func text(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func timeOf(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
