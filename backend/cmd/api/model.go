package main

import (
	"log/slog"

	"github.com/nasirlinijat/anti-fraud/backend/internal/lgbm"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/ruleset"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
)

// useModel loads the fraud model and its policy and gives them to the score
// service and the ruleset service. Without a model path the rules decide alone.
// It exits when the model cannot be loaded or used.
func useModel(modelPath string, service *scoresvc.Service, rulesets *ruleset.Service) *transport.LoadedModel {
	if modelPath == "" {
		slog.Info("MODEL_PATH is not set, so scoring uses rules only")
		return nil
	}
	model, err := lgbm.LoadScorer(modelPath)
	if err != nil {
		fatal("load fraud model", err)
	}
	policy, evaluation, err := lgbm.LoadReport(lgbm.ReportPath(modelPath), model.Version())
	if err != nil {
		fatal("load fraud model policy", err)
	}
	if err := service.UseModel(model, policy); err != nil {
		fatal("use fraud model "+model.Version(), err)
	}
	rulesets.UseModelThresholds(policy)
	slog.Info("fraud model loaded",
		"model_version", model.Version(), "trees", model.Trees(), "features", len(model.Features()),
		"flag_at", policy.FlagAt, "clear_below", policy.ClearBelow)
	return &transport.LoadedModel{Version: model.Version(), Policy: policy, Evaluation: evaluation}
}
