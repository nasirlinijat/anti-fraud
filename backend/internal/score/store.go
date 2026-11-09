package score

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nasirlinijat/anti-fraud/backend/internal/db"
	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
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
	ClientOS             *string
	ClientBrowser        *string
	ScreenResolution     *string
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
	// FeatureHistory is the history the model inputs were computed from. Save
	// uses it to add the purchase to the feature running totals.
	FeatureHistory features.History
}

// ErrDuplicateTransaction means a purchase with the same transaction ID is already saved.
var ErrDuplicateTransaction = errors.New("transaction ID is already saved")

// Conn runs queries and starts transactions. *pgxpool.Pool and pgx.Tx implement it.
type Conn interface {
	db.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store saves scored purchases and their decisions, and reads purchase history.
type Store struct {
	conn Conn
}

func NewStore(conn Conn) *Store {
	return &Store{conn: conn}
}

// CardPurchasesLastHour counts saved purchases with the card in the hour before
// at. A purchase exactly one hour earlier is not counted.
func (s *Store) CardPurchasesLastHour(ctx context.Context, cardToken string, at time.Time) (int, error) {
	n, err := db.New(s.conn).CountCardPurchasesLastHour(ctx, db.CountCardPurchasesLastHourParams{
		CardToken: cardToken,
		EventTime: timestamptz(&at),
	})
	if err != nil {
		return 0, fmt.Errorf("count card purchases: %w", err)
	}
	return int(n), nil
}

// FeatureInput returns the request fields the model features use.
func (p Purchase) FeatureInput() features.Input {
	return features.Input{
		TransactionID:        p.TransactionID,
		EventTime:            p.EventTime,
		AmountMinor:          p.AmountMinor,
		PlayerID:             p.PlayerID,
		CardToken:            p.CardToken,
		CardIssuer:           p.CardIssuer,
		CardNetwork:          p.CardNetwork,
		CardType:             p.CardType,
		CardCountry:          p.CardCountry,
		CardCategory:         p.CardCategory,
		ProductCode:          p.ProductCode,
		BillingRegion:        p.BillingRegion,
		BillingCountry:       p.BillingCountry,
		BillingDistance:      p.BillingDistance,
		EmailDomain:          p.EmailDomain,
		RecipientEmailDomain: p.RecipientEmailDomain,
		DeviceType:           p.DeviceType,
		DeviceName:           p.DeviceName,
		AccountCreatedAt:     p.AccountCreatedAt,
		ClientOS:             p.ClientOS,
		ClientBrowser:        p.ClientBrowser,
		ScreenResolution:     p.ScreenResolution,
	}
}

// FeatureHistory reads the purchase history the model features need.
func (s *Store) FeatureHistory(ctx context.Context, in features.Input) (features.History, error) {
	return features.NewStore(s.conn).History(ctx, in)
}

// Save stores a purchase and its decision in one transaction and returns the
// purchase ID. It returns ErrDuplicateTransaction if the transaction ID is already saved.
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
		ClientOs:             text(p.ClientOS),
		ClientBrowser:        text(p.ClientBrowser),
		ScreenResolution:     text(p.ScreenResolution),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "purchases_transaction_id_key" {
			return 0, ErrDuplicateTransaction
		}
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
	if err := features.AddToCounts(ctx, tx, p.FeatureInput(), d.FeatureHistory); err != nil {
		return 0, fmt.Errorf("purchase %s: %w", p.TransactionID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit purchase %s: %w", p.TransactionID, err)
	}
	return id, nil
}

const uniqueViolation = "23505"

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
