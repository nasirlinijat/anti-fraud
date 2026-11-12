package stats

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

// QualitySource supplies aggregate evaluations, isolated from ordinary stats.
type QualitySource interface {
	Quality(context.Context, string, string) (Quality, error)
	Runs(context.Context, string, string, int) (QualityRuns, error)
}

type qualityHandler struct{ source QualitySource }

// RegisterQualityRoutes protects optional quality reads with the service key.
func RegisterQualityRoutes(mux *http.ServeMux, source QualitySource, apiKey string) {
	h := qualityHandler{source: source}
	mux.Handle("GET /stats/quality", platform.RequireAPIKey(apiKey, http.HandlerFunc(h.quality)))
	mux.Handle("GET /stats/quality/runs", platform.RequireAPIKey(apiKey, http.HandlerFunc(h.runs)))
}

func (h qualityHandler) quality(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	source, fields := qualityParameters(query, "source", "run_id")
	run := query.Get("run_id")
	if strings.TrimSpace(run) == "" {
		fields["run_id"] = "is required"
	}
	if len(fields) > 0 {
		qualityInvalid(w, fields)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	q, err := h.source.Quality(ctx, source, run)
	if err != nil {
		qualityFailure(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, q)
}

func (h qualityHandler) runs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	source, fields := qualityParameters(query, "source", "after", "limit")
	limit := 20
	if raw, ok := query["limit"]; ok {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 {
			fields["limit"] = "must be between 1 and 100"
		} else {
			limit = n
		}
	}
	if len(fields) > 0 {
		qualityInvalid(w, fields)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := h.source.Runs(ctx, source, query.Get("after"), limit)
	if err != nil {
		qualityFailure(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, page)
}

func qualityParameters(query url.Values, allowed ...string) (string, map[string]string) {
	fields := map[string]string{}
	for key, values := range query {
		valid := false
		for _, name := range allowed {
			if key == name {
				valid = true
			}
		}
		if !valid {
			fields[key] = "is not supported"
		}
		if len(values) != 1 {
			fields[key] = "must be supplied once"
		}
	}
	source := query.Get("source")
	if source == "" && !query.Has("source") {
		source = "simulator"
	}
	if source != "simulator" && source != "ieee-cis" {
		fields["source"] = "must be simulator or ieee-cis"
	}
	return source, fields
}

func qualityInvalid(w http.ResponseWriter, fields map[string]string) {
	platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: fields})
}

func qualityFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrQualityRunNotFound) {
		platform.WriteError(w, http.StatusNotFound, "quality run not found")
		return
	}
	// Optional evaluation failures must not expose database credentials or answers.
	log.Print("quality reporting unavailable")
	platform.WriteError(w, http.StatusServiceUnavailable, "quality reports are unavailable")
}
