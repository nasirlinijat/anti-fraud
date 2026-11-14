package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
)

func parseBlocklistQuery(raw string) (*models.BlocklistQuery, *blocklist.ValidationError) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 4096 {
		return nil, queryFieldError("query", "must not exceed 4096 bytes")
	}
	params, err := url.ParseQuery(raw)
	if err != nil || len(params) == 0 {
		return nil, queryFieldError("query", "must use valid URL encoding")
	}
	for name, values := range params {
		if name != "type" && name != "q" && name != "page" && name != "page_size" {
			return nil, queryFieldError("query", "contains an unsupported parameter")
		}
		if len(values) != 1 {
			return nil, queryFieldError(name, "must be supplied at most once")
		}
	}
	query := models.BlocklistQuery{Type: models.BlocklistType(params.Get("type")), Query: params.Get("q")}
	if _, present := params["page"]; present {
		query.Page, err = positiveBlocklistNumber(params.Get("page"))
		if err != nil {
			return nil, queryFieldError("page", "must be a positive integer")
		}
	}
	if _, present := params["page_size"]; present {
		size, err := positiveBlocklistNumber(params.Get("page_size"))
		if err != nil || size > blocklist.MaxPageSize {
			return nil, queryFieldError("page_size", "must be an integer from 1 to 100")
		}
		query.PageSize = int(size)
	}
	return &query, nil
}

func positiveBlocklistNumber(value string) (int64, error) {
	if value == "" || strings.ContainsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, errors.New("invalid integer")
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 1 {
		return 0, errors.New("invalid integer")
	}
	return number, nil
}

func queryFieldError(field, message string) *blocklist.ValidationError {
	return &blocklist.ValidationError{Fields: map[string]string{field: message}}
}

func (h *BlocklistHandler) listPage(w http.ResponseWriter, r *http.Request, query models.BlocklistQuery) {
	page, err := h.service.ListPage(r.Context(), query)
	var invalid *blocklist.ValidationError
	if errors.As(err, &invalid) {
		writeBlocklistQueryError(w, invalid)
		return
	}
	if err != nil {
		blocklistError(w, r, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, page)
}

func writeBlocklistQueryError(w http.ResponseWriter, invalid *blocklist.ValidationError) {
	platform.WriteJSON(w, http.StatusBadRequest, platform.ErrorResponse{Error: "invalid request", Fields: invalid.Fields})
}
