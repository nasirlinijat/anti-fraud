package mockbank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// Sender posts chargebacks to the webhook, signed with the shared secret.
type Sender struct {
	// BaseURL is the backend URL, without the /chargebacks path.
	BaseURL string
	Secret  string
	HTTP    *http.Client
	// Now is the signing clock; nil means time.Now.
	Now func() time.Time
}

// SendSummary counts the webhook's answers.
type SendSummary struct {
	// Recorded chargebacks were new to the service (202).
	Recorded int
	// Repeated chargebacks had been recorded by an earlier run (200).
	Repeated int
}

type webhookBody struct {
	ChargebackID  string    `json:"chargeback_id"`
	TransactionID string    `json:"transaction_id"`
	ReasonCode    string    `json:"reason_code"`
	AmountMinor   int64     `json:"amount_minor"`
	ReceivedAt    time.Time `json:"received_at"`
}

// Send posts the chargebacks one at a time, in order. It stops at the first
// delivery the webhook does not accept, because every planned chargeback is
// for a scored purchase and should be accepted.
func (s *Sender) Send(ctx context.Context, chargebacks []models.Chargeback) (SendSummary, error) {
	var sum SendSummary
	for _, c := range chargebacks {
		status, err := s.post(ctx, c)
		switch {
		case err != nil:
			return sum, fmt.Errorf("send chargeback %s: %w", c.ChargebackID, err)
		case status == http.StatusAccepted:
			sum.Recorded++
		case status == http.StatusOK:
			sum.Repeated++
		}
	}
	return sum, nil
}

func (s *Sender) post(ctx context.Context, c models.Chargeback) (int, error) {
	body, err := json.Marshal(webhookBody{
		ChargebackID: c.ChargebackID, TransactionID: c.TransactionID, ReasonCode: string(c.Reason),
		AmountMinor: c.AmountMinor, ReceivedAt: c.ReceivedAt.UTC(),
	})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.BaseURL, "/")+"/chargebacks", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	signedAt := s.now()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(platform.SignatureTimestampHeader, strconv.FormatInt(signedAt.Unix(), 10))
	req.Header.Set(platform.SignatureHeader, platform.SignWebhook(s.Secret, signedAt, body))
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("webhook answered %d: %s", resp.StatusCode, strings.TrimSpace(string(answer)))
	}
	return resp.StatusCode, nil
}

func (s *Sender) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}
