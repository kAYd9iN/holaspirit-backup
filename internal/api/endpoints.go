package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

// validOrgID matches alphanumeric IDs with hyphens and underscores (UUIDs, numeric IDs, slugs).
var validOrgID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-]{0,63}$`)

// ValidateOrgID returns an error if orgID contains characters that could alter URL routing.
func ValidateOrgID(orgID string) error {
	if !validOrgID.MatchString(orgID) {
		return fmt.Errorf("invalid organization ID %q: must be 1-64 alphanumeric chars with hyphens/underscores", orgID)
	}
	return nil
}

// Endpoint describes a single API resource to back up.
type Endpoint struct {
	Name string // used as filename (e.g. "circles" -> circles.json); [a-z0-9-] only
	Path string // full API path
	// Paginated marks list endpoints whose published response schema carries
	// pagination metadata. They are fetched page by page and their `linked`
	// entities are written to <name>-linked.json. Other endpoints are stored
	// as the complete response.
	Paginated bool
	// ByCreated: fetch in createdAfter/createdBefore time windows, because the
	// endpoint can hold more than ResultCap items (tensions).
	ByCreated bool
	// Optional, when set, explains why the endpoint may legitimately be absent
	// for an organization (a feature that is not enabled). HTTP 400/403/404 then
	// marks it "unavailable" instead of failing the backup.
	Optional string
}

// byCreated lists endpoints that exceeded ResultCap in a live run and support
// the createdAfter/createdBefore filters.
var byCreated = map[string]bool{"tensions": true, "node-tensions": true}

// optional lists feature-dependent endpoints (live run: 404/400 for an
// organization without these features).
var optional = map[string]string{
	"translations":        "translation feature not enabled for this organization",
	"node-translations":   "translation feature not enabled for this organization",
	"chartviews":          "saved chart views not available for this token/organization",
	"authentication-saml": "SAML single sign-on not configured",
}

// orgCollections lists every organization-level GET collection of the
// published Holaspirit API that holds organization data, as name → path below
// /api/organizations/{id}. Keep it in sync with the ENDPOINTS map in
// scripts/check-api-schema.sh (enforced by TestDriftCheckCoversAllEndpoints).
//
// Deliberately not backed up (see docs/architecture.md):
//   - tokens: API secrets
//   - stripe/current: billing, not organization data
//   - favorites: personal data of the token's user
//   - search-global, search/mention, migration/domain-availability: queries, not stored data
//   - jobs, record: transient job/recording state
//   - domains: needs a role parameter; the content is in roles-linked.json
var orgCollections = []struct {
	name, sub string
	paginated bool
}{
	{"organization", "", false},
	{"circles", "/circles", true},
	{"circles-timespent", "/circles-timespent", true},
	{"roles", "/roles", true},
	{"members", "/members", true},
	{"tensions", "/tensions", true},
	{"policies", "/policies", true},
	{"meetings", "/meetings", true},
	// objectives/keyresults were removed from the published API —
	// replaced by the OKR endpoints (key results are nested in OKRs).
	{"okrs", "/okrs", true},
	{"node-okrs", "/node-okrs", true},
	{"tasks", "/tasks", true},
	{"boards", "/boards", true},
	{"columns", "/columns", true},
	{"checklists", "/checklists", true},
	{"metrics", "/metrics", true},
	{"publications", "/publications", true},
	{"categories", "/categories", true},
	{"attachments", "/attachments", true},
	{"chartviews", "/chartviews", true},
	{"calendars", "/calendars", true},
	{"backups", "/backups", true},

	// Governance structure and configuration
	{"custom-fields", "/custom-fields", true},
	{"roles-custom-fields", "/roles-custom-fields", true},
	{"roles-relations", "/roles/relations", false},
	{"roles-diagram", "/roles-diagram", false},
	{"members-hierarchy", "/members-hierarchy", true},
	{"labels", "/labels", true},
	{"highlights", "/highlights", true},
	{"timeframes", "/timeframes", true},
	{"templates", "/templates", false},
	{"meetingtemplates", "/meetingtemplates", true},
	{"meetings-recurrences", "/meetings/recurrences", true},
	{"translations", "/translations", true},

	// Publications: revision history and comment threads
	{"publications-revision", "/publications/revision", true},
	{"comment-feeds", "/comment-feeds", true},

	// Node (tree) views of the organization
	{"node-boards", "/node-boards", true},
	{"node-circles", "/node-circles", true},
	{"node-meetings", "/node-meetings", true},
	{"node-members", "/node-members", true},
	{"node-roles", "/node-roles", true},
	{"node-roles-custom-fields", "/node-roles-custom-fields", true},
	{"node-roles-diagram", "/node-roles-diagram", false},
	{"node-tasks", "/node-tasks", true},
	{"node-tensions", "/node-tensions", true},
	{"node-translations", "/node-translations", true},

	// Integrations and authentication setup (admin token required)
	{"webhooks", "/webhooks", true},
	{"authentication-saml", "/authentication/saml", false},
}

// AllEndpoints returns every endpoint to back up for a given organization ID.
func AllEndpoints(orgID string) []Endpoint {
	base := fmt.Sprintf("/api/organizations/%s", orgID)
	eps := make([]Endpoint, 0, len(orgCollections))
	for _, c := range orgCollections {
		eps = append(eps, Endpoint{Name: c.name, Path: base + c.sub, Paginated: c.paginated,
			ByCreated: byCreated[c.name], Optional: optional[c.name]})
	}
	return eps
}

// MeResponse is the response from GET /api/me.
type MeResponse struct {
	Data struct {
		Relationships struct {
			Organization struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"organization"`
		} `json:"relationships"`
	} `json:"data"`
}

// DiscoverOrgID fetches the organization ID from the /api/me endpoint.
func DiscoverOrgID(ctx context.Context, client *Client) (string, error) {
	body, err := client.Get(ctx, "/api/me")
	if err != nil {
		return "", fmt.Errorf("GET /api/me: %w", err)
	}
	var me MeResponse
	if err := json.Unmarshal(body, &me); err != nil {
		return "", fmt.Errorf("parse /api/me: %w", err)
	}
	orgID := me.Data.Relationships.Organization.Data.ID
	if orgID == "" {
		return "", fmt.Errorf("organization ID not found in /api/me response")
	}
	return orgID, nil
}
