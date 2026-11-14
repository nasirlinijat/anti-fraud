package models

// ModelEvaluation is a training report's ranking metrics on a named dataset.
type ModelEvaluation struct {
	Dataset string  `json:"dataset"`
	ROCAUC  float64 `json:"roc_auc"`
	PRAUC   float64 `json:"pr_auc"`
}

type ModelReason struct {
	Feature string
	Value   float64
	Effect  float64
}
