package reviews

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

const maxDecisionBytes = 1024

type resolution struct {
	status        string
	resolvedAt    time.Time
	deviceBlocked bool
}

type decisionResponse struct {
	PurchaseID string    `json:"purchase_id"`
	Decision   string    `json:"decision"`
	AnalystID  string    `json:"analyst_id"`
	ResolvedAt time.Time `json:"resolved_at"`
	// DeviceBlocked is true when this rejection added the device to the blocklist.
	DeviceBlocked bool `json:"device_blocked"`
}

// decisionRequest is the body of a review decision.
type decisionRequest struct {
	Decision    string `json:"decision"`
	BlockDevice bool   `json:"block_device"`
}

func serveResolution(w http.ResponseWriter, r *http.Request, store reviewStore, analystID string) {
	id := r.PathValue("purchase_id")
	purchaseID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || purchaseID <= 0 || strconv.FormatInt(purchaseID, 10) != id {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}
	body, ok := readDecision(w, r)
	if !ok {
		return
	}

	result, err := store.resolve(r.Context(), purchaseID, body.Decision, analystID, body.BlockDevice)
	if err != nil {
		log.Printf("save review decision: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeResolution(w, result.status, decisionResponse{
		PurchaseID:    id,
		Decision:      body.Decision,
		AnalystID:     analystID,
		ResolvedAt:    result.resolvedAt.UTC(),
		DeviceBlocked: result.deviceBlocked,
	})
}

// readDecision reads and validates the decision body. On failure it writes
// the error response and returns false.
func readDecision(w http.ResponseWriter, r *http.Request) (decisionRequest, bool) {
	var body decisionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDecisionBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if err == nil {
		err = dec.Decode(new(any))
		if err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("multiple JSON values")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		platform.WriteError(w, http.StatusRequestEntityTooLarge, "review decision body is too large")
	case err != nil:
		platform.WriteError(w, http.StatusBadRequest, "request body must be a single review decision object")
	case body.Decision != "approve" && body.Decision != "reject":
		platform.WriteError(w, http.StatusBadRequest, "decision must be approve or reject")
	case body.BlockDevice && body.Decision != "reject":
		platform.WriteError(w, http.StatusBadRequest, "block_device is only allowed with a reject decision")
	default:
		return body, true
	}
	return decisionRequest{}, false
}

// writeResolution writes the response for the status the store returned.
// saved is the body sent when the decision was saved.
func writeResolution(w http.ResponseWriter, status string, saved decisionResponse) {
	switch status {
	case "saved":
		platform.WriteJSON(w, http.StatusCreated, saved)
	case "not_found":
		platform.WriteError(w, http.StatusNotFound, "purchase not found")
	case "not_review":
		platform.WriteError(w, http.StatusConflict, "purchase was not scored for review")
	case "already_resolved":
		platform.WriteError(w, http.StatusConflict, "purchase has already been decided")
	default:
		log.Printf("unexpected review decision status %q", status)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	}
}
