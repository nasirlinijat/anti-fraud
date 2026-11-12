package score

import (
	"context"
	"errors"
	"fmt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"slices"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/features"
	"github.com/nasirlinijat/anti-fraud/backend/internal/ipintel"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

//go:generate mockgen -destination=mocks/mock_stores.go -package=mocks . Rulesets,Blocklist,Purchases,AccountEvents,Model

// Rulesets provides the active ruleset.
type Rulesets interface {
	Active(ctx context.Context) (scoring.Ruleset, error)
}

// Blocklist finds the blocklist entry that matches a purchase.
type Blocklist interface {
	Match(ctx context.Context, s models.BlocklistSubject) (models.BlocklistEntry, bool, error)
}

// Purchases saves decisions and looks up saved decisions and purchase history.
type Purchases interface {
	FindSavedDecision(ctx context.Context, p models.Purchase) (*models.Decision, error)
	CardPurchasesLastHour(ctx context.Context, cardToken string, at time.Time) (int, error)
	DeviceCardsLastHour(ctx context.Context, deviceID, cardToken string, at time.Time) (int64, error)
	TrustedHistory(ctx context.Context, p models.Purchase, windowDays int32) (*scoring.TrustedPurchaseHistory, error)
	FeatureHistory(ctx context.Context, in features.Input) (features.History, error)
	PlayerEarlierModelScores(ctx context.Context, playerID string, at time.Time, transactionID string) (float64, int64, error)
	Save(ctx context.Context, p models.Purchase, d models.Decision) (int64, error)
}

// AccountEvents reads a player's account history from the account events
// after since and before the purchase time before.
type AccountEvents interface {
	History(ctx context.Context, playerID string, since, before time.Time) (scoring.AccountHistory, error)
}

// Model scores a feature vector from 0 (legitimate) to 1 (fraud).
type Model interface {
	Predict(x []float64) (float64, error)
	Features() []string
	Version() string
}

type Metrics interface {
	Decided(d models.Decision)
}

// combination is a model made of several models, such as *lgbm.Combined. The
// player blend applies to its first model's score, before combining.
type combination interface {
	Scores(x []float64) ([]float64, error)
	Combine(scores []float64) float64
}

type Service struct {
	metrics   Metrics
	rulesets  Rulesets
	blocklist Blocklist
	purchases Purchases
	// accountEvents is nil until UseAccountEvents is called.
	accountEvents AccountEvents
	model         Model
	// modelFeatures is how many of the first features in features.Names the
	// model takes.
	modelFeatures int
	policy        scoring.ModelPolicy
	ipIntel       IPIntel
}

func NewService(rulesets Rulesets, blocklist Blocklist, purchases Purchases) *Service {
	return &Service{rulesets: rulesets, blocklist: blocklist, purchases: purchases}
}

func (s *Service) UseMetrics(m Metrics) *Service {
	s.metrics = m
	return s
}

// UseAccountEvents gives the rules that read account events their history.
// Without it those rules never fire.
func (s *Service) UseAccountEvents(a AccountEvents) *Service {
	s.accountEvents = a
	return s
}

// UseModel makes the service score purchases with the model and apply its
// policy to the rule outcome. Without a model the service uses rules only. The
// model must take the first features of features.Names, in their order.
// Features are only ever added at the end, so a model exported before the
// newest features still loads and is scored without them.
func (s *Service) UseModel(m Model, p scoring.ModelPolicy) error {
	got := m.Features()
	if len(got) == 0 || len(got) > len(features.Names) || !slices.Equal(got, features.Names[:len(got)]) {
		return fmt.Errorf("model features %v are not the first features of the service %v", got, features.Names)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	s.model, s.modelFeatures, s.policy = m, len(got), p
	return nil
}

// Score returns the decision for a purchase. A retry of a saved transaction
// returns the decision saved the first time; a different purchase with a saved
// transaction ID returns models.ErrDuplicateTransaction.
func (s *Service) Score(ctx context.Context, p models.Purchase, started time.Time) (models.Decision, error) {
	saved, err := s.purchases.FindSavedDecision(ctx, p)
	if err != nil {
		return models.Decision{}, err
	}
	if saved != nil {
		return *saved, nil
	}
	decision, err := s.decide(ctx, p, s.lookupIP(ctx, p))
	if err != nil {
		return models.Decision{}, err
	}
	decision.Latency = time.Since(started)
	return s.save(ctx, p, decision)
}

// save saves the decision. When another request saved the same transaction
// first, it returns that decision.
func (s *Service) save(ctx context.Context, p models.Purchase, decision models.Decision) (models.Decision, error) {
	_, err := s.purchases.Save(ctx, p, decision)
	if errors.Is(err, models.ErrDuplicateTransaction) {
		// Save has rolled back, so the lookup can see the committed winner.
		saved, err := s.purchases.FindSavedDecision(ctx, p)
		if err != nil {
			return models.Decision{}, err
		}
		if saved == nil {
			return models.Decision{}, errors.New("saved decision missing after duplicate transaction")
		}
		return *saved, nil
	}
	if err != nil {
		return models.Decision{}, err
	}
	if s.metrics != nil {
		s.metrics.Decided(decision)
	}
	return decision, nil
}

// decide scores a purchase that has no saved decision. ipIntel is the IP
// lookup result, or nil without one. The caller sets the latency.
func (s *Service) decide(ctx context.Context, p models.Purchase, ipIntel *ipintel.Result) (models.Decision, error) {
	rs, err := s.rulesets.Active(ctx)
	if err != nil {
		return models.Decision{}, err
	}

	// A blocklist match blocks the purchase before the rules run.
	entry, blocked, err := s.blocklist.Match(ctx, models.BlocklistSubject{
		CardToken: p.CardToken,
		PlayerID:  p.PlayerID,
		DeviceID:  p.DeviceID,
		IPAddress: p.IPAddress,
	})
	if err != nil {
		return models.Decision{}, err
	}

	// Model inputs are computed for every purchase, blocked ones too, so that
	// all saved purchases can be exported for training.
	in := p.FeatureInput()
	history, err := s.purchases.FeatureHistory(ctx, in)
	if err != nil {
		return models.Decision{}, err
	}
	vector := features.Vector(in, history)
	modelInputs := features.Map(vector)
	modelCtx, modelSpan := tracer().Start(ctx, "score.model")
	model, err := s.runModel(modelCtx, p, rs, vector)
	if model.version != nil {
		modelSpan.SetAttributes(attribute.String("model.version", *model.version), attribute.Float64("model.score", *model.score))
	}
	endSpan(modelSpan, err)
	if err != nil {
		return models.Decision{}, err
	}

	var result scoring.Result
	if blocked {
		result = scoring.Result{Outcome: scoring.OutcomeBlock, FiredRules: []scoring.FiredRule{scoring.BlocklistMatch(string(entry.Type))}}
	} else {
		rulesCtx, rulesSpan := tracer().Start(ctx, "score.rules")
		result, err = s.evaluateRules(rulesCtx, p, rs, ruleSignals{modelScore: model.score, ip: ipReputation(ipIntel)})
		rulesSpan.SetAttributes(attribute.String("score.outcome", string(result.Outcome)), attribute.Int("score.points", result.Score))
		endSpan(rulesSpan, err)
		if err != nil {
			return models.Decision{}, err
		}
	}
	return models.Decision{
		RulesetVersion:      rs.Version,
		Result:              result,
		ModelInputs:         modelInputs,
		ModelScore:          model.score,
		ModelScoreUnblended: model.unblended,
		ModelVersion:        model.version,
		FeatureHistory:      history,
		IPIntel:             ipIntel,
	}, nil
}

// modelResult is the model's part of a decision. Its fields are nil when the
// model did not run.
type modelResult struct {
	score, unblended *float64
	version          *string
}

// runModel scores the purchase with the model. The ruleset's model mode
// decides whether the model runs; whether its score can change the outcome is
// decided in evaluateRules. Without a model the rules decide alone.
func (s *Service) runModel(ctx context.Context, p models.Purchase, rs scoring.Ruleset, vector []float64) (modelResult, error) {
	scoreWithModel := s.model != nil && (rs.Model.Mode == scoring.ModelShadow || rs.Model.Mode == scoring.ModelAssist)
	if !scoreWithModel {
		return modelResult{}, nil
	}
	final, own, err := s.modelScore(ctx, p, vector)
	if err != nil {
		return modelResult{}, err
	}
	version := s.model.Version()
	return modelResult{score: &final, unblended: &own, version: &version}, nil
}

// ruleSignals are the inputs of the rules that come from outside the
// purchase and its history.
type ruleSignals struct {
	// modelScore is nil when the model did not run.
	modelScore *float64
	ip         scoring.IPReputation
}

// evaluateRules runs the rules on a purchase that is not blocklisted. In
// assist mode the model score then adjusts the outcome.
func (s *Service) evaluateRules(ctx context.Context, p models.Purchase, rs scoring.Ruleset, signals ruleSignals) (scoring.Result, error) {
	in, err := s.ruleInput(ctx, p, rs)
	if err != nil {
		return scoring.Result{}, err
	}
	in.IPReputation = signals.ip
	result := scoring.Evaluate(in, rs)
	if signals.modelScore != nil && rs.Model.Mode == scoring.ModelAssist {
		result.Outcome = scoring.ApplyModel(result.Outcome, *signals.modelScore, rs.Model.Policy(s.policy))
	}
	return result, nil
}

// ruleInput builds the rule input. It reads only the purchase history that
// the enabled rules need.
func (s *Service) ruleInput(ctx context.Context, p models.Purchase, rs scoring.Ruleset) (scoring.Input, error) {
	cardUses, err := s.purchases.CardPurchasesLastHour(ctx, p.CardToken, p.EventTime)
	if err != nil {
		return scoring.Input{}, err
	}

	in := scoring.Input{
		AmountMinor:           p.AmountMinor,
		IPCountry:             p.IPCountry,
		AccountCreatedAt:      p.AccountCreatedAt,
		EventTime:             p.EventTime,
		CardPurchasesLastHour: cardUses,
		Verification:          p.Verification(),
	}
	if p.CardCountry != nil {
		in.CardCountry = *p.CardCountry
	}
	if p.DeviceID != nil && rs.Rules[scoring.RuleDeviceCardCount].Enabled {
		in.DeviceCardsLastHour, err = s.purchases.DeviceCardsLastHour(ctx, *p.DeviceID, p.CardToken, p.EventTime)
		if err != nil {
			return scoring.Input{}, err
		}
	}
	spike := rs.Rules[scoring.RuleNewDeviceSpike]
	if p.DeviceID != nil && spike.Enabled && p.AmountMinor >= spike.MinAmountMinor {
		in.TrustedHistory, err = s.purchases.TrustedHistory(ctx, p, spike.WindowDays)
		if err != nil {
			return scoring.Input{}, err
		}
	}
	if in.AccountHistory, err = s.accountHistory(ctx, p, rs); err != nil {
		return scoring.Input{}, err
	}
	return in, nil
}

// accountHistory reads the player's account events for the enabled rules
// that need them. It returns nil when no enabled rule does. Account events
// feed only the rules, never the model inputs.
func (s *Service) accountHistory(ctx context.Context, p models.Purchase, rs scoring.Ruleset) (*scoring.AccountHistory, error) {
	lookback := scoring.AccountEventLookback(rs)
	if lookback == 0 || s.accountEvents == nil {
		return nil, nil
	}
	h, err := s.accountEvents.History(ctx, p.PlayerID, p.EventTime.Add(-lookback), p.EventTime)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// modelScore returns the score the policy applies to, and the score saved as
// unblended: the main model's own score, which later purchases of the player
// average. Without blending both come from the model alone.
func (s *Service) modelScore(ctx context.Context, p models.Purchase, vector []float64) (float64, float64, error) {
	vector = vector[:s.modelFeatures]
	combined, isCombined := s.model.(combination)
	if !isCombined {
		own, err := s.model.Predict(vector)
		if err != nil {
			return 0, 0, err
		}
		if s.policy.PlayerBlendWeight == 0 {
			return own, own, nil
		}
		sum, count, err := s.purchases.PlayerEarlierModelScores(ctx, p.PlayerID, p.EventTime, p.TransactionID)
		if err != nil {
			return 0, 0, err
		}
		return s.policy.Blend(own, sum, count), own, nil
	}

	scores, err := combined.Scores(vector)
	if err != nil {
		return 0, 0, err
	}
	own := scores[0]
	if s.policy.PlayerBlendWeight > 0 {
		sum, count, err := s.purchases.PlayerEarlierModelScores(ctx, p.PlayerID, p.EventTime, p.TransactionID)
		if err != nil {
			return 0, 0, err
		}
		scores[0] = s.policy.Blend(own, sum, count)
	}
	return combined.Combine(scores), own, nil
}

func tracer() trace.Tracer {
	return otel.Tracer("anti-fraud/score")
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed")
	}
	span.End()
}
