package score

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// Purchase is a scored purchase as received in the score request. Optional
// fields are nil when the request omits them.
type Purchase struct {
	TransactionID        string
	EventTime            time.Time
	AmountMinor          int64
	PlayerID             string
	CardToken            string
	CardIssuer           *string
	CardNetwork          *string
	CardType             *string
	CardCountry          *string
	CardCategory         *string
	ProductCode          *string
	BillingRegion        *string
	BillingCountry       *string
	BillingDistance      *float64
	EmailDomain          *string
	RecipientEmailDomain *string
	DeviceType           *string
	DeviceName           *string
	DeviceID             *string
	IPAddress            *netip.Addr
	IPCountry            *string
	AccountCreatedAt     *time.Time
}

// Decision is the scoring result saved with a purchase.
type Decision struct {
	RulesetVersion int
	Result         scoring.Result
	// ModelInputs maps each model feature name to its value. It is saved even
	// when no model is loaded, so the inputs can be exported for training.
	ModelInputs map[string]float64
	// ModelScore is nil when no model is loaded.
	ModelScore *float64
}

// Beginner starts a database transaction. *pgxpool.Pool and pgx.Tx implement it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store saves scored purchases and their decisions.
type Store struct {
	conn Beginner
}

func NewStore(conn Beginner) *Store {
	return &Store{conn: conn}
}

// Save stores a purchase and its decision in one transaction and returns the purchase ID.
func (s *Store) Save(ctx context.Context, p Purchase, d Decision) (int64, error) {
	firedRules := d.Result.FiredRules
	if firedRules == nil {
		firedRules = []scoring.FiredRule{}
	}
	firedJSON, err := json.Marshal(firedRules)
	if err != nil {
		return 0, fmt.Errorf("encode fired rules: %w", err)
	}

	inputs := d.ModelInputs
	if inputs == nil {
		inputs = map[string]float64{}
	}
	inputsJSON, err := json.Marshal(inputs)
	if err != nil {
		return 0, fmt.Errorf("encode model inputs: %w", err)
	}

	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	id, err := q.InsertPurchase(ctx, db.InsertPurchaseParams{
		TransactionID:        p.TransactionID,
		EventTime:            timestamptz(&p.EventTime),
		AmountMinor:          p.AmountMinor,
		PlayerID:             p.PlayerID,
		CardToken:            p.CardToken,
		CardIssuer:           text(p.CardIssuer),
		CardNetwork:          text(p.CardNetwork),
		CardType:             text(p.CardType),
		CardCountry:          text(p.CardCountry),
		CardCategory:         text(p.CardCategory),
		ProductCode:          text(p.ProductCode),
		BillingRegion:        text(p.BillingRegion),
		BillingCountry:       text(p.BillingCountry),
		BillingDistance:      float8(p.BillingDistance),
		EmailDomain:          text(p.EmailDomain),
		RecipientEmailDomain: text(p.RecipientEmailDomain),
		DeviceType:           text(p.DeviceType),
		DeviceName:           text(p.DeviceName),
		DeviceID:             text(p.DeviceID),
		IpAddress:            p.IPAddress,
		IpCountry:            text(p.IPCountry),
		AccountCreatedAt:     timestamptz(p.AccountCreatedAt),
	})
	if err != nil {
		return 0, fmt.Errorf("insert purchase %s: %w", p.TransactionID, err)
	}

	err = q.InsertDecision(ctx, db.InsertDecisionParams{
		PurchaseID:     id,
		RulesetVersion: int32(d.RulesetVersion),
		Outcome:        string(d.Result.Outcome),
		RuleScore:      int32(d.Result.Score),
		FiredRules:     firedJSON,
		ModelInputs:    inputsJSON,
		ModelScore:     float8(d.ModelScore),
	})
	if err != nil {
		return 0, fmt.Errorf("insert decision for purchase %s: %w", p.TransactionID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit purchase %s: %w", p.TransactionID, err)
	}
	return id, nil
}

func text(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func float8(f *float64) pgtype.Float8 {
	if f == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *f, Valid: true}
}

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}
