//go:build integration

package concurrency

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

func sendChargeback(body string) (response, error) {
	req, err := http.NewRequest(http.MethodPost, server.URL+"/chargebacks", strings.NewReader(body))
	if err != nil {
		return response{}, err
	}
	signedAt := time.Now()
	req.Header.Set(platform.SignatureTimestampHeader, strconv.FormatInt(signedAt.Unix(), 10))
	req.Header.Set(platform.SignatureHeader, platform.SignWebhook(webhookSecret, signedAt, []byte(body)))
	resp, err := server.Client().Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: string(b)}, err
}

// sendChargebacksTogether sends the body n times at once.
func sendChargebacksTogether(t *testing.T, n int, body string) []response {
	t.Helper()
	responses := make([]response, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			responses[i], errs[i] = sendChargeback(body)
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	return responses
}

func TestConcurrentIdenticalChargebacksRecordOne(t *testing.T) {
	const n = 20
	transactionID := unique("race-chargeback")
	if r, err := send(http.MethodPost, "/score", serviceKey, purchase(transactionID, 1999)); err != nil || r.status != http.StatusOK {
		t.Fatalf("score the purchase: %v %+v", err, r)
	}
	chargebackID := "cb-" + transactionID
	body := fmt.Sprintf(`{"chargeback_id":%q,"transaction_id":%q,"reason_code":"fraud_card_not_present","amount_minor":1999,"received_at":%q}`,
		chargebackID, transactionID, time.Now().UTC().Format(time.RFC3339))

	responses := sendChargebacksTogether(t, n, body)

	recorded := 0
	for i, r := range responses {
		switch r.status {
		case http.StatusAccepted:
			recorded++
		case http.StatusOK:
		default:
			t.Fatalf("delivery %d: status %d, body %s", i, r.status, r.body)
		}
	}
	if recorded != 1 {
		t.Fatalf("%d deliveries recorded the chargeback, want exactly 1 and repeats for the rest", recorded)
	}
	if got := count(t, `SELECT count(*) FROM chargebacks WHERE chargeback_id = $1`, chargebackID); got != 1 {
		t.Fatalf("%d chargebacks saved, want 1", got)
	}
}
