package platform

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewServerSetsTimeouts(t *testing.T) {
	srv := NewServer(":8080", http.NewServeMux())

	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", srv.Addr, ":8080")
	}
	for name, got := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if got <= 0 {
			t.Errorf("%s = %v, want a positive timeout", name, got)
		}
	}
	if srv.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, maxHeaderBytes)
	}
}

func TestServerServesHealthRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux, fakePinger{})

	ts := httptest.NewUnstartedServer(nil)
	ts.Config = NewServer("", mux)
	ts.Start()
	defer ts.Close()

	for _, path := range []string{"/health", "/ready"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestServerClosesConnectionWithIncompleteHeaders(t *testing.T) {
	srv := NewServer("", http.NewServeMux())
	srv.ReadHeaderTimeout = 50 * time.Millisecond

	ts := httptest.NewUnstartedServer(nil)
	ts.Config = srv
	ts.Start()
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a request line but never finish the headers.
	if _, err := conn.Write([]byte("GET /health HTTP/1.1\r\nHost: test\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bufio.NewReader(conn).ReadByte(); err == nil {
		t.Fatal("server responded to incomplete headers, want the connection closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("server kept the connection open past the read header timeout")
	}
}
