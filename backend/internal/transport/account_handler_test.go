package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/models"
	"github.com/nasirlinijat/anti-fraud/backend/internal/platform"
	"github.com/nasirlinijat/anti-fraud/backend/internal/services/auth"
)

func TestAdminCreatesAccounts(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "accounts-test-admin", models.RoleAdmin)
	reviewer := server.signIn(t, "accounts-test-rita", models.RoleReviewer)
	body := `{"username":"Accounts-Test-New","password":"new reviewer password","role":"reviewer"}`

	byReviewer := server.send(http.MethodPost, "/admin/accounts", reviewer, body)
	created := server.send(http.MethodPost, "/admin/accounts", admin, body)

	if byReviewer.Code != http.StatusForbidden {
		t.Fatalf("reviewer create = %d, want 403", byReviewer.Code)
	}
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"username":"accounts-test-new","role":"reviewer","disabled":false`) {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	if _, err := server.sessions.Login(context.Background(), "accounts-test-new", "new reviewer password"); err != nil {
		t.Fatalf("new account cannot sign in: %v", err)
	}
	// The failed insert aborts the test transaction, so it comes last.
	if taken := server.send(http.MethodPost, "/admin/accounts", admin, body); taken.Code != http.StatusConflict {
		t.Fatalf("taken username = %d %s, want 409", taken.Code, taken.Body)
	}
}

func TestCreateAccountNamesTheInvalidField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		field string
	}{
		{"short username", `{"username":"ab","password":"long enough password","role":"reviewer"}`, "username"},
		{"unknown role", `{"username":"accounts-test-x","password":"long enough password","role":"owner"}`, "role"},
		{"short password", `{"username":"accounts-test-x","password":"short","role":"admin"}`, "password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testTx(t)
			server := newSessionTestServer(t, tx)
			admin := server.signIn(t, "accounts-test-admin", models.RoleAdmin)

			rec := server.send(http.MethodPost, "/admin/accounts", admin, tc.body)

			var got platform.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusBadRequest || got.Fields[tc.field] == "" {
				t.Fatalf("create = %d %s, want 400 naming %s", rec.Code, rec.Body, tc.field)
			}
		})
	}
}

func TestListAccountsNeverShowsPasswordHashes(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "accounts-test-admin", models.RoleAdmin)
	server.signIn(t, "accounts-test-rita", models.RoleReviewer)

	rec := server.send(http.MethodGet, "/admin/accounts", admin, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "$2a$") || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("list exposes password data: %s", rec.Body)
	}
	var got accountListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found := map[string]accountDetailsResponse{}
	for _, a := range got.Accounts {
		found[a.Username] = a
	}
	rita, ok := found["accounts-test-rita"]
	if !ok || rita.Role != models.RoleReviewer || rita.Disabled || rita.CreatedAt.IsZero() {
		t.Fatalf("rita = %+v, found %v", rita, ok)
	}
}

func TestDisabledAccountIsSignedOutAtOnce(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "accounts-test-admin", models.RoleAdmin)
	rita := server.signIn(t, "accounts-test-rita", models.RoleReviewer)

	rec := server.send(http.MethodPost, "/admin/accounts/Accounts-Test-Rita/disable", admin, "")

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"disabled":true`) {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	if after := server.send(http.MethodGet, "/reviews", rita, ""); after.Code != http.StatusUnauthorized {
		t.Fatalf("rita's session after disable = %d, want 401", after.Code)
	}
	if _, err := server.sessions.Login(context.Background(), "accounts-test-rita", "session test password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("sign in after disable: err = %v, want ErrInvalidCredentials", err)
	}
}

func TestDisableRefusesOwnAndUnknownAccounts(t *testing.T) {
	tx := testTx(t)
	server := newSessionTestServer(t, tx)
	admin := server.signIn(t, "accounts-test-admin", models.RoleAdmin)
	reviewer := server.signIn(t, "accounts-test-rita", models.RoleReviewer)

	self := server.send(http.MethodPost, "/admin/accounts/accounts-test-admin/disable", admin, "")
	unknown := server.send(http.MethodPost, "/admin/accounts/accounts-test-missing/disable", admin, "")
	byReviewer := server.send(http.MethodPost, "/admin/accounts/accounts-test-admin/disable", reviewer, "")

	if self.Code != http.StatusConflict || unknown.Code != http.StatusNotFound || byReviewer.Code != http.StatusForbidden {
		t.Fatalf("self = %d, unknown = %d, reviewer = %d; want 409, 404, 403", self.Code, unknown.Code, byReviewer.Code)
	}
	if _, err := server.sessions.Identify(context.Background(), admin); err != nil {
		t.Fatalf("admin session after refused self-disable: %v", err)
	}
}
