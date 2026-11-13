package main

import (
	"reflect"
	"testing"
	"time"
)

func TestDemoPurchasesAreRepeatableAndCoverSevenDays(t *testing.T) {
	first, err := demoPurchases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := demoPurchases(t.Context())
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated generation differs: %v", err)
	}
	if len(first) < 100 {
		t.Fatalf("generated only %d purchases", len(first))
	}
	start := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)
	var days [7]bool
	for _, purchase := range first {
		elapsed := purchase.Request.EventTime.Sub(start)
		if elapsed < 0 || elapsed >= 7*24*time.Hour {
			t.Fatalf("purchase falls outside demo week: %s", purchase.Request.EventTime)
		}
		days[elapsed/(24*time.Hour)] = true
	}
	for day, hasPurchases := range days {
		if !hasPurchases {
			t.Errorf("day %d has no purchases", day+1)
		}
	}
}
