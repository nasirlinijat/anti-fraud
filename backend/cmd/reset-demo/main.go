package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nasirlinijat/anti-fraud/backend/internal/demoreset"
)

func main() {
	if err := demoreset.Reset(context.Background(), demoreset.Config{
		BaseURL: os.Getenv("RESET_URL"), Port: os.Getenv("PORT"),
		OperatorKey: os.Getenv("RESET_OPERATOR_KEY"), SessionToken: os.Getenv("RESET_SESSION_TOKEN"),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Demo data reset")
}
