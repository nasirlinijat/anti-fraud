package blocklist

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

const (
	DefaultPageSize = 25
	MaxPageSize     = 100
	maxQueryRunes   = 200
	maxQueryBytes   = 800
)

// ListPage validates filters and bounds before reading a page from PostgreSQL.
func (s *Service) ListPage(ctx context.Context, query models.BlocklistQuery) (models.BlocklistPage, error) {
	query, fields := validateQuery(query)
	if len(fields) > 0 {
		return models.BlocklistPage{}, &ValidationError{Fields: fields}
	}
	return s.store.ListPage(ctx, query)
}

func validateQuery(query models.BlocklistQuery) (models.BlocklistQuery, map[string]string) {
	fields := map[string]string{}
	if query.Type != "" && !slices.Contains(models.BlocklistTypes, query.Type) {
		fields["type"] = "must be one of card, device, player, ip"
	}
	switch {
	case !utf8.ValidString(query.Query) || strings.ContainsFunc(query.Query, unicode.IsControl):
		fields["q"] = "must be valid UTF-8 without control characters"
	case len(query.Query) > maxQueryBytes || utf8.RuneCountInString(query.Query) > maxQueryRunes:
		fields["q"] = "must be at most 200 characters and 800 UTF-8 bytes"
	}
	query.Query = strings.TrimSpace(query.Query)
	if query.Page == 0 {
		query.Page = 1
	}
	if query.Page < 1 {
		fields["page"] = "must be a positive integer"
	}
	if query.PageSize == 0 {
		query.PageSize = DefaultPageSize
	}
	if query.PageSize < 1 || query.PageSize > MaxPageSize {
		fields["page_size"] = "must be an integer from 1 to 100"
	}
	return query, fields
}
