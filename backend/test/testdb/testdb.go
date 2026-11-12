//go:build integration

package testdb

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	image           = "postgres:18-alpine"
	runtimePassword = "test-runtime-password"
	labelsPassword  = "test-labels-password"
)

type Database struct {
	OwnerURL   string
	RuntimeURL string
	container  *postgres.PostgresContainer
}

func Start(ctx context.Context) (*Database, error) {
	_, file, _, _ := runtime.Caller(0)
	backend := filepath.Join(filepath.Dir(file), "..", "..")
	scripts, err := filepath.Glob(filepath.Join(backend, "migrations", "*.up.sql"))
	if err != nil {
		return nil, err
	}
	if len(scripts) == 0 {
		return nil, fmt.Errorf("no migrations found in %s", backend)
	}
	slices.Sort(scripts)
	scripts = append(scripts, filepath.Join(backend, "runtime-role.sql"))

	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase("antifraud"),
		postgres.WithUsername("antifraud"),
		postgres.WithPassword("antifraud"),
		postgres.WithInitScripts(scripts...),
		testcontainers.WithEnv(map[string]string{
			"RUNTIME_DB_PASSWORD": runtimePassword,
			"LABELS_DB_PASSWORD":  labelsPassword,
		}),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		if container != nil {
			_ = testcontainers.TerminateContainer(container)
		}
		return nil, err
	}
	db := &Database{container: container}
	if db.OwnerURL, err = container.ConnectionString(ctx, "sslmode=disable"); err != nil {
		_ = db.Stop()
		return nil, err
	}
	if db.RuntimeURL, err = withUser(db.OwnerURL, "antifraud_api", runtimePassword); err != nil {
		_ = db.Stop()
		return nil, err
	}
	return db, nil
}

func (d *Database) Stop() error {
	return testcontainers.TerminateContainer(d.container)
}

func withUser(dsn, user, password string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(user, password)
	return u.String(), nil
}
