package transport

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

const loginBody = `{"event_id":"evt-1","player_id":"player-1","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`

var accountEventNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func newAccountEventMux(t *testing.T) (*http.ServeMux, *mocks.MockAccountEventService) {
	t.Helper()
	return newAccountEventMuxWithTimeout(t, 0)
}

func newAccountEventMuxWithTimeout(t *testing.T, timeout time.Duration) (*http.ServeMux, *mocks.MockAccountEventService) {
	t.Helper()
	service := mocks.NewMockAccountEventService(gomock.NewController(t))
	handler := NewAccountEventHandler(service).UseTimeout(timeout)
	handler.now = func() time.Time { return accountEventNow }
	mux := http.NewServeMux()
	RegisterAccountEventRoutes(mux, handler, mockServiceKey)
	return mux, service
}

func TestAccountEventsRequireTheServiceKey(t *testing.T) {
	for name, key := range map[string]string{"missing key": "", "wrong key": "wrong", "operator key": mockOperatorKey} {
		t.Run(name, func(t *testing.T) {
			mux, service := newAccountEventMux(t)
			service.EXPECT().Record(gomock.Any(), gomock.Any()).Times(0)

			rec := send(mux, http.MethodPost, "/account-events", key, loginBody)

			wantResponse(t, rec, http.StatusUnauthorized, `"error":"missing or invalid API key"`)
		})
	}
}

func TestAccountEventsRecordANewEventWith201(t *testing.T) {
	mux, service := newAccountEventMux(t)
	device, country := "device-1", "TR"
	want := models.AccountEvent{
		EventID: "evt-1", PlayerID: "player-1", EventType: models.AccountEventPasswordReset,
		EventTime: time.Date(2026, 11, 8, 6, 0, 0, 0, time.UTC), DeviceID: &device, IPCountry: &country,
	}
	service.EXPECT().Record(gomock.Any(), want).Return(true, nil)

	rec := send(mux, http.MethodPost, "/account-events", mockServiceKey,
		`{"event_id":" evt-1 ","player_id":"player-1","event_type":"password_reset","event_time":"2026-11-08T10:00:00+04:00","device_id":"device-1","ip_country":" tr "}`)

	wantResponse(t, rec, http.StatusCreated,
		`{"event_id":"evt-1","player_id":"player-1","event_type":"password_reset","event_time":"2026-11-08T06:00:00Z","device_id":"device-1","ip_country":"TR"}`)
}

func TestAccountEventsTreatEmptyOptionalFieldsAsOmitted(t *testing.T) {
	mux, service := newAccountEventMux(t)
	service.EXPECT().Record(gomock.Any(), models.AccountEvent{
		EventID: "evt-1", PlayerID: "player-1", EventType: models.AccountEventLogin, EventTime: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
	}).Return(true, nil)

	rec := send(mux, http.MethodPost, "/account-events", mockServiceKey,
		`{"event_id":"evt-1","player_id":"player-1","event_type":"login","event_time":"2026-11-08T10:00:00Z","device_id":"  ","ip_country":""}`)

	wantResponse(t, rec, http.StatusCreated, `"device_id":null`, `"ip_country":null`)
}

func TestAccountEventsReturn200ForAMatchingRetry(t *testing.T) {
	mux, service := newAccountEventMux(t)
	service.EXPECT().Record(gomock.Any(), gomock.Any()).Return(false, nil)

	rec := send(mux, http.MethodPost, "/account-events", mockServiceKey, loginBody)

	wantResponse(t, rec, http.StatusOK, `"event_id":"evt-1"`, `"event_type":"login"`)
}

func TestAccountEventsReturn409ForADifferentEventWithASavedID(t *testing.T) {
	mux, service := newAccountEventMux(t)
	service.EXPECT().Record(gomock.Any(), gomock.Any()).Return(false, models.ErrDuplicateAccountEvent)

	rec := send(mux, http.MethodPost, "/account-events", mockServiceKey, loginBody)

	wantResponse(t, rec, http.StatusConflict,
		`"error":"an account event with this event ID was already recorded"`, `"event_id":"is already recorded"`)
}

