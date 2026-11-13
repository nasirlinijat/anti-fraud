package simulation

import "errors"

var (
	ErrShuttingDown  = errors.New("simulation service is shutting down")
	ErrRequestReused = errors.New("request_id was used with different settings")
)
