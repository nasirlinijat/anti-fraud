package chargeback_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chargebackReaders are the only source files under backend/ that may mention
// chargebacks: the webhook, its configuration, the training export and the
// mock bank. A path ending in / allows the whole directory.
var chargebackReaders = []string{
	"cmd/export-inputs/",
	"cmd/mock-bank/",
	"internal/mockbank/",
	"internal/models/chargeback.go",
	"internal/platform/chargeback_config.go",
	"internal/platform/config.go",
	"internal/platform/webhook_signature.go",
	"internal/services/chargeback/",
	"internal/storage/chargeback_store.go",
	"internal/trainingdata/",
	"internal/transport/chargeback_handler.go",
}

// routeWiring is the only chargeback code routes.go may hold: wiring the store,
// service and handler together.
var routeWiring = []string{
	`"github.com/nasirlinijat/anti-fraud/backend/internal/services/chargeback"`,
	`chargebacks := chargeback.NewService(storage.NewChargebackStore(pool))`,
	`transport.RegisterChargebackRoutes(mux, transport.NewChargebackHandler(chargebacks, cfg.ChargebackWebhookSecret))`,
}

// The runtime role must read chargebacks to answer repeated webhook
// deliveries, so database grants cannot keep them away from scoring. This
// check does: chargebacks are training labels, never model inputs, rule
// inputs or review evidence.
func TestChargebacksStayOutOfScoring(t *testing.T) {
	backend := filepath.Join("..", "..", "..")
	err := filepath.WalkDir(backend, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, err := filepath.Rel(backend, path)
		if err != nil {
			return err
		}
		checkChargebackMentions(t, filepath.ToSlash(rel), path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", backend, err)
	}
}

func checkChargebackMentions(t *testing.T, rel, path string) {
	t.Helper()
	if isChargebackReader(rel) {
		return
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(source), "\n") {
		if !strings.Contains(strings.ToLower(line), "chargeback") {
			continue
		}
		if rel == "cmd/api/routes.go" && isRouteWiring(line) {
			continue
		}
		t.Errorf("%s:%d mentions chargebacks; only the webhook, the export and the mock bank may", rel, i+1)
	}
}

func isChargebackReader(rel string) bool {
	for _, allowed := range chargebackReaders {
		if rel == allowed || strings.HasSuffix(allowed, "/") && strings.HasPrefix(rel, allowed) {
			return true
		}
	}
	return false
}

func isRouteWiring(line string) bool {
	for _, wiring := range routeWiring {
		if strings.TrimSpace(line) == wiring {
			return true
		}
	}
	return false
}
