package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-monitor/internal/domain"
)

func TestSub2APIProbePreservesUpstreamFailures(t *testing.T) {
	for _, status := range []int{200, 401, 403, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/auth/me" || r.Header.Get("Authorization") != "Bearer valid-token" {
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<html>upstream gateway page</html>"))
			}))
			defer server.Close()
			c := &sub2APIUserConnector{client: server.Client()}
			result, err := c.Test(context.Background(), domain.Instance{BaseURL: server.URL, Credential: &domain.Credential{Value: "  Bearer valid-token  "}})
			if err == nil || result.OK || !strings.Contains(result.Message, "upstream status") {
				t.Fatalf("expected actionable failure: %#v, %v", result, err)
			}
			encoded, marshalErr := json.Marshal(result)
			if marshalErr != nil || !json.Valid(encoded) {
				t.Fatalf("probe cannot reach browser: %v", marshalErr)
			}
			if calls != 1 {
				t.Fatalf("overwrote original failure with %d requests", calls)
			}
		})
	}
}

func TestSub2APIAuthTokenReadsBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer browser-token" {
			t.Error("missing browser token")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/me" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":2678,"balance":0.42114473}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c := &sub2APIUserConnector{client: server.Client()}
	targets, err := c.Discover(context.Background(), domain.Instance{BaseURL: server.URL, Credential: &domain.Credential{JSON: map[string]any{"auth_token": "browser-token"}}})
	if err != nil || len(targets) == 0 || targets[0].Balance == nil || targets[0].Balance.Amount != 0.42114473 {
		t.Fatalf("balance not discovered: %#v, %v", targets, err)
	}
}
