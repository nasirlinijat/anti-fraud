package main

import (
	"context"
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
	if err := d.request(ctx, http.MethodGet, "/rulesets/active", d.serviceKey, nil, http.StatusOK); err != nil {
		return fmt.Errorf("check service access: %w", err)
	}
	if err := d.request(ctx, http.MethodGet, "/reviews", d.analystKey, nil, http.StatusOK); err != nil {
		return fmt.Errorf("check SEED_ANALYST_KEY: %w", err)
	}
	return nil
}

func (d demo) resolveReviews(ctx context.Context, reviews []labels.DemoReview) (int, error) {
	for i, review := range reviews[min(len(reviews), openCount):] {
		body := `{"decision":"approve"}`
		if review.Fraud {
			body = `{"decision":"reject"}`
		}
		path := fmt.Sprintf("/reviews/%d/decision", review.PurchaseID)
		if err := d.request(ctx, http.MethodPost, path, d.analystKey, strings.NewReader(body), http.StatusCreated); err != nil {
			return i, fmt.Errorf("resolve demo review: %w", err)
		}
	}
	return max(len(reviews)-openCount, 0), nil
}

func (d demo) request(ctx context.Context, method, path, key string, body io.Reader, want int) error {
	req, err := http.NewRequestWithContext(ctx, method, d.reset.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", key)
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
