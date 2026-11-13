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

func TestResetEndpointAcceptsBackendURLs(t *testing.T) {
	tests := []struct{ base, port, want string }{
		{"", "", "http://127.0.0.1:8080/simulations/reset"},
		{"", "9000", "http://127.0.0.1:9000/simulations/reset"},
		{"https://demo.example.com/", "invalid", "https://demo.example.com/simulations/reset"},
		{"http://localhost:3000/api/", "", "http://localhost:3000/api/simulations/reset"},
		{"http://[::1]:8080", "", "http://[::1]:8080/simulations/reset"},
	}
	for _, tc := range tests {
		got, err := resetEndpoint(tc.base, tc.port)
		if err != nil || got != tc.want {
			t.Errorf("base %q port %q: got %q, %v; want %q", tc.base, tc.port, got, err, tc.want)
		}
	}
}

func TestResetEndpointRejectsInvalidURLs(t *testing.T) {
	for _, base := range []string{
		"relative/path", "//localhost:8080", "http://", "file:///tmp/reset",
		"ftp://localhost", "https://operator:password@example.com", "https://example.com?token=secret",
		"https://example.com?", "https://example.com#section", "https://example.com#",
		"http://localhost:0", "http://localhost:65536", "http://localhost:bad", "http://localhost:",
		"http://localhost/%zz",
	} {
		if _, err := resetEndpoint(base, ""); err == nil {
			t.Errorf("accepted %q", base)
		}
	}
}

func TestResetEndpointRejectsInvalidDefaultPorts(t *testing.T) {
	for _, port := range []string{"0", "-1", "65536", "8080/path", "8080@otherhost", "abc"} {
		if _, err := resetEndpoint("", port); err == nil {
			t.Errorf("accepted port %q", port)
		}
	}
}
