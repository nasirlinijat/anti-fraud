package transport

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

var exportNow = time.Date(2026, 11, 14, 13, 5, 0, 0, time.UTC)

func newMockExportMux(t *testing.T) (*http.ServeMux, *mocks.MockStatsService) {
	t.Helper()
	service := mocks.NewMockStatsService(gomock.NewController(t))
	handler := NewStatsHandler(service)
	handler.now = func() time.Time { return exportNow }
	mux := http.NewServeMux()
	RegisterStatsRoutes(mux, handler, platform.Access{Sessions: consoleTestSessions})
	return mux, service
}

func TestExportWritesACSVOfTheDecisions(t *testing.T) {
	mux, service := newMockExportMux(t)
	window := models.StatsWindow{Since: exportNow.Add(-24 * time.Hour), Until: exportNow}
	score, version, analyst := 0.81234, "lgbm-game-v5", "reviewer-one"
	resolved := exportNow.Add(-time.Minute)
	service.EXPECT().Window("24h", exportNow).Return(window, nil)
	service.EXPECT().CountDecisions(gomock.Any(), window).Return(int64(2), nil)
	service.EXPECT().Export(gomock.Any(), window, gomock.Any()).DoAndReturn(
		func(_ any, _ models.StatsWindow, emit func(models.DecisionExport) error) error {
			if err := emit(models.DecisionExport{DecidedAt: exportNow.Add(-time.Minute), TransactionID: "=HYPERLINK(\"x\")", PlayerID: "player-1",
				AmountMinor: 72609, Outcome: "review", RuleScore: 50, FiredRules: "high_amount;card_velocity", ModelScore: &score,
				ModelVersion: &version, RulesetVersion: 3, Review: "approved", ReviewedBy: &analyst, ReviewedAt: &resolved}); err != nil {
				return err
			}
			return emit(models.DecisionExport{DecidedAt: exportNow.Add(-time.Hour), TransactionID: "txn-2", PlayerID: "player-2",
				AmountMinor: 5, Outcome: "allow", RulesetVersion: 3})
		})

	rec := send(mux, http.MethodGet, "/stats/export?period=24h", mockAdminToken, "")
	wantResponse(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("content type %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="decisions-24h-20261114T1305Z.csv"` {
		t.Fatalf("content disposition %q", got)
	}
	if rec.Header().Get("X-Export-Rows") != "2" || rec.Header().Get("X-Export-Truncated") != "false" {
		t.Fatalf("export headers %v", rec.Header())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Fatal("the file does not start with a UTF-8 byte order mark for Excel")
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		exportHeader,
		{"2026-11-14T13:04:00Z", "'=HYPERLINK(\"x\")", "player-1", "726.09", "review", "50", "high_amount;card_velocity",
			"0.8123", "lgbm-game-v5", "3", "approved", "reviewer-one", "2026-11-14T13:04:00Z"},
		{"2026-11-14T12:05:00Z", "txn-2", "player-2", "0.05", "allow", "0", "", "", "", "3", "", "", ""},
	}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d: %q", len(records), len(want), records)
	}
	for i := range want {
		if strings.Join(records[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("record %d:\n got %q\nwant %q", i, records[i], want[i])
		}
	}
}

func TestExportSaysWhenTheFileIsCutOff(t *testing.T) {
	mux, service := newMockExportMux(t)
	window := models.StatsWindow{Since: time.Unix(0, 0).UTC(), Until: exportNow}
	service.EXPECT().Window("all", exportNow).Return(window, nil)
	service.EXPECT().CountDecisions(gomock.Any(), window).Return(int64(stats.ExportLimit+1), nil)
	service.EXPECT().Export(gomock.Any(), window, gomock.Any()).Return(nil)

	rec := send(mux, http.MethodGet, "/stats/export?period=all", mockAdminToken, "")
	wantResponse(t, rec, http.StatusOK)
	if rec.Header().Get("X-Export-Rows") != "50000" || rec.Header().Get("X-Export-Truncated") != "true" {
		t.Fatalf("export headers %v", rec.Header())
	}
}

func TestExportRejectsBadPeriodsAndReviewers(t *testing.T) {
	mux, service := newMockExportMux(t)
	service.EXPECT().Window("2d", exportNow).Return(models.StatsWindow{}, stats.ErrUnknownPeriod)
	wantResponse(t, send(mux, http.MethodGet, "/stats/export?period=2d", mockAdminToken, ""), http.StatusBadRequest, `"period":`)
	wantResponse(t, send(mux, http.MethodGet, "/stats/export", mockReviewerToken, ""), http.StatusForbidden)
	wantResponse(t, send(mux, http.MethodGet, "/stats/export", "", ""), http.StatusUnauthorized)
}

func TestSafeCellStopsSpreadsheetFormulas(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+cmd": "'+cmd", "-2": "'-2", "@SUM(A1)": "'@SUM(A1)", "order-1": "order-1", "": ""} {
		if got := safeCell(in); got != want {
			t.Errorf("safeCell(%q) = %q, want %q", in, got, want)
		}
	}
}
