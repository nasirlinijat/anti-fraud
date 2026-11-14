package transport

import (
	"context"
	"net/http"

	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
)

type consoleSessions map[string]platform.Identity

func (s consoleSessions) Identify(_ context.Context, token string) (platform.Identity, error) {
	identity, ok := s[token]
	if !ok {
		return platform.Identity{}, platform.ErrNoSession
	}
	return identity, nil
}

var consoleTestSessions = consoleSessions{
	"mock-admin-token":      {Username: "op-1", Role: platform.RoleAdmin},
	"mock-reviewer-token":   {Username: "analyst-1", Role: platform.RoleReviewer},
	"mock-service-key":      {Username: "admin", Role: platform.RoleAdmin},
	"test-key":              {Username: "admin", Role: platform.RoleAdmin},
	"admin-token":           {Username: "test-operator", Role: platform.RoleAdmin},
	"service-key":           {Username: "admin", Role: platform.RoleAdmin},
	"reviewer-token":        {Username: "first", Role: platform.RoleReviewer},
	"second-reviewer-token": {Username: "second", Role: platform.RoleReviewer},
	"spike-reviewer-token":  {Username: "test", Role: platform.RoleReviewer},
}

func setTestCredential(r *http.Request, token string) {
	if r.URL.Path == "/score" || r.URL.Path == "/account-events" || r.URL.Path == "/decision-status" {
		r.Header.Set("X-API-Key", token)
		return
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
}
