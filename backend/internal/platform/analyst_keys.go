package platform

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// AnalystKeys authenticates the individual credentials for the review console.
type AnalystKeys struct {
	entries []analystCredential
}

type analystCredential struct {
	id     string
	digest [sha256.Size]byte
}

// ParseAnalystKeys loads optional reviewer credentials without retaining their raw keys.
func ParseAnalystKeys(value, serviceKey string) (AnalystKeys, error) {
	if strings.TrimSpace(value) == "" {
		return AnalystKeys{}, nil
	}

	var entries []struct {
		AnalystID string `json:"analyst_id"`
		Key       string `json:"key"`
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil || entries == nil || dec.Decode(new(any)) != io.EOF {
		return AnalystKeys{}, fmt.Errorf("REVIEW_ANALYST_KEYS must be a JSON array of analyst_id and key entries")
	}

	var keys AnalystKeys
	ids := make(map[string]bool, len(entries))
	digests := make(map[[sha256.Size]byte]bool, len(entries))
	serviceDigest := sha256.Sum256([]byte(serviceKey))
	for i, entry := range entries {
		if entry.AnalystID == "" || strings.TrimSpace(entry.AnalystID) != entry.AnalystID ||
			strings.ContainsFunc(entry.AnalystID, unicode.IsControl) {
			return AnalystKeys{}, fmt.Errorf("REVIEW_ANALYST_KEYS entry %d has an invalid analyst_id", i+1)
		}
		if entry.Key == "" || strings.ContainsFunc(entry.Key, func(r rune) bool { return r <= ' ' || r > '~' }) {
			return AnalystKeys{}, fmt.Errorf("REVIEW_ANALYST_KEYS entry %d needs a nonempty ASCII key without spaces", i+1)
		}
		digest := sha256.Sum256([]byte(entry.Key))
		if ids[entry.AnalystID] || digests[digest] || digest == serviceDigest {
			return AnalystKeys{}, fmt.Errorf("REVIEW_ANALYST_KEYS entry %d must have a unique analyst ID and a key distinct from all other keys and API_KEY", i+1)
		}
		ids[entry.AnalystID] = true
		digests[digest] = true
		keys.entries = append(keys.entries, analystCredential{id: entry.AnalystID, digest: digest})
	}
	return keys, nil
}

func (keys AnalystKeys) Configured() bool {
	return len(keys.entries) > 0
}

// Analyst returns the ID associated with a key, or the empty string if it is invalid.
func (keys AnalystKeys) Analyst(key string) string {
	digest := sha256.Sum256([]byte(key))
	var id string
	for _, entry := range keys.entries {
		if subtle.ConstantTimeCompare(digest[:], entry.digest[:]) == 1 {
			id = entry.id
		}
	}
	return id
}
