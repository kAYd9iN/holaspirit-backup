package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

func TestAllEndpoints_Count(t *testing.T) {
	endpoints := api.AllEndpoints("org123")
	if len(endpoints) != 47 {
		t.Errorf("expected 47 endpoints, got %d", len(endpoints))
	}
}

// Names become file names (<name>.json, <name>-linked.json) and are recorded
// in the manifest verbatim, so they must already be what the storage writer's
// sanitizer would produce, and unique.
func TestAllEndpoints_NamesAreFileSafeAndUnique(t *testing.T) {
	safe := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	seen := map[string]bool{}
	for _, ep := range api.AllEndpoints("org123") {
		if !safe.MatchString(ep.Name) {
			t.Errorf("endpoint name %q is not file-safe", ep.Name)
		}
		if seen[ep.Name] || seen[ep.Name+"-linked"] {
			t.Errorf("endpoint name %q collides with another endpoint or its -linked file", ep.Name)
		}
		seen[ep.Name] = true
		if strings.HasSuffix(ep.Name, "-linked") {
			t.Errorf("endpoint name %q would collide with a -linked file", ep.Name)
		}
	}
}

// The daily drift check must watch every endpoint the tool backs up; a new
// endpoint missing from scripts/check-api-schema.sh would never be monitored.
func TestDriftCheckCoversAllEndpoints(t *testing.T) {
	script, err := os.ReadFile("../../scripts/check-api-schema.sh")
	if err != nil {
		t.Fatal(err)
	}
	const base = "/api/organizations/ORG"
	for _, ep := range api.AllEndpoints("ORG") {
		sub := strings.TrimPrefix(ep.Path, base)
		entry := fmt.Sprintf("%q: %q", ep.Name, sub)
		if !strings.Contains(string(script), entry) {
			t.Errorf("scripts/check-api-schema.sh ENDPOINTS lacks %s", entry)
		}
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
