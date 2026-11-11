package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
)

const maxBlocklistBodyBytes = 4 << 10

type BlocklistService interface {
	List(ctx context.Context) ([]models.StoredBlocklistEntry, error)
	Add(ctx context.Context, e models.BlocklistEntry, operatorID string) (models.StoredBlocklistEntry, error)
	Remove(ctx context.Context, id int64, operatorID string) error
}

// BlocklistHandler serves the blocklist management endpoints.
type BlocklistHandler struct {
	service BlocklistService
}

func NewBlocklistHandler(service BlocklistService) *BlocklistHandler {
	return &BlocklistHandler{service: service}
}

// RegisterBlocklistRoutes registers GET and POST /blocklist and DELETE /blocklist/{id}
// behind API key authentication.
func RegisterBlocklistRoutes(mux *http.ServeMux, h *BlocklistHandler, apiKey string, operators platform.OperatorKeys) {
	mux.Handle("GET /blocklist", platform.RequireAPIKey(apiKey, http.HandlerFunc(h.list)))
	mux.Handle("POST /blocklist", platform.RequireOperator(operators, h.add))
	mux.Handle("DELETE /blocklist/{id}", platform.RequireOperator(operators, h.remove))
}

type blocklistListResponse struct {
	Entries []models.StoredBlocklistEntry `json:"entries"`
}

type blocklistAddRequest struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

func (h *BlocklistHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.service.List(r.Context())
	if err != nil {
		blocklistError(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, blocklistListResponse{Entries: entries})
}

func (h *BlocklistHandler) add(w http.ResponseWriter, r *http.Request, operatorID string) {
	var req blocklistAddRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBlocklistBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeBlocklistDecodeError(w, err)
		return
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		writeBlocklistDecodeError(w, err)
		return
	}

	entry := models.BlocklistEntry{Type: models.BlocklistType(req.Type), Value: req.Value, Reason: req.Reason}
	saved, err := h.service.Add(r.Context(), entry, operatorID)
	var invalid *blocklist.ValidationError
	if errors.As(err, &invalid) {
		platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: invalid.Fields})
		return
	}
	if errors.Is(err, models.ErrBlocklistDuplicate) {
		platform.WriteJSON(w, http.StatusConflict, platform.ErrorResponse{
			Error:  "this value is already on the blocklist",
			Fields: map[string]string{"value": "is already on the blocklist"},
		})
		return
	}
	if err != nil {
		blocklistError(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, saved)
}

func writeBlocklistDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		platform.WriteError(w, http.StatusRequestEntityTooLarge, "request body must not exceed 4096 bytes")
		return
	}
	platform.WriteError(w, http.StatusBadRequest, "request body must be one JSON object with type, value and reason")
}

func (h *BlocklistHandler) remove(w http.ResponseWriter, r *http.Request, operatorID string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		platform.WriteError(w, http.StatusBadRequest, "the entry ID must be a positive integer")
		return
	}
	err = h.service.Remove(r.Context(), id, operatorID)
	if errors.Is(err, models.ErrBlocklistEntryNotFound) {
		platform.WriteError(w, http.StatusNotFound, "no blocklist entry has this ID")
		return
	}
	if err != nil {
		blocklistError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func blocklistError(w http.ResponseWriter, err error) {
	log.Printf("blocklist: %v", err)
	platform.WriteError(w, http.StatusInternalServerError, "internal error")
}
