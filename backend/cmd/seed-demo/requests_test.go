package main

import (
	"encoding/json"
	"fmt"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport"
	"github.com/nasirlinijat/anti-fraud/backend/internal/transport/mocks"
	"go.uber.org/mock/gomock"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/nasirlinijat/anti-fraud/backend/internal/labels"
)

func TestResolveReviewsLeavesNewestOpenAndUsesLabels(t *testing.T) {
	var reviews []labels.DemoReview
	for id := int64(13); id > 0; id-- {
		reviews = append(reviews, labels.DemoReview{PurchaseID: id, Fraud: id%2 == 0})
	}
	requests := make(chan string, 2*len(reviews))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer admin-token" {
			t.Errorf("unexpected review request: %s %s", r.Method, r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/claim") {
			requests <- r.URL.Path + " claim"
			w.WriteHeader(http.StatusCreated)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 {
			t.Errorf("expected only a review decision, got %v: %v", body, err)
		}
		requests <- fmt.Sprintf("%s %s", r.URL.Path, body["decision"])
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	d := demo{http: server.Client()}
	d.reset.BaseURL = server.URL
	d.reset.SessionToken = "admin-token"
	resolved, err := d.resolveReviews(t.Context(), reviews)
	close(requests)
	var got []string
	for request := range requests {
		got = append(got, request)
	}
	want := []string{"/reviews/3/claim claim", "/reviews/3/decision approve", "/reviews/2/claim claim", "/reviews/2/decision reject", "/reviews/1/claim claim", "/reviews/1/decision approve"}
	if err != nil || resolved != 3 || !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved %d, requests %v, error %v; want %v", resolved, got, err, want)
	}
}

func TestServicePreflightNeverScoresAPurchase(t *testing.T) {
	for _, token := range []string{"service-key", "wrong", ""} {
		t.Run(token, func(t *testing.T) {
			service := mocks.NewMockScoreService(gomock.NewController(t))
			mux := http.NewServeMux()
			transport.RegisterScoreRoutes(mux, transport.NewScoreHandler(service), "service-key")
			server := httptest.NewServer(mux)
			defer server.Close()
			d := demo{serviceKey: token, http: server.Client()}
			d.reset.BaseURL = server.URL
			err := d.checkServiceKey(t.Context())
			if (err == nil) != (token == "service-key") {
				t.Fatalf("preflight: %v", err)
			}
		})
	}
}

func TestSeedRequiresAnAdminSession(t *testing.T) {
	for _, role := range []string{"admin", "reviewer", ""} {
		t.Run(role, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/auth/me" || r.Header.Get("Authorization") != "Bearer seed-token" {
					t.Error("wrong session request")
				}
				json.NewEncoder(w).Encode(map[string]string{"role": role})
			}))
			defer server.Close()
			d := demo{http: server.Client()}
			d.reset.BaseURL = server.URL
			d.reset.SessionToken = "seed-token"
			if err := d.checkAdmin(t.Context()); (err == nil) != (role == "admin") {
				t.Fatalf("admin check: %v", err)
			}
		})
	}
}
