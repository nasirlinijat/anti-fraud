package blocklist

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/blocklist/mocks"
	"go.uber.org/mock/gomock"
)

func TestListPageDefaultsAndTrimsTheLiteralQuery(t *testing.T) {
	store := mocks.NewMockStore(gomock.NewController(t))
	want := models.BlocklistQuery{Type: models.BlocklistDevice, Query: `%_\.*`, Page: 1, PageSize: 25}
	store.EXPECT().ListPage(gomock.Any(), want).Return(models.BlocklistPage{}, nil)
	_, err := NewService(store).ListPage(context.Background(), models.BlocklistQuery{Type: models.BlocklistDevice, Query: `  %_\.*  `})
	if err != nil {
		t.Fatal(err)
	}
}

func TestListPageRejectsInvalidFiltersBeforeReading(t *testing.T) {
	cases := []struct {
		name  string
		query models.BlocklistQuery
		field string
	}{
		{"type", models.BlocklistQuery{Type: "email"}, "type"},
		{"negative page", models.BlocklistQuery{Page: -1}, "page"},
		{"negative size", models.BlocklistQuery{PageSize: -1}, "page_size"},
		{"oversized page", models.BlocklistQuery{PageSize: 101}, "page_size"},
		{"long query", models.BlocklistQuery{Query: strings.Repeat("x", 201)}, "q"},
		{"long unicode", models.BlocklistQuery{Query: strings.Repeat("😀", 201)}, "q"},
		{"invalid UTF-8", models.BlocklistQuery{Query: string([]byte{0xff})}, "q"},
		{"null", models.BlocklistQuery{Query: "x\x00"}, "q"},
		{"newline", models.BlocklistQuery{Query: "x\n"}, "q"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := mocks.NewMockStore(gomock.NewController(t))
			_, err := NewService(store).ListPage(context.Background(), tc.query)
			var invalid *ValidationError
			if !errors.As(err, &invalid) || invalid.Fields[tc.field] == "" {
				t.Fatalf("error = %v, want field %s", err, tc.field)
			}
		})
	}
}

func TestListPageAcceptsMaximumUnicodeLengthAndPropagatesErrors(t *testing.T) {
	store := mocks.NewMockStore(gomock.NewController(t))
	query := models.BlocklistQuery{Query: strings.Repeat("😀", 200), Page: 1, PageSize: 100}
	boom := errors.New("unavailable")
	store.EXPECT().ListPage(gomock.Any(), query).Return(models.BlocklistPage{}, boom)
	if _, err := NewService(store).ListPage(context.Background(), query); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
}
