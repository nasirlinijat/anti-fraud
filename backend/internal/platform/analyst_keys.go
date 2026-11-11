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
type AnalystKeys struct{ namedKeys }

// OperatorKeys authenticates operators who change fraud configuration.
type OperatorKeys struct{ namedKeys }

type namedKeys struct {
	entries []namedCredential
}

type namedCredential struct {
	id     string
	digest [sha256.Size]byte
}

// keyVariable names an environment variable that holds named keys, and the
// field that holds the ID in each of its entries.
type keyVariable struct {
	name    string
	idField string
}

// ParseAnalystKeys reads the value of REVIEW_ANALYST_KEYS. Every key must
// differ from the service key and from the other analyst keys.
func ParseAnalystKeys(value, serviceKey string) (AnalystKeys, error) {
	keys, err := parseNamedKeys(value, keyVariable{"REVIEW_ANALYST_KEYS", "analyst_id"}, serviceKey, namedKeys{})
	return AnalystKeys{keys}, err
}

// ParseOperatorKeys reads the value of OPERATOR_API_KEYS. Every key must
// differ from the service key, the analyst keys, and the other operator keys.
func ParseOperatorKeys(value, serviceKey string, analysts AnalystKeys) (OperatorKeys, error) {
	keys, err := parseNamedKeys(value, keyVariable{"OPERATOR_API_KEYS", "operator_id"}, serviceKey, analysts.namedKeys)
	return OperatorKeys{keys}, err
}

// parseNamedKeys reads a JSON array of ID and key entries. An empty value
// gives no keys. other holds keys that these must not repeat.
func parseNamedKeys(value string, v keyVariable, serviceKey string, other namedKeys) (namedKeys, error) {
	variable, idField := v.name, v.idField
	if strings.TrimSpace(value) == "" {
		return namedKeys{}, nil
	}
	var entries []map[string]string
	dec := json.NewDecoder(strings.NewReader(value))
	if err := dec.Decode(&entries); err != nil || entries == nil || dec.Decode(new(any)) != io.EOF {
		return namedKeys{}, fmt.Errorf("%s must be a JSON array of %s and key entries", variable, idField)
	}

	var keys namedKeys
	ids := make(map[string]bool, len(entries))
	digests := make(map[[sha256.Size]byte]bool, len(entries)+len(other.entries)+1)
	digests[sha256.Sum256([]byte(serviceKey))] = true
	for _, entry := range other.entries {
		digests[entry.digest] = true
	}
	for i, entry := range entries {
		id, key := entry[idField], entry["key"]
		if len(entry) != 2 || id == "" || strings.TrimSpace(id) != id || strings.ContainsFunc(id, unicode.IsControl) {
			return namedKeys{}, fmt.Errorf("%s entry %d must contain a valid %s and key", variable, i+1, idField)
		}
		if key == "" || strings.ContainsFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) {
			return namedKeys{}, fmt.Errorf("%s entry %d needs a nonempty ASCII key without spaces", variable, i+1)
		}
		digest := sha256.Sum256([]byte(key))
		if ids[id] || digests[digest] {
			return namedKeys{}, fmt.Errorf("%s entry %d must have a unique ID and a distinct key", variable, i+1)
		}
		ids[id] = true
		digests[digest] = true
		keys.entries = append(keys.entries, namedCredential{id: id, digest: digest})
	}
	return keys, nil
}

// Configured reports whether at least one key is set.
func (keys namedKeys) Configured() bool {
	return len(keys.entries) > 0
}

// Analyst returns the authenticated analyst ID, or the empty string.
func (keys AnalystKeys) Analyst(key string) string {
	return keys.identity(key)
}

func (keys namedKeys) identity(key string) string {
	digest := sha256.Sum256([]byte(key))
	var id string
	for _, entry := range keys.entries {
		if subtle.ConstantTimeCompare(digest[:], entry.digest[:]) == 1 {
			id = entry.id
		}
	}
	return id
}
