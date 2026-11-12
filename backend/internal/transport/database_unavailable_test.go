package transport

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	scoresvc "github.com/nasirlinijat/anti-fraud/backend/internal/services/score"
	"github.com/nasirlinijat/anti-fraud/backend/internal/storage"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
)

func TestScoreReturns503WhenTheDatabaseIsUnreachable(t *testing.T) {
	conn, connectErr := pgx.Connect(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=2")
	if connectErr == nil {
		conn.Close(context.Background())
		t.Fatal("connected to port 1")
	}
	service := mocks.NewMockScoreService(gomock.NewController(t))
	service.EXPECT().Score(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(models.Decision{}, fmt.Errorf("read saved decision: %w", connectErr))
	mux := http.NewServeMux()
	RegisterScoreRoutes(mux, NewScoreHandler(service), mockServiceKey)

	rec := send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody)
	wantResponse(t, rec, http.StatusServiceUnavailable, "the database is unavailable, nothing was saved")
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
	}
}

func TestScoreWithRealStoresAndNoDatabaseReturns503(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	service := scoresvc.NewService(storage.NewRulesetStore(pool), storage.NewBlocklistStore(pool), storage.NewPurchaseStore(pool))
	mux := http.NewServeMux()
	RegisterScoreRoutes(mux, NewScoreHandler(service), mockServiceKey)

	rec := send(mux, http.MethodPost, "/score", mockServiceKey, minimalBody)
	wantResponse(t, rec, http.StatusServiceUnavailable, "nothing was saved")
}
