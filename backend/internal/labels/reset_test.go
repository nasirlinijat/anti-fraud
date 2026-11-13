package labels

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResetRejectsDifferentDatabaseTargets(t *testing.T) {
	a, err := pgxpool.New(t.Context(), "postgres://api@localhost:5432/one?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, url := range []string{"postgres://labels@localhost:5432/two?sslmode=disable", "postgres://labels@localhost:5433/one?sslmode=disable", "postgres://labels@127.0.0.1:5432/one?sslmode=disable"} {
		b, err := pgxpool.New(t.Context(), url)
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewResetStore(t.Context(), a, b)
		b.Close()
		if err == nil || err.Error() != "reset requires matching runtime and evaluation database targets" {
			t.Fatalf("got %v", err)
		}
	}
}

func TestResetComparesFallbackTargets(t *testing.T) {
	a := []*pgconn.FallbackConfig{{Host: "db", Port: 5432}}
	if !sameFallbackTargets(a, []*pgconn.FallbackConfig{{Host: "db", Port: 5432}}) {
		t.Fatal("identical targets rejected")
	}
	if sameFallbackTargets(a, nil) || sameFallbackTargets(a, []*pgconn.FallbackConfig{{Host: "other", Port: 5432}}) {
		t.Fatal("different targets accepted")
	}
}