func TestAccountEventsRejectInvalidRequests(t *testing.T) {
	future := accountEventNow.Add(6 * time.Minute).Format(time.RFC3339)
	nearFuture := accountEventNow.Add(4 * time.Minute).Format(time.RFC3339)
	long := strings.Repeat("x", maxAccountEventIDBytes+1)
	withField := func(field, value string) string {
		return fmt.Sprintf(`{"event_id":"e","player_id":"p","event_type":"login","event_time":"2026-11-08T10:00:00Z",%q:%q}`, field, value)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"missing event ID", `{"player_id":"p","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`, `"event_id":"is required"`},
		{"blank event ID", `{"event_id":"  ","player_id":"p","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`, `"event_id":"is required"`},
		{"missing player", `{"event_id":"e","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`, `"player_id":"is required"`},
		{"missing type", `{"event_id":"e","player_id":"p","event_time":"2026-11-08T10:00:00Z"}`, `"event_type":"must be login or password_reset"`},
		{"unknown type", `{"event_id":"e","player_id":"p","event_type":"logout","event_time":"2026-11-08T10:00:00Z"}`, `"event_type":"must be login or password_reset"`},
		{"missing time", `{"event_id":"e","player_id":"p","event_type":"login"}`, `"event_time":"is required"`},
		{"time in the future", fmt.Sprintf(`{"event_id":"e","player_id":"p","event_type":"login","event_time":%q}`, future),
			`"event_time":"must not be more than 5 minutes in the future"`},
		{"time not RFC 3339", `{"event_id":"e","player_id":"p","event_type":"login","event_time":"2026-11-08 10:00"}`, `"error":"times must be RFC 3339`},
		{"wrong type", `{"event_id":7,"player_id":"p","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`, `"event_id":"must be a string"`},
		{"unknown field", `{"event_id":"e","player_id":"p","event_type":"login","event_time":"2026-11-08T10:00:00Z","amount_minor":1}`,
			`"amount_minor":"is not a known field"`},
		{"event ID too long", withField("event_id", long), `"event_id":"must not be longer than 256 bytes"`},
		{"player ID too long", withField("player_id", long), `"player_id":"must not be longer than 256 bytes"`},
		{"device ID too long", withField("device_id", long), `"device_id":"must not be longer than 256 bytes"`},
		{"control character in event ID", withField("event_id", "evt\n1"), `"event_id":"must not contain control characters"`},
		{"control character in player ID", `{"event_id":"e","player_id":"player\u00001","event_type":"login","event_time":"2026-11-08T10:00:00Z"}`,
			`"player_id":"must not contain control characters"`},
		{"control character in device ID", withField("device_id", "dev\t1"), `"device_id":"must not contain control characters"`},
		{"three-letter country", withField("ip_country", "TUR"), `"ip_country":"must be a two-letter country code"`},
		{"country with a digit", withField("ip_country", "T1"), `"ip_country":"must be a two-letter country code"`},
		{"country with a non-ASCII letter", withField("ip_country", "TÜ"), `"ip_country":"must be a two-letter country code"`},
		{"two objects", loginBody + loginBody, `"error":"request body must contain a single JSON object"`},
		{"not JSON", `{"event_id":`, `"error":"request body is not valid JSON"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, service := newAccountEventMux(t)
			service.EXPECT().Record(gomock.Any(), gomock.Any()).Times(0)

			rec := send(mux, http.MethodPost, "/account-events", mockServiceKey, tt.body)

			wantResponse(t, rec, http.StatusBadRequest, tt.want)
		})
	}

	t.Run("IDs at the length limit", func(t *testing.T) {
		mux, service := newAccountEventMux(t)
		service.EXPECT().Record(gomock.Any(), gomock.Any()).Return(true, nil)
		limit := strings.Repeat("x", maxAccountEventIDBytes)

		rec := send(mux, http.MethodPost, "/account-events", mockServiceKey,
			fmt.Sprintf(`{"event_id":%q,"player_id":%q,"event_type":"login","event_time":"2026-11-08T10:00:00Z","device_id":%q}`, limit, limit, limit))

		wantResponse(t, rec, http.StatusCreated)
	})

	t.Run("time within the clock skew", func(t *testing.T) {
		mux, service := newAccountEventMux(t)
		service.EXPECT().Record(gomock.Any(), gomock.Any()).Return(true, nil)

		rec := send(mux, http.MethodPost, "/account-events", mockServiceKey,
			fmt.Sprintf(`{"event_id":"e","player_id":"p","event_type":"login","event_time":%q}`, nearFuture))

		wantResponse(t, rec, http.StatusCreated)
	})
}

func TestAccountEventsReportStoreFailures(t *testing.T) {
	t.Run("database unavailable", func(t *testing.T) {
		mux, service := newAccountEventMux(t)
		service.EXPECT().Record(gomock.Any(), gomock.Any()).Return(false, &pgconn.ConnectError{})

		rec := send(mux, http.MethodPost, "/account-events", mockServiceKey, loginBody)

		wantResponse(t, rec, http.StatusServiceUnavailable, `"error":"the database is unavailable, nothing was saved; retry later"`)
		if rec.Header().Get("Retry-After") != "1" {
			t.Errorf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
		}
	})

	t.Run("other error", func(t *testing.T) {
		mux, service := newAccountEventMux(t)
		service.EXPECT().Record(gomock.Any(), gomock.Any()).Return(false, errServiceDown)

		rec := send(mux, http.MethodPost, "/account-events", mockServiceKey, loginBody)

		wantResponse(t, rec, http.StatusInternalServerError, `"error":"internal error"`)
	})
}

func TestAccountEventsStopAfterTheTimeout(t *testing.T) {
	mux, service := newAccountEventMuxWithTimeout(t, 20*time.Millisecond)
	service.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ models.AccountEvent) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	})

	rec := send(mux, http.MethodPost, "/account-events", mockServiceKey, loginBody)

	wantResponse(t, rec, http.StatusServiceUnavailable, `"error":"recording the account event took too long; retry later"`)
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
	}
}
