package transport

import (
	"context"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/scoring"
)

// LoadedModel describes the model available to scoring at startup.
type LoadedModel struct {
	Version    string
	Policy     scoring.ModelPolicy
	Evaluation *models.ModelEvaluation
}

type modelStatus struct {
	Loaded     bool                    `json:"loaded"`
	Version    *string                 `json:"version"`
	Mode       scoring.ModelMode       `json:"mode"`
	ClearBelow *float64                `json:"clear_below"`
	FlagAt     *float64                `json:"flag_at"`
	Evaluation *models.ModelEvaluation `json:"evaluation"`
}

// RegisterModelRoutes exposes the loaded model and its effective ruleset settings.
func RegisterModelRoutes(mux *http.ServeMux, active func(context.Context) (scoring.Ruleset, error), model *LoadedModel, access platform.Access) {
	mux.HandleFunc("GET /model", access.Review(func(w http.ResponseWriter, r *http.Request, _ platform.Actor) {
		ruleset, err := active(r.Context())
		if err != nil {
			platform.WriteServerError(r.Context(), w, "read active model settings", err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, describeModel(ruleset.Model, model))
	}))
}

func describeModel(settings scoring.ModelSettings, model *LoadedModel) modelStatus {
	status := modelStatus{Mode: settings.Mode}
	if model == nil {
		return status
	}
	policy := settings.Policy(model.Policy)
	status.Loaded = true
	status.Version = &model.Version
	status.ClearBelow = &policy.ClearBelow
	status.FlagAt = &policy.FlagAt
	status.Evaluation = model.Evaluation
	return status
}
