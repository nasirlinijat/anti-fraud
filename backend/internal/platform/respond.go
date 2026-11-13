package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// ErrorResponse is the body of every error response. Fields maps request field
// names to what is wrong with them, when the error is about specific fields.
type ErrorResponse struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields,omitempty"`
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "error", err)
	}
}

// WriteError writes an error response with the given status code and message.
func WriteError(w http.ResponseWriter, code int, message string) {
	WriteJSON(w, code, ErrorResponse{Error: message})
}

const DatabaseUnavailableMessage = "the database is unavailable; retry later"

// WriteServerError logs err with LogServerError and answers 503 when the
// database is unavailable and 500 otherwise, without the error's details.
func WriteServerError(ctx context.Context, w http.ResponseWriter, msg string, err error, attrs ...any) {
	LogServerError(ctx, msg, err, attrs...)
	if DatabaseUnavailable(err) {
		w.Header().Set("Retry-After", "1")
		WriteError(w, http.StatusServiceUnavailable, DatabaseUnavailableMessage)
		return
	}
	WriteError(w, http.StatusInternalServerError, "internal error")
}

// LogServerError logs an error the client cannot fix, as a warning when the
// database is unavailable and as an error otherwise. It is for handlers that
// report the failure inside a successful response, such as one purchase of a
// batch.
func LogServerError(ctx context.Context, msg string, err error, attrs ...any) {
	args := append(attrs, "error", err)
	if DatabaseUnavailable(err) {
		slog.WarnContext(ctx, msg+": database unavailable", args...)
		return
	}
	slog.ErrorContext(ctx, msg, args...)
}
