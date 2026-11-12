package labels

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/simulator"
)

var ErrPurchaseMismatch = errors.New("saved purchase does not match pending request")

// requestSnapshot follows scoring normalization: trimmed strings, absent empty
// optionals, unmapped IPs, UTC and PostgreSQL microsecond timestamp precision.
func requestSnapshot(req simulator.Request) []byte {
	req.TransactionID = strings.TrimSpace(req.TransactionID)
	req.PlayerID = strings.TrimSpace(req.PlayerID)
	req.CardToken = strings.TrimSpace(req.CardToken)
	v := reflect.ValueOf(&req).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Type() == reflect.TypeFor[*string]() && !f.IsNil() {
			s := strings.TrimSpace(f.Elem().String())
			if s == "" {
				f.SetZero()
			} else {
				f.Set(reflect.ValueOf(&s))
			}
		}
	}
	req.EventTime = req.EventTime.UTC().Truncate(time.Microsecond)
	if req.AccountCreatedAt != nil {
		t := req.AccountCreatedAt.UTC().Truncate(time.Microsecond)
		req.AccountCreatedAt = &t
	}
	if req.IPAddress != nil {
		if a, err := netip.ParseAddr(*req.IPAddress); err == nil {
			s := a.Unmap().String()
			req.IPAddress = &s
		}
	}
	b, _ := json.Marshal(req)
	return b
}
func verifyPurchase(ctx context.Context, c platform.DBTX, l Label) error {
	var matches bool
	err := c.QueryRow(ctx, `SELECT (to_jsonb(p)-'id'-'received_at') =
 (to_jsonb(jsonb_populate_record(NULL::purchases,$2::jsonb))-'id'-'received_at')
 FROM purchases p JOIN decisions d ON d.purchase_id=p.id WHERE p.transaction_id=$1`, l.TransactionID, l.RequestJSON).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return ErrPurchaseMismatch
	}
	return nil
}

// Reject records a definitive HTTP rejection without deleting its audit evidence.
func Reject(ctx context.Context, c platform.DBTX, id string, err error) bool {
	var response *simulator.HTTPError
	if !errors.As(err, &response) || response.Status < 400 || response.Status >= 500 || response.Status == 408 || response.Status == 429 {
		return false
	}
	_, _ = c.Exec(ctx, `UPDATE pending_purchase_labels SET rejection_reason='score request rejected' WHERE transaction_id=$1`, id)
	return true
}
