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
	status     string
	resolvedAt time.Time
}

type decisionResponse struct {
	PurchaseID string    `json:"purchase_id"`
	Decision   string    `json:"decision"`
	AnalystID  string    `json:"analyst_id"`
	ResolvedAt time.Time `json:"resolved_at"`
}

func serveResolution(w http.ResponseWriter, r *http.Request, store reviewStore, analystID string) {
	id := r.PathValue("purchase_id")
	purchaseID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || purchaseID <= 0 || strconv.FormatInt(purchaseID, 10) != id {
		platform.WriteError(w, http.StatusBadRequest, "invalid purchase ID")
		return
	}

	var body struct {
		Decision string `json:"decision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDecisionBytes))
	dec.DisallowUnknownFields()
	err = dec.Decode(&body)
	if err == nil {
		err = dec.Decode(new(any))
		if err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("multiple JSON values")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			platform.WriteError(w, http.StatusRequestEntityTooLarge, "review decision body is too large")
		} else {
			platform.WriteError(w, http.StatusBadRequest, "request body must be a single review decision object")
		}
		return
	}
	if body.Decision != "approve" && body.Decision != "reject" {
		platform.WriteError(w, http.StatusBadRequest, "decision must be approve or reject")
		return
	}

	result, err := store.resolve(r.Context(), purchaseID, body.Decision, analystID)
	if err != nil {
		log.Printf("save review decision: %v", err)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	switch result.status {
	case "saved":
		platform.WriteJSON(w, http.StatusCreated, decisionResponse{
			PurchaseID: id,
			Decision:   body.Decision,
			AnalystID:  analystID,
			ResolvedAt: result.resolvedAt.UTC(),
		})
	case "not_found":
		platform.WriteError(w, http.StatusNotFound, "purchase not found")
	case "not_review":
		platform.WriteError(w, http.StatusConflict, "purchase was not scored for review")
	case "already_resolved":
		platform.WriteError(w, http.StatusConflict, "purchase has already been decided")
	default:
		log.Printf("unexpected review decision status %q", result.status)
		platform.WriteError(w, http.StatusInternalServerError, "internal error")
	}
}
