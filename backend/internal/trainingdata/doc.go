// Package trainingdata exports the model inputs saved with each decision, so
// the fraud model can be trained on the service's own traffic, and the labels
// a business really has for them: chargebacks reported by the processor.
// The private evaluation labels of simulator and replay runs are never read
// here; training joins whichever labels file it is given.
package trainingdata
