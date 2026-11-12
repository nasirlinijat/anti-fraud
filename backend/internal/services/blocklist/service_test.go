package blocklist_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist/mocks"
	"go.uber.org/mock/gomock"
)

func newService(t *testing.T) (*blocklist.Service, *mocks.MockStore) {
	store := mocks.NewMockStore(gomock.NewController(t))
	return blocklist.NewService(store), store
}

func TestAddRejectsAnInvalidEntryBeforeSaving(t *testing.T) {
	tests := []struct {
		name  string
		entry models.BlocklistEntry
		field string
	}{
		{"unknown type", models.BlocklistEntry{Type: "email", Value: "a@b.c", Reason: "spam"}, "type"},
		{"empty value", models.BlocklistEntry{Type: models.BlocklistCard, Value: "  ", Reason: "stolen"}, "value"},
		{"long value", models.BlocklistEntry{Type: models.BlocklistCard, Value: strings.Repeat("x", 201), Reason: "stolen"}, "value"},
		{"bad ip", models.BlocklistEntry{Type: models.BlocklistIP, Value: "203.000.113.9", Reason: "proxy"}, "value"},
		{"empty reason", models.BlocklistEntry{Type: models.BlocklistCard, Value: "card-1"}, "reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Add(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			_, err := svc.Add(context.Background(), tt.entry, "operator-1")
			var invalid *blocklist.ValidationError
			if !errors.As(err, &invalid) || invalid.Fields[tt.field] == "" {
				t.Fatalf("err = %v, want a problem for %s", err, tt.field)
			}
		})
	}
}

func TestAddSavesTheCleanedEntry(t *testing.T) {
	tests := []struct {
		name string
		in   models.BlocklistEntry
		want models.BlocklistEntry
	}{
		{"trims card", models.BlocklistEntry{Type: " card ", Value: " card-1 ", Reason: " stolen "},
			models.BlocklistEntry{Type: models.BlocklistCard, Value: "card-1", Reason: "stolen"}},
		{"unmaps an IPv4 in IPv6", models.BlocklistEntry{Type: models.BlocklistIP, Value: "::ffff:203.0.113.9", Reason: "proxy"},
			models.BlocklistEntry{Type: models.BlocklistIP, Value: "203.0.113.9", Reason: "proxy"}},
		{"shortens an IPv6", models.BlocklistEntry{Type: models.BlocklistIP, Value: "2001:0db8:0000::1", Reason: "proxy"},
			models.BlocklistEntry{Type: models.BlocklistIP, Value: "2001:db8::1", Reason: "proxy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newService(t)
			store.EXPECT().Add(gomock.Any(), tt.want, "operator-1").Return(models.StoredBlocklistEntry{ID: 3}, nil)

			got, err := svc.Add(context.Background(), tt.in, "operator-1")
			if err != nil || got.ID != 3 {
				t.Fatalf("Add = %+v, %v", got, err)
			}
		})
	}
}

func TestAddReturnsADuplicate(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Add(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.StoredBlocklistEntry{}, models.ErrBlocklistDuplicate)
	_, err := svc.Add(context.Background(), models.BlocklistEntry{Type: models.BlocklistCard, Value: "card-1", Reason: "stolen"}, "operator-1")
	if !errors.Is(err, models.ErrBlocklistDuplicate) {
		t.Fatalf("err = %v, want ErrBlocklistDuplicate", err)
	}
}

func TestRemoveAndListGoToTheStore(t *testing.T) {
	svc, store := newService(t)
	store.EXPECT().Remove(gomock.Any(), int64(5), "operator-1").Return(models.ErrBlocklistEntryNotFound)
	store.EXPECT().List(gomock.Any()).Return([]models.StoredBlocklistEntry{{ID: 1}}, nil)

	if err := svc.Remove(context.Background(), 5, "operator-1"); !errors.Is(err, models.ErrBlocklistEntryNotFound) {
		t.Fatalf("Remove err = %v, want ErrBlocklistEntryNotFound", err)
	}
	if list, err := svc.List(context.Background()); err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
}
