package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
)

func TestResolveReviewsLeavesNewestOpenAndUsesLabels(t *testing.T) {
	var reviews []labels.DemoReview
	for id := int64(13); id > 0; id-- {
		reviews = append(reviews, labels.DemoReview{PurchaseID: id, Fraud: id%2 == 0})
	}
	requests := make(chan string, len(reviews))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-API-Key") != "analyst" {
			t.Errorf("unexpected review request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 {
			t.Errorf("expected only a review decision, got %v: %v", body, err)
		}
		requests <- fmt.Sprintf("%s %s", r.URL.Path, body["decision"])
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	d := demo{analystKey: "analyst", http: server.Client()}
	d.reset.BaseURL = server.URL
	resolved, err := d.resolveReviews(t.Context(), reviews)
	close(requests)
	var got []string
	for request := range requests {
		got = append(got, request)
	}
	want := []string{"/reviews/3/decision approve", "/reviews/2/decision reject", "/reviews/1/decision approve"}
	if err != nil || resolved != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved %d, requests %v, error %v; want %v", resolved, got, err, want)
	}
}
