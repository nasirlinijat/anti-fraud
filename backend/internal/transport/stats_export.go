package transport

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/stats"
)

var exportHeader = []string{
	"decided_at", "transaction_id", "player_id", "amount_usd", "outcome", "rule_score", "fired_rules",
	"model_score", "model_version", "ruleset_version", "review", "reviewed_by", "reviewed_at",
}

func (h *StatsHandler) export(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = stats.DefaultPeriod
	}
	now := h.now().UTC().Truncate(time.Microsecond)
	window, err := h.service.Window(period, now)
	if err != nil {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{
			Error:  "invalid request",
			Fields: map[string]string{"period": err.Error()},
		})
		return
	}
	total, err := h.service.CountDecisions(r.Context(), window)
	if err != nil {
		platform.WriteServerError(r.Context(), w, "count exported decisions", err, "period", period)
		return
	}

	rows := min(total, stats.ExportLimit)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="decisions-%s-%s.csv"`, period, now.Format("20060102T1504Z")))
	w.Header().Set("X-Export-Rows", strconv.FormatInt(rows, 10))
	w.Header().Set("X-Export-Truncated", strconv.FormatBool(total > stats.ExportLimit))
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	out := csv.NewWriter(w)
	_ = out.Write(exportHeader)
	err = h.service.Export(r.Context(), window, func(d models.DecisionExport) error {
		return out.Write(exportRecord(d))
	})
	out.Flush()
	if err == nil {
		err = out.Error()
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "export decisions stopped", "period", period, "error", err)
	}
}

func exportRecord(d models.DecisionExport) []string {
	modelScore, modelVersion, reviewedBy, reviewedAt := "", "", "", ""
	if d.ModelScore != nil {
		modelScore = strconv.FormatFloat(*d.ModelScore, 'f', 4, 64)
	}
	if d.ModelVersion != nil {
		modelVersion = *d.ModelVersion
	}
	if d.ReviewedBy != nil {
		reviewedBy = *d.ReviewedBy
	}
	if d.ReviewedAt != nil {
		reviewedAt = d.ReviewedAt.UTC().Format(time.RFC3339)
	}
	return []string{
		d.DecidedAt.UTC().Format(time.RFC3339),
		safeCell(d.TransactionID),
		safeCell(d.PlayerID),
		fmt.Sprintf("%d.%02d", d.AmountMinor/100, d.AmountMinor%100),
		d.Outcome,
		strconv.Itoa(d.RuleScore),
		d.FiredRules,
		modelScore,
		safeCell(modelVersion),
		strconv.Itoa(d.RulesetVersion),
		d.Review,
		safeCell(reviewedBy),
		reviewedAt,
	}
}

func safeCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
