package blocklist

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
)

//go:generate mockgen -destination=mocks/mock_store.go -package=mocks . Store

const (
	maxValueRunes  = 200
	maxReasonRunes = 500
)

type Store interface {
	List(ctx context.Context) ([]models.StoredBlocklistEntry, error)
	ListPage(ctx context.Context, query models.BlocklistQuery) (models.BlocklistPage, error)
	Add(ctx context.Context, e models.BlocklistEntry, operatorID string) (models.StoredBlocklistEntry, error)
	Remove(ctx context.Context, id int64, operatorID string) error
}

// ValidationError lists the problems found in an entry, keyed by field name.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "invalid blocklist entry"
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) List(ctx context.Context) ([]models.StoredBlocklistEntry, error) {
	return s.store.List(ctx)
}

// Add validates the entry and saves it. It returns a *ValidationError for an
// invalid entry and models.ErrBlocklistDuplicate when the same type and value
// are already on the blocklist.
func (s *Service) Add(ctx context.Context, entry models.BlocklistEntry, operatorID string) (models.StoredBlocklistEntry, error) {
	entry, fields := validate(entry)
	if len(fields) > 0 {
		return models.StoredBlocklistEntry{}, &ValidationError{Fields: fields}
	}
	return s.store.Add(ctx, entry, operatorID)
}

func (s *Service) Remove(ctx context.Context, id int64, operatorID string) error {
	return s.store.Remove(ctx, id, operatorID)
}

// validate checks the entry and trims it. IP addresses are stored in the canonical form
// that the score endpoint compares with, so 203.000.113.9 is not accepted and
// ::ffff:203.0.113.9 becomes 203.0.113.9.
func validate(in models.BlocklistEntry) (models.BlocklistEntry, map[string]string) {
	fields := map[string]string{}
	e := models.BlocklistEntry{
		Type:   models.BlocklistType(strings.TrimSpace(string(in.Type))),
		Value:  strings.TrimSpace(in.Value),
		Reason: strings.TrimSpace(in.Reason),
	}

	if !slices.Contains(models.BlocklistTypes, e.Type) {
		fields["type"] = "must be one of card, device, player, ip"
	}
	switch {
	case e.Value == "":
		fields["value"] = "is required"
	case utf8.RuneCountInString(e.Value) > maxValueRunes:
		fields["value"] = "must be at most 200 characters"
	case e.Type == models.BlocklistIP:
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
