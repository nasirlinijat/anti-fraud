//go:build integration

package integration

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestEdgeLogsOmitSearchQueriesWithoutChangingRequests(t *testing.T) {
	container, endpoint := loggingEdge(t)
	marker := "af197-private-search-marker"
	paths := []string{
		"/score?q=" + marker, "/score?q=first&q=" + marker,
		"/score?%71=" + marker, "/score?q=%ZZ&other=" + marker,
		"/score?q=" + marker + ";other=x", "/score?other=" + marker,
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, path := range paths {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "localhost:8080"
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != path {
			t.Fatalf("forwarded request %q: status %d, body %q, err %v", path, response.StatusCode, body, err)
		}
	}
	logs := edgeRequestLogs(t, container, len(paths))
	if strings.Contains(logs, marker) || strings.Contains(logs, "%ZZ") {
		t.Fatal("the edge logged private query text")
	}
}

func loggingEdge(t *testing.T) (testcontainers.Container, string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	config, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	// Echo the upstream request URI to verify that filtering affects only log encoding.
	text := strings.ReplaceAll(string(config), `reverse_proxy backend:{$BACKEND_PORT}`, `respond "{http.request.uri}" 200`)
	container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "caddy:2.11-alpine", ExposedPorts: []string{"8080/tcp"},
			Env:        map[string]string{"API_HOST": "http://localhost:8080", "CONSOLE_HOST": "http://console.local:8080"},
			Files:      []testcontainers.ContainerFile{{Reader: strings.NewReader(text), ContainerFilePath: "/etc/caddy/Caddyfile", FileMode: 0644}},
			WaitingFor: wait.ForListeningPort("8080/tcp").WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	if container != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	}
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := container.PortEndpoint(t.Context(), "8080/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	return container, endpoint
}

func edgeRequestLogs(t *testing.T, container testcontainers.Container, count int) string {
	t.Helper()
	for range 100 {
		reader, err := container.Logs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		logs, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(logs), `"uri":"/score"`) >= count {
			return string(logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("edge did not emit the expected access logs")
	return ""
}
