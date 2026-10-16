package api_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/kAYd9iN/holaspirit-backup/internal/api"
)

// Names become file names (<name>.json, one per endpoint and sub-resource)
// and are recorded in the manifest verbatim, so they must already be what the
// storage writer's sanitizer would produce, and unique.
func TestAllEndpoints_NamesAreFileSafeAndUnique(t *testing.T) {
	safe := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	seen := map[string]bool{"backup-manifest": true}
	check := func(name string) {
		if !safe.MatchString(name) {
			t.Errorf("name %q is not file-safe", name)
		}
		if seen[name] {
			t.Errorf("name %q is used twice", name)
		}
		seen[name] = true
	}
	for _, ep := range api.AllEndpoints() {
		check(ep.Name)
		for _, ch := range ep.Children {
			check(ch.Name)
			if strings.Count(ch.Path, "%s") != 1 {
				t.Errorf("child %q path %q needs exactly one %%s", ch.Name, ch.Path)
			}
		}
	}
}

func TestAllEndpoints_PathsAreV1(t *testing.T) {
	for _, ep := range api.AllEndpoints() {
		if !strings.HasPrefix(ep.Path, "/v1/") {
			t.Errorf("%s: path %q is not under /v1/", ep.Name, ep.Path)
		}
		if ep.Limit > 199 {
			t.Errorf("%s: limit %d exceeds the documented maximum", ep.Name, ep.Limit)
		}
	}
}

// The documented route limits: users ≤ 100, meetings ≤ 50.
func TestAllEndpoints_RouteLimits(t *testing.T) {
	want := map[string]int{"users": 100, "meetings": 50}
	for _, ep := range api.AllEndpoints() {
		if w, ok := want[ep.Name]; ok && (ep.Limit == 0 || ep.Limit > w) {
			t.Errorf("%s: limit %d, documented maximum %d", ep.Name, ep.Limit, w)
		}
	}
}

func TestRequiredScopesAreReadScopes(t *testing.T) {
	scopes := api.RequiredScopes()
	if len(scopes) < 8 {
		t.Fatalf("scopes = %v", scopes)
	}
	for _, s := range scopes {
		if !strings.HasSuffix(s, ":read") {
			t.Errorf("scope %q is not a read scope — the tool must never need write access", s)
		}
	}
}

// route turns an endpoint path into its OpenAPI route ("/v1/goals/{id}/…").
func route(p string) string {
	return strings.ReplaceAll(strings.SplitN(p, "?", 2)[0], "%s", "{id}")
}

// The daily drift check must watch every route the tool backs up; a new
// endpoint missing from scripts/check-api-schema.sh would never be monitored.
func TestDriftCheckCoversAllEndpoints(t *testing.T) {
	script, err := os.ReadFile("../../scripts/check-api-schema.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range api.AllEndpoints() {
		entry := fmt.Sprintf("%q: %q", ep.Name, route(ep.Path))
		if !strings.Contains(string(script), entry) {
			t.Errorf("scripts/check-api-schema.sh ENDPOINTS lacks %s", entry)
		}
		for _, ch := range ep.Children {
			entry := fmt.Sprintf("%q: %q", ch.Name, route(ch.Path))
			if !strings.Contains(string(script), entry) {
				t.Errorf("scripts/check-api-schema.sh ENDPOINTS lacks %s", entry)
			}
		}
	}
}

func TestParseWhoAmI(t *testing.T) {
	w, err := api.ParseWhoAmI([]byte(`{"partner_name":"p","tenant_id":"3f2a-11","scopes":["roles:read"],"user_id":"u"}`))
	if err != nil || w.TenantID != "3f2a-11" || len(w.Scopes) != 1 {
		t.Fatalf("w=%+v err=%v", w, err)
	}
	if _, err := api.ParseWhoAmI([]byte(`{}`)); err == nil {
		t.Error("missing tenant_id must fail")
	}
}

func TestValidateTenantID(t *testing.T) {
	for _, ok := range []string{"3f2a1b2c-0000-4000-8000-000000000000", "tenant_1"} {
		if err := api.ValidateTenantID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../etc", "a b", "x\ny"} {
		if err := api.ValidateTenantID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
