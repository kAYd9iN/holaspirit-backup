package api

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// BaseURL is the production host of the Talkspirit Partner API.
const BaseURL = "https://partner-api.talkspirit.com"

// validTenantID matches the tenant identifiers /v1/whoami returns (UUIDs,
// numeric IDs, slugs). It is recorded in the manifest, so it is format-checked
// before use.
var validTenantID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-]{0,63}$`)

// ValidateTenantID returns an error if id is not a plausible tenant identifier.
func ValidateTenantID(id string) error {
	if !validTenantID.MatchString(id) {
		return fmt.Errorf("invalid tenant ID %q: must be 1-64 alphanumeric chars with hyphens/underscores", id)
	}
	return nil
}

// Endpoint describes one top-level Partner API collection to back up.
type Endpoint struct {
	Name string // used as filename (e.g. "circles" -> circles.json); [a-z0-9-] only
	Path string // path below the base URL, including fixed query parameters
	// Limit is the page size to request (0 = DefaultLimit). Some routes accept
	// less than the usual maximum of 199.
	Limit int
	// Scope is the read scope the key needs for this endpoint (documentation
	// and the startup scope check).
	Scope string
	// Optional, when set, explains why the endpoint may legitimately be
	// refused (admin-only data). HTTP 401/403/404 then marks it "unavailable"
	// instead of failing the backup.
	Optional string
	// Children are per-item sub-resources fetched for every item of this
	// collection (members of each circle, agenda of each meeting, ...).
	Children []Child
}

// Child is a sub-resource fetched once per parent item.
type Child struct {
	Name string // file name, like Endpoint.Name
	// Path is a format string with one %s for the parent's id.
	Path  string
	Limit int
	// OnlyIf names a numeric parent field that must be > 0 for the request to
	// be worth making (e.g. subtask_count); "" fetches for every parent.
	OnlyIf string
	// Recursive: the child items are of the parent's kind and get the same
	// child fetched for them in turn (sub-tasks of sub-tasks).
	Recursive bool
	// Detail marks expensive per-item fetches (one request per item) that
	// --quick skips.
	Detail bool
	// Optional, like Endpoint.Optional, for a sub-resource that may be
	// refused for some parents.
	Optional string
}

const taskFields = "include=assignees,attachments,description,labels"

// endpoints lists every read endpoint of the published Partner API contract
// (/v1/openapi.json) that holds organization data. Keep it in sync with the
// ENDPOINTS map in scripts/check-api-schema.sh (enforced by
// TestDriftCheckCoversAllEndpoints).
//
// Deliberately not backed up (see docs/architecture.md):
//   - /v1/structure-exports: needs POST to start an export; the client is
//     GET-only by design, and the export holds the same structure as
//     circles/roles/memberships
//   - /v1/*/{id} detail routes: same fields as the list routes
//   - /healthz: diagnostics
var endpoints = []Endpoint{
	{Name: "whoami", Path: "/v1/whoami"},
	{Name: "custom-fields-circle", Path: "/v1/custom-fields?entity_type=circle", Scope: "circles:read"},
	{Name: "custom-fields-role", Path: "/v1/custom-fields?entity_type=role", Scope: "roles:read"},
	{Name: "custom-fields-user", Path: "/v1/custom-fields?entity_type=user", Scope: "users:read"},

	// Governance structure
	{Name: "circles", Path: "/v1/circles?include=custom_fields", Scope: "circles:read",
		Children: []Child{{Name: "circle-members", Path: "/v1/circles/%s/members"}}},
	{Name: "roles", Path: "/v1/roles?include=custom_fields,members", Scope: "roles:read"},
	{Name: "role-templates", Path: "/v1/role-templates", Scope: "roles:read"},
	{Name: "users", Path: "/v1/users?include=custom_fields", Limit: 100, Scope: "users:read"},
	{Name: "memberships", Path: "/v1/memberships", Scope: "users:read"},

	// Operations
	{Name: "tensions", Path: "/v1/tensions?include=links", Scope: "tensions:read"},
	// Without ?status= the API leaves out cancelled meetings.
	{Name: "meetings", Path: "/v1/meetings?status=DRAFT,SCHEDULED,IN_PROGRESS,COMPLETED,CANCELLED", Limit: 50, Scope: "meetings:read",
		Children: []Child{{Name: "meeting-agenda-items", Path: "/v1/meetings/%s/agenda-items", Detail: true}}},
	// /v1/tasks lists top-level tasks only; sub-tasks come per parent.
	{Name: "tasks", Path: "/v1/tasks?archived_status=all&" + taskFields, Scope: "tasks:read",
		Children: []Child{
			{Name: "subtasks", Path: "/v1/tasks?archived_status=all&" + taskFields + "&parent_task_id=%s", OnlyIf: "subtask_count", Recursive: true},
			{Name: "task-comments", Path: "/v1/tasks/%s/comments", Detail: true},
		}},
	{Name: "projects", Path: "/v1/projects?archived_status=all&include=assignees", Scope: "projects:read",
		Children: []Child{
			{Name: "sections", Path: "/v1/sections?project_id=%s"},
			{Name: "labels", Path: "/v1/labels?project_id=%s"},
			{Name: "project-comments", Path: "/v1/projects/%s/comments", Detail: true},
		}},
	{Name: "goals", Path: "/v1/goals?archived_status=all", Scope: "goals:read",
		Children: []Child{
			{Name: "goal-key-results", Path: "/v1/goals/%s/key-results"},
			{Name: "goal-comments", Path: "/v1/goals/%s/comments", Detail: true},
		}},
	{Name: "time-periods", Path: "/v1/time-periods", Scope: "goals:read"},
	{Name: "documents", Path: "/v1/documents?archived_status=all", Scope: "documents:read",
		Children: []Child{
			{Name: "document-content", Path: "/v1/documents/%s/content", Optional: "no readable content version"},
			{Name: "document-comments", Path: "/v1/documents/%s/comments", Detail: true},
		}},
	{Name: "audit-logs", Path: "/v1/audit-logs", Scope: "audit_log:read",
		Optional: "needs the audit_log:read scope and an organization-admin key"},
}

// AllEndpoints returns every endpoint to back up.
func AllEndpoints() []Endpoint {
	out := make([]Endpoint, len(endpoints))
	copy(out, endpoints)
	return out
}

// RequiredScopes returns the read scopes a key needs for a complete backup.
func RequiredScopes() []string {
	seen := map[string]bool{}
	var out []string
	for _, ep := range endpoints {
		if ep.Scope != "" && !seen[ep.Scope] {
			seen[ep.Scope] = true
			out = append(out, ep.Scope)
		}
	}
	return out
}

// WhoAmI is the response of GET /v1/whoami.
type WhoAmI struct {
	PartnerName     string   `json:"partner_name"`
	TenantID        string   `json:"tenant_id"`
	Scopes          []string `json:"scopes"`
	UserID          string   `json:"user_id"`
	CreatedByUserID *string  `json:"created_by_user_id"`
}

// ParseWhoAmI decodes a /v1/whoami response.
func ParseWhoAmI(body []byte) (*WhoAmI, error) {
	var w WhoAmI
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("parse /v1/whoami: %w", err)
	}
	if w.TenantID == "" {
		return nil, fmt.Errorf("/v1/whoami returned no tenant_id")
	}
	return &w, nil
}
