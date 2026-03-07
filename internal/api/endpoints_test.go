package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

func TestAllEndpoints_Count(t *testing.T) {
	endpoints := api.AllEndpoints("org123")
	if len(endpoints) != 21 {
		t.Errorf("expected 21 endpoints, got %d", len(endpoints))
	}
}

func TestAllEndpoints_ContainsOrgID(t *testing.T) {
	endpoints := api.AllEndpoints("myorg")
	for _, ep := range endpoints {
		if ep.Name == "circles" {
			if ep.Path != "/api/organizations/myorg/circles" {
				t.Errorf("unexpected path: %s", ep.Path)
			}
			return
		}
	}
	t.Error("circles endpoint not found")
}

func TestAllEndpoints_AllHaveNames(t *testing.T) {
	endpoints := api.AllEndpoints("org123")
	for _, ep := range endpoints {
		if ep.Name == "" {
			t.Errorf("endpoint with empty name found: %+v", ep)
		}
		if ep.Path == "" {
			t.Errorf("endpoint %q has empty path", ep.Name)
		}
	}
}

func TestDiscoverOrgID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"relationships": map[string]interface{}{
					"organization": map[string]interface{}{
						"data": map[string]string{"id": "org_test123"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "api:test")
	orgID, err := api.DiscoverOrgID(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if orgID != "org_test123" {
		t.Errorf("got %q, want %q", orgID, "org_test123")
	}
}
