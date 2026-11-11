package blocklist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const (
	maxBodyBytes   = 4 << 10
	maxValueRunes  = 200
	maxReasonRunes = 500
)

// Entries lists, adds and removes blocklist entries.
type Entries interface {
	List(ctx context.Context) ([]StoredEntry, error)
	Add(ctx context.Context, e Entry, operatorID string) (StoredEntry, error)
	Remove(ctx context.Context, id int64, operatorID string) error
}

// Handler serves the blocklist management endpoints.
type Handler struct {
	entries Entries
}

func NewHandler(entries Entries) *Handler {
	return &Handler{entries: entries}
}

// RegisterRoutes registers GET and POST /blocklist and DELETE /blocklist/{id}
// behind API key authentication.
func RegisterRoutes(mux *http.ServeMux, h *Handler, apiKey string, operators platform.OperatorKeys) {
	mux.Handle("GET /blocklist", platform.RequireAPIKey(apiKey, http.HandlerFunc(h.list)))
	mux.Handle("POST /blocklist", platform.RequireOperator(operators, h.add))
	mux.Handle("DELETE /blocklist/{id}", platform.RequireOperator(operators, h.remove))
}

type listResponse struct {
	Entries []StoredEntry `json:"entries"`
}

type addRequest struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.entries.List(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, listResponse{Entries: entries})
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request, operatorID string) {
	var req addRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeAddDecodeError(w, err)
		return
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		writeAddDecodeError(w, err)
		return
	}

	entry, fields := req.entry()
	if len(fields) > 0 {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: fields})
		return
	}

	saved, err := h.entries.Add(r.Context(), entry, operatorID)
	if errors.Is(err, ErrDuplicate) {
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error:  "this value is already on the blocklist",
			Fields: map[string]string{"value": "is already on the blocklist"},
		})
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, saved)
}

func writeAddDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		platform.WriteError(w, http.StatusRequestEntityTooLarge, "request body must not exceed 4096 bytes")
		return
	}
	platform.WriteError(w, http.StatusBadRequest, "request body must be one JSON object with type, value and reason")
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request, operatorID string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "the entry ID must be a positive integer")
		return
	}
	err = h.entries.Remove(r.Context(), id, operatorID)
	if errors.Is(err, ErrNotFound) {
		platform.WriteError(w, http.StatusNotFound, "no blocklist entry has this ID")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// entry validates the request. IP addresses are stored in the canonical form
// that the score endpoint compares with, so 203.000.113.9 is not accepted and
// ::ffff:203.0.113.9 becomes 203.0.113.9.
func (req addRequest) entry() (Entry, map[string]string) {
	fields := map[string]string{}
	e := Entry{
		Type:   Type(strings.TrimSpace(req.Type)),
		Value:  strings.TrimSpace(req.Value),
		Reason: strings.TrimSpace(req.Reason),
	}

	if !slices.Contains(Types, e.Type) {
		fields["type"] = "must be one of card, device, player, ip"
	}
	switch {
	case e.Value == "":
		fields["value"] = "is required"
	case utf8.RuneCountInString(e.Value) > maxValueRunes:
		fields["value"] = "must be at most 200 characters"
	case e.Type == TypeIP:
		addr, err := netip.ParseAddr(e.Value)
		if err != nil {
			fields["value"] = "must be an IPv4 or IPv6 address"
		} else {
			e.Value = addr.Unmap().String()
		}
	}
	switch {
	case e.Reason == "":
		fields["reason"] = "is required"
	case utf8.RuneCountInString(e.Reason) > maxReasonRunes:
		fields["reason"] = "must be at most 500 characters"
	}
	return e, fields
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("blocklist: %v", err)
	platform.WriteError(w, http.StatusInternalServerError, "internal error")
}
