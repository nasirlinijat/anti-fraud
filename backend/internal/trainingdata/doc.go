// Package trainingdata exports the model inputs saved with each decision, so
// the fraud model can be trained on the service's own traffic. Fraud labels
// are never read here; training joins them from a file kept outside the
// service.
package trainingdata
