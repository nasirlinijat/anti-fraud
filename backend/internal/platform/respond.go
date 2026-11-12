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

func WriteServerError(ctx context.Context, w http.ResponseWriter, msg string, err error, attrs ...any) {
	args := append(attrs, "error", err)
	if DatabaseUnavailable(err) {
		slog.WarnContext(ctx, msg+": database unavailable", args...)
		w.Header().Set("Retry-After", "1")
		WriteError(w, http.StatusServiceUnavailable, DatabaseUnavailableMessage)
		return
	}
	slog.ErrorContext(ctx, msg, args...)
	WriteError(w, http.StatusInternalServerError, "internal error")
}
