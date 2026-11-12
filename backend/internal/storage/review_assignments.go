package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

// AssignmentReviewersSQL lists every active reviewer, and every other account
// that still holds an open claim, so that no claim is left out of the view.
// Decisions are counted by username, the analyst ID that sessions save.
const AssignmentReviewersSQL = `
SELECT a.username, a.role, a.disabled_at IS NOT NULL AS disabled,
       (SELECT count(*) FROM review_resolutions r WHERE r.analyst_id = a.username) AS decided
FROM accounts a
WHERE (a.role = 'reviewer' AND a.disabled_at IS NULL)
   OR EXISTS (
       SELECT 1 FROM review_claims c
       WHERE c.account_id = a.id
         AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = c.purchase_id)
   )
ORDER BY a.username`

// OpenClaimsSQL lists the claims whose purchase has not been decided yet,
// oldest claim first for each account.
const OpenClaimsSQL = `
SELECT a.username, c.purchase_id, p.amount_minor, d.rule_score, d.model_score, c.claimed_at
FROM review_claims c
JOIN accounts a ON a.id = c.account_id
JOIN decisions d ON d.purchase_id = c.purchase_id
JOIN purchases p ON p.id = c.purchase_id
WHERE NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = c.purchase_id)
ORDER BY a.username, c.claimed_at, c.purchase_id`

// Assignments returns each reviewer with their open claims and the number of
// purchases they decided, ordered by username.
func (s *ReviewStore) Assignments(ctx context.Context) ([]models.ReviewerWork, error) {
	reviewers, err := s.assignmentReviewers(ctx)
	if err != nil {
		return nil, err
	}
	claims, err := s.openClaims(ctx)
	if err != nil {
		return nil, err
	}
	for i := range reviewers {
		reviewers[i].OpenClaims = claims[reviewers[i].Username]
		if reviewers[i].OpenClaims == nil {
			reviewers[i].OpenClaims = []models.OpenClaim{}
		}
	}
	return reviewers, nil
}

func (s *ReviewStore) assignmentReviewers(ctx context.Context) ([]models.ReviewerWork, error) {
	rows, err := s.conn.Query(ctx, AssignmentReviewersSQL)
	if err != nil {
		return nil, fmt.Errorf("list assignment reviewers: %w", err)
	}
	reviewers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.ReviewerWork, error) {
		var w models.ReviewerWork
		err := row.Scan(&w.Username, &w.Role, &w.Disabled, &w.Decided)
		return w, err
	})
	if err != nil {
		return nil, fmt.Errorf("list assignment reviewers: %w", err)
	}
	return reviewers, nil
}

// openClaims returns the open claims grouped by username.
func (s *ReviewStore) openClaims(ctx context.Context) (map[string][]models.OpenClaim, error) {
	rows, err := s.conn.Query(ctx, OpenClaimsSQL)
	if err != nil {
		return nil, fmt.Errorf("list open claims: %w", err)
	}
	defer rows.Close()
	claims := make(map[string][]models.OpenClaim)
	for rows.Next() {
		var username string
		var c models.OpenClaim
		if err := rows.Scan(&username, &c.PurchaseID, &c.AmountMinor, &c.RuleScore, &c.ModelScore, &c.ClaimedAt); err != nil {
			return nil, fmt.Errorf("list open claims: %w", err)
		}
		c.ClaimedAt = c.ClaimedAt.UTC()
		claims[username] = append(claims[username], c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list open claims: %w", err)
	}
	return claims, nil
}

// releaseClaim removes an open claim and reports what happened: released,
// already_resolved when the purchase was decided, or no_open_claim.
const releaseClaim = `
WITH released AS (
    DELETE FROM review_claims c
    WHERE c.purchase_id = $1
      AND NOT EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = c.purchase_id)
    RETURNING c.purchase_id
)
SELECT CASE
    WHEN EXISTS (SELECT 1 FROM released) THEN 'released'
    WHEN EXISTS (SELECT 1 FROM review_resolutions r WHERE r.purchase_id = $1) THEN 'already_resolved'
    ELSE 'no_open_claim'
END::text`

// ReleaseClaim returns a claimed purchase to the shared queue. A decided
// purchase keeps its claim as the record of who held it.
func (s *ReviewStore) ReleaseClaim(ctx context.Context, purchaseID int64) (models.ReleaseStatus, error) {
	var status models.ReleaseStatus
	if err := s.conn.QueryRow(ctx, releaseClaim, purchaseID).Scan(&status); err != nil {
		return "", fmt.Errorf("release claim of review %d: %w", purchaseID, err)
	}
	return status, nil
}
