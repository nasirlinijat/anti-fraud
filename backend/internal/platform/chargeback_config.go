package platform

import (
	"errors"
	"os"
	"strings"
)

// minWebhookSecretBytes keeps the shared secret long enough that its HMAC
// cannot be guessed by trying values against the endpoint.
const minWebhookSecretBytes = 32

// loadChargebackWebhookSecret reads the optional CHARGEBACK_WEBHOOK_SECRET.
// The secret must differ from the service key, so no
// client of the other endpoints can sign chargebacks.
func loadChargebackWebhookSecret(apiKey string) (string, error) {
	secret := os.Getenv("CHARGEBACK_WEBHOOK_SECRET")
	switch {
	case secret == "":
		return "", nil
	case strings.IndexFunc(secret, isSpaceOrControl) >= 0:
		return "", errors.New("CHARGEBACK_WEBHOOK_SECRET must not contain whitespace or control characters")
	case len(secret) < minWebhookSecretBytes:
		return "", errors.New("CHARGEBACK_WEBHOOK_SECRET must be at least 32 bytes")
	case secret == apiKey:
		return "", errors.New("CHARGEBACK_WEBHOOK_SECRET must differ from API_KEY")
	}
	return secret, nil
}
