package main

import (
	"strings"
	"testing"
)

func TestFirstLineDropsTheLineEnding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"unix line ending", "correct horse battery\nignored", "correct horse battery"},
		{"windows line ending", "correct horse battery\r\n", "correct horse battery"},
		{"no line ending", "correct horse battery", "correct horse battery"},
		{"spaces are kept", " spaced password \n", " spaced password "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := firstLine(strings.NewReader(tc.input))

			if err != nil {
				t.Fatalf("firstLine: %v", err)
			}
			if got != tc.want {
				t.Fatalf("firstLine = %q, want %q", got, tc.want)
			}
		})
	}
}
