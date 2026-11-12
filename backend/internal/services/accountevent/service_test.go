package accountevent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/accountevent"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/accountevent/mocks"
	"go.uber.org/mock/gomock"
)

var login = models.AccountEvent{
	EventID:   "evt-1",
	PlayerID:  "player-1",
	EventType: models.AccountEventLogin,
	EventTime: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
}

func TestRecordReportsWhetherTheEventIsNew(t *testing.T) {
	for _, isNew := range []bool{true, false} {
		store := mocks.NewMockStore(gomock.NewController(t))
		store.EXPECT().Save(gomock.Any(), login).Return(isNew, nil)

		got, err := accountevent.NewService(store).Record(context.Background(), login)

		if err != nil || got != isNew {
			t.Errorf("Record = %v, %v, want %v, nil", got, err, isNew)
		}
	}
}

func TestRecordReturnsTheDuplicateError(t *testing.T) {
	store := mocks.NewMockStore(gomock.NewController(t))
	store.EXPECT().Save(gomock.Any(), login).Return(false, models.ErrDuplicateAccountEvent)

	_, err := accountevent.NewService(store).Record(context.Background(), login)

	if !errors.Is(err, models.ErrDuplicateAccountEvent) {
		t.Errorf("err = %v, want ErrDuplicateAccountEvent", err)
	}
}
