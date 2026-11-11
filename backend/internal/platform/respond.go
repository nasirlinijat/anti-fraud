package platform

import (
	"encoding/json"
	"log"
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

// WriteError writes an error response with the given status code and message.
func WriteError(w http.ResponseWriter, code int, message string) {
	WriteJSON(w, code, ErrorResponse{Error: message})
}
