package transport

import (
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

type cardVelocityResponse struct {
	Count            int                            `json:"count"`
	Limit            int64                          `json:"limit"`
	WindowStart      time.Time                      `json:"window_start"`
	WindowEnd        time.Time                      `json:"window_end"`
	RecordsAvailable bool                           `json:"records_available"`
	ContextTruncated bool                           `json:"context_truncated"`
	Purchases        []cardVelocityPurchaseResponse `json:"purchases"`
}

type cardVelocityPurchaseResponse struct {
	TransactionID         string    `json:"transaction_id"`
	EventTime             time.Time `json:"event_time"`
	AmountMinor           string    `json:"amount_minor"`
	Contributed           bool      `json:"contributed"`
	RecordedAfterDecision bool      `json:"recorded_after_decision"`
}

func newCardVelocityResponse(evidence *models.CardVelocityEvidence) *cardVelocityResponse {
	if evidence == nil {
		return nil
	}
	purchases := make([]cardVelocityPurchaseResponse, 0, len(evidence.Purchases))
	for _, p := range evidence.Purchases {
		purchases = append(purchases, cardVelocityPurchaseResponse{
			TransactionID: p.TransactionID, EventTime: p.EventTime, AmountMinor: strconv.FormatInt(p.AmountMinor, 10),
			Contributed: p.Contributed, RecordedAfterDecision: p.RecordedAfterDecision,
		})
	}
	return &cardVelocityResponse{
		Count: evidence.Snapshot.Count, Limit: evidence.Snapshot.Limit,
		WindowStart: evidence.Snapshot.WindowStart, WindowEnd: evidence.Snapshot.WindowEnd,
		RecordsAvailable: evidence.RecordsAvailable, ContextTruncated: evidence.ContextTruncated, Purchases: purchases,
	}
}
