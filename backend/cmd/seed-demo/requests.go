package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
)

func (d demo) preflight(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := labels.Preflight(ctx, pool); err != nil {
		return err
	}
	if err := d.checkAdmin(ctx); err != nil {
		return err
	}
	if err := d.request(ctx, http.MethodGet, "/rulesets/active", nil, http.StatusOK); err != nil {
		return err
	}
	return d.checkServiceKey(ctx)
}

func (d demo) checkServiceKey(ctx context.Context) error {
	// Validation rejects this body before scoring, so credential checks cannot save a purchase.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.reset.BaseURL+"/score", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", d.serviceKey)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("check API_KEY: HTTP %d", resp.StatusCode)
	}

	return nil
}

func (d demo) resolveReviews(ctx context.Context, reviews []labels.DemoReview) (int, error) {
	for i, review := range reviews[min(len(reviews), openCount):] {
		body := `{"decision":"approve"}`
		if review.Fraud {
			body = `{"decision":"reject"}`
		}
		claimPath := fmt.Sprintf("/reviews/%d/claim", review.PurchaseID)
		if err := d.request(ctx, http.MethodPost, claimPath, nil, http.StatusCreated); err != nil {
			return i, fmt.Errorf("claim demo review: %w", err)
		}
		path := fmt.Sprintf("/reviews/%d/decision", review.PurchaseID)
		if err := d.request(ctx, http.MethodPost, path, strings.NewReader(body), http.StatusCreated); err != nil {
			return i, fmt.Errorf("resolve demo review: %w", err)
		}
	}
	return max(len(reviews)-openCount, 0), nil
}

func (d demo) request(ctx context.Context, method, path string, body io.Reader, want int) error {
	req, err := http.NewRequestWithContext(ctx, method, d.reset.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.reset.SessionToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return err
}

func (d demo) checkAdmin(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.reset.BaseURL+"/auth/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.reset.SessionToken)
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var account struct {
		Role string `json:"role"`
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("check RESET_SESSION_TOKEN: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return err
	}
	if account.Role != "admin" {
		return fmt.Errorf("RESET_SESSION_TOKEN requires an admin session")
	}
	return nil
}
