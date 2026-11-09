package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Result is the score API response for one purchase.
type Result struct {
	TransactionID string `json:"transaction_id"`
	Outcome       string `json:"outcome"`
	Score         int    `json:"score"`
	FiredRules    []struct {
		Name   string `json:"name"`
		Points int    `json:"points"`
	} `json:"fired_rules"`
	RulesetVersion int `json:"ruleset_version"`
}

// Scorer sends one score request.
type Scorer interface {
	Score(ctx context.Context, req Request) (Result, error)
}

// Client calls POST /score on a running backend.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// Score sends req and returns the decision. A response other than 200 is an
// error that includes the response body.
func (c *Client) Score(ctx context.Context, req Request) (Result, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/score", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.APIKey)

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("score %s: status %d: %s", req.TransactionID, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return Result{}, fmt.Errorf("decode response: %w", err)
	}
	return res, nil
}

// Summary counts the outcomes of a run by pattern.
type Summary struct {
	Sent     int
	Failed   int
	Outcomes map[Pattern]map[string]int
}

// MaxFailuresInRow is how many purchases in a row may fail before Run gives
// up, for example because the URL or API key is wrong.
const MaxFailuresInRow = 20

// Run sends the purchases in order, at most rate per second, or as fast as
// possible when rate is 0. A failed purchase is reported to onError and the
// run continues, unless MaxFailuresInRow purchases in a row fail. Run stops
// early when ctx is cancelled.
func Run(ctx context.Context, s Scorer, purchases []Purchase, rate float64, onError func(error)) (Summary, error) {
	sum := Summary{Outcomes: map[Pattern]map[string]int{}}
	if rate < 0 {
		return sum, fmt.Errorf("rate must not be negative, got %v", rate)
	}

	var tick <-chan time.Time
	if rate > 0 {
		ticker := time.NewTicker(time.Duration(float64(time.Second) / rate))
		defer ticker.Stop()
		tick = ticker.C
	}

	failuresInRow := 0
	for i, p := range purchases {
		if i > 0 && tick != nil {
			select {
			case <-ctx.Done():
				return sum, ctx.Err()
			case <-tick:
			}
		}
		if err := ctx.Err(); err != nil {
			return sum, err
		}

		res, err := s.Score(ctx, p.Request)
		if err != nil {
			sum.Failed++
			if onError != nil {
				onError(err)
			}
			failuresInRow++
			if failuresInRow == MaxFailuresInRow {
				return sum, fmt.Errorf("%d purchases in a row failed, last error: %w", failuresInRow, err)
			}
			continue
		}
		failuresInRow = 0
		sum.Sent++
		if sum.Outcomes[p.Pattern] == nil {
			sum.Outcomes[p.Pattern] = map[string]int{}
		}
		sum.Outcomes[p.Pattern][res.Outcome]++
	}
	return sum, nil
}
