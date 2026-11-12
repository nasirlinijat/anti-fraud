package platform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const testWebhookSecret = "webhook-secret-of-at-least-32-bytes"

func signedHeader(secret string, signedAt time.Time, body []byte) http.Header {
	h := http.Header{}
	h.Set(SignatureTimestampHeader, strconv.FormatInt(signedAt.Unix(), 10))
	h.Set(SignatureHeader, SignWebhook(secret, signedAt, body))
	return h
}

// signedTimestamp signs the body with the timestamp header value exactly as
// given, so only the timestamp format can make the check fail.
func signedTimestamp(timestamp string, body []byte) http.Header {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	h := http.Header{}
	h.Set(SignatureTimestampHeader, timestamp)
	h.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return h
}

func TestSignWebhookMatchesAKnownSignature(t *testing.T) {
	signedAt := time.Unix(1700000000, 0)

	got := SignWebhook("secret", signedAt, []byte(`{"a":1}`))

	// HMAC-SHA256 with key "secret" of the message `1700000000.{"a":1}`.
	want := "sha256=49f24e537407743fa4a0242bb63b94b9a47ee99cbbe071ccd8a22550ae411686"
	if got != want {
		t.Fatalf("SignWebhook = %q, want %q", got, want)
	}
}

func TestVerifyWebhookAcceptsAValidSignature(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	body := []byte(`{"chargeback_id":"cb-1"}`)
	for name, signedAt := range map[string]time.Time{
		"now":                  now,
		"at the past limit":    now.Add(-SignatureTolerance),
		"at the future limit":  now.Add(SignatureTolerance),
		"slightly in the past": now.Add(-time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			if err := VerifyWebhook(testWebhookSecret, signedHeader(testWebhookSecret, signedAt, body), body, now); err != nil {
				t.Fatalf("VerifyWebhook: %v", err)
			}
		})
	}
}

func TestVerifyWebhookRejectsInvalidSignatures(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	body := []byte(`{"chargeback_id":"cb-1"}`)
	valid := func() http.Header { return signedHeader(testWebhookSecret, now, body) }
	tests := []struct {
		name   string
		secret string
		header func() http.Header
		body   []byte
	}{
		{"wrong secret", testWebhookSecret, func() http.Header { return signedHeader("another-secret-of-at-least-32-bytes", now, body) }, body},
		{"changed body", testWebhookSecret, valid, []byte(`{"chargeback_id":"cb-2"}`)},
		{"stale timestamp", testWebhookSecret, func() http.Header {
			return signedHeader(testWebhookSecret, now.Add(-SignatureTolerance-time.Second), body)
		}, body},
		{"timestamp too far ahead", testWebhookSecret, func() http.Header {
			return signedHeader(testWebhookSecret, now.Add(SignatureTolerance+time.Second), body)
		}, body},
		{"missing signature", testWebhookSecret, func() http.Header { h := valid(); h.Del(SignatureHeader); return h }, body},
		{"missing timestamp", testWebhookSecret, func() http.Header { h := valid(); h.Del(SignatureTimestampHeader); return h }, body},
		{"timestamp not signed with the body", testWebhookSecret, func() http.Header {
			h := valid()
			h.Set(SignatureTimestampHeader, strconv.FormatInt(now.Unix()+1, 10))
			return h
		}, body},
		{"timestamp with a plus sign", testWebhookSecret, func() http.Header { return signedTimestamp("+"+strconv.FormatInt(now.Unix(), 10), body) }, body},
		{"timestamp with a minus sign", testWebhookSecret, func() http.Header { return signedTimestamp("-0", body) }, body},
		{"timestamp with spaces", testWebhookSecret, func() http.Header { return signedTimestamp(" "+strconv.FormatInt(now.Unix(), 10), body) }, body},
		{"timestamp not a number", testWebhookSecret, func() http.Header { h := valid(); h.Set(SignatureTimestampHeader, "soon"); return h }, body},
		{"no sha256 prefix", testWebhookSecret, func() http.Header {
			h := valid()
			h.Set(SignatureHeader, h.Get(SignatureHeader)[len("sha256="):])
			return h
		}, body},
		{"not hex", testWebhookSecret, func() http.Header { h := valid(); h.Set(SignatureHeader, "sha256=zz"); return h }, body},
		{"two signatures", testWebhookSecret, func() http.Header { h := valid(); h.Add(SignatureHeader, h.Get(SignatureHeader)); return h }, body},
		{"no secret configured", "", func() http.Header { return signedHeader("", now, body) }, body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWebhook(tt.secret, tt.header(), tt.body, now)

			if !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("err = %v, want ErrInvalidSignature", err)
			}
		})
	}
}
