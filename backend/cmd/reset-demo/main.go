package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := reset(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Demo data reset")
}

func reset() error {
	endpoint, err := resetEndpoint(os.Getenv("RESET_URL"), os.Getenv("PORT"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil) // #nosec G704 -- This CLI deliberately contacts the operator-configured backend; resetEndpoint validates its URL.
	if err != nil {
		return fmt.Errorf("invalid reset URL")
	}
	if token := os.Getenv("RESET_SESSION_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if key := os.Getenv("RESET_OPERATOR_KEY"); key != "" {
		req.Header.Set("X-API-Key", key)
	} else {
		return fmt.Errorf("set RESET_OPERATOR_KEY or RESET_SESSION_TOKEN")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req) // #nosec G704 -- The destination is operator configuration, not server input, and redirects are disabled.
	if err != nil {
		return fmt.Errorf("reset request could not complete")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("reset returned HTTP %d", response.StatusCode)
	}
	return nil
}

// resetEndpoint accepts the backend chosen by the operator, never an incoming
// HTTP request. URL validation does not make an arbitrary host trustworthy.
func resetEndpoint(base, port string) (string, error) {
	if base == "" {
		if port == "" {
			port = "8080"
		}
		if !validPort(port) {
			return "", fmt.Errorf("PORT must be between 1 and 65535")
		}
		base = "http://" + net.JoinHostPort("127.0.0.1", port)
	}
	target, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid reset URL")
	}
	if (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.Opaque != "" {
		return "", fmt.Errorf("reset URL must be an absolute HTTP or HTTPS URL")
	}
	if target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || strings.Contains(base, "#") {
		return "", fmt.Errorf("reset URL must not contain user information, a query, or a fragment")
	}
	if strings.HasSuffix(target.Host, ":") || (target.Port() != "" && !validPort(target.Port())) {
		return "", fmt.Errorf("reset URL port must be between 1 and 65535")
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/simulations/reset"
	target.RawPath = ""
	return target.String(), nil
}

func validPort(port string) bool {
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535
}
