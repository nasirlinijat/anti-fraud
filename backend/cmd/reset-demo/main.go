package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
	base := os.Getenv("RESET_URL")
	if base == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		base = "http://127.0.0.1:" + port
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/simulations/reset", nil)
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
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reset request could not complete")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("reset returned HTTP %d", response.StatusCode)
	}
	return nil
}
