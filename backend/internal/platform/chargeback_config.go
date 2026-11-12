package platform

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"os"
	"strings"
)

// minWebhookSecretBytes keeps the shared secret long enough that its HMAC
// cannot be guessed by trying values against the endpoint.
const minWebhookSecretBytes = 32

// loadChargebackWebhookSecret reads the optional CHARGEBACK_WEBHOOK_SECRET.
// The secret must differ from the service, analyst and operator keys, so no
// client of the other endpoints can sign chargebacks.
func loadChargebackWebhookSecret(apiKey string, analysts AnalystKeys, operators OperatorKeys) (string, error) {
	secret := os.Getenv("CHARGEBACK_WEBHOOK_SECRET")
	switch {
	case secret == "":
		return "", nil
	case strings.IndexFunc(secret, isSpaceOrControl) >= 0:
		return "", errors.New("CHARGEBACK_WEBHOOK_SECRET must not contain whitespace or control characters")
	case len(secret) < minWebhookSecretBytes:
		return "", errors.New("CHARGEBACK_WEBHOOK_SECRET must be at least 32 bytes")
	case secret == apiKey || analysts.has(secret) || operators.has(secret):
		return "", errors.New("CHARGEBACK_WEBHOOK_SECRET must differ from API_KEY and every analyst and operator key")
	}
	return secret, nil
}

// has reports whether key is one of the keys.
func (k namedKeys) has(key string) bool {
	digest := sha256.Sum256([]byte(key))
	for _, entry := range k.entries {
		if subtle.ConstantTimeCompare(digest[:], entry.digest[:]) == 1 {
			return true
		}
	}
	return false
}
