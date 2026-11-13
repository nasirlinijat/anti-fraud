package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResetClientUsesCredentialsAndPropagatesStatuses(t *testing.T) {
	for _, status := range []int{204, 401, 404, 409, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/simulations/reset" || r.Header.Get("X-API-Key") != "operator" {
				t.Error("wrong request")
			}
			w.WriteHeader(status)
		}))
		t.Setenv("RESET_URL", server.URL)
		t.Setenv("RESET_OPERATOR_KEY", "operator")
		t.Setenv("RESET_SESSION_TOKEN", "")
		err := reset()
		server.Close()
		if (err == nil) != (status == 204) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}

func TestResetClientRequiresCredentials(t *testing.T) {
	t.Setenv("RESET_OPERATOR_KEY", "")
	t.Setenv("RESET_SESSION_TOKEN", "")
	if err := reset(); err == nil {
		t.Fatal("accepted absent credentials")
	}
}

func TestResetClientDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	t.Setenv("RESET_URL", server.URL)
	t.Setenv("RESET_SESSION_TOKEN", "secret")
	if err := reset(); err == nil {
		t.Fatal("accepted redirect")
	}
}
