package platform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// SignatureHeader carries the webhook signature: sha256=<hex HMAC-SHA256>.
	SignatureHeader = "X-Signature"
	// SignatureTimestampHeader carries the Unix time in seconds when the
	// webhook was signed.
	SignatureTimestampHeader = "X-Signature-Timestamp"
	// SignatureTolerance is how far the signing time may be from the
	// receiver's clock, in either direction, before a delivery counts as a
	// replay.
	SignatureTolerance = 5 * time.Minute

	signaturePrefix = "sha256="
)

// ErrInvalidSignature means the webhook signature or its timestamp is missing,
// malformed, wrong, or outside SignatureTolerance.
var ErrInvalidSignature = errors.New("missing, invalid or expired webhook signature")

// SignWebhook returns the X-Signature value for a body signed at the given
// time. The timestamp is signed together with the raw body, as
// "<unix seconds>.<body>", so a captured delivery cannot be replayed later
// with a fresh timestamp.
func SignWebhook(secret string, signedAt time.Time, body []byte) string {
	return signaturePrefix + hex.EncodeToString(webhookMAC(secret, strconv.FormatInt(signedAt.Unix(), 10), body))
}

// VerifyWebhook checks the signature headers of a webhook delivery against the
// raw body. The MAC is compared in constant time. It returns
// ErrInvalidSignature for every kind of failure, so a caller cannot learn
// which check failed.
func VerifyWebhook(secret string, header http.Header, body []byte, now time.Time) error {
	timestamps, signatures := header.Values(SignatureTimestampHeader), header.Values(SignatureHeader)
	if secret == "" || len(timestamps) != 1 || len(signatures) != 1 {
		return ErrInvalidSignature
	}
	if !withinTolerance(timestamps[0], now) {
		return ErrInvalidSignature
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signatures[0], signaturePrefix))
	if err != nil || !strings.HasPrefix(signatures[0], signaturePrefix) {
		return ErrInvalidSignature
	}
	if !hmac.Equal(got, webhookMAC(secret, timestamps[0], body)) {
		return ErrInvalidSignature
	}
	return nil
}

// withinTolerance reports whether the timestamp is plain decimal digits and
// within SignatureTolerance of now. Signs are rejected so that one signing
// time has exactly one header value.
func withinTolerance(timestamp string, now time.Time) bool {
	if timestamp == "" || strings.ContainsFunc(timestamp, func(r rune) bool { return r < '0' || r > '9' }) {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(seconds, 0))
	return age <= SignatureTolerance && age >= -SignatureTolerance
}

func webhookMAC(secret, timestamp string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return mac.Sum(nil)
}
