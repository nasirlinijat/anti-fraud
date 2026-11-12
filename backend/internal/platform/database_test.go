package platform

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

func unreachableDatabaseError(t *testing.T) error {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=2")
	if err == nil {
		conn.Close(context.Background())
		t.Fatal("connected to port 1")
	}
	return err
}

func TestDatabaseUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connect failure", unreachableDatabaseError(t), true},
		{"wrapped connect failure", fmt.Errorf("read saved decision: %w", unreachableDatabaseError(t)), true},
		{"other error", errors.New("insert decision: check constraint"), false},
		{"deadline", context.DeadlineExceeded, false},
		{"no error", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DatabaseUnavailable(tt.err); got != tt.want {
				t.Fatalf("DatabaseUnavailable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
