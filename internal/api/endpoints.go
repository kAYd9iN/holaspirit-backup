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
	Name      string // used as filename (e.g. "circles" -> circles.json)
	Path      string // full API path
	Paginated bool
}

// AllEndpoints returns all 21 endpoints to back up for a given organization ID.
func AllEndpoints(orgID string) []Endpoint {
	base := fmt.Sprintf("/api/organizations/%s", orgID)
	return []Endpoint{
		{Name: "organization", Path: fmt.Sprintf("/api/organizations/%s", orgID), Paginated: false},
		{Name: "circles", Path: base + "/circles", Paginated: true},
		{Name: "circles-timespent", Path: base + "/circles-timespent", Paginated: true},
		{Name: "roles", Path: base + "/roles", Paginated: true},
		{Name: "members", Path: base + "/members", Paginated: true},
		{Name: "tensions", Path: base + "/tensions", Paginated: true},
		{Name: "policies", Path: base + "/policies", Paginated: true},
		{Name: "meetings", Path: base + "/meetings", Paginated: true},
		{Name: "objectives", Path: base + "/objectives", Paginated: true},
		{Name: "keyresults", Path: base + "/keyresults", Paginated: true},
		{Name: "tasks", Path: base + "/tasks", Paginated: true},
		{Name: "boards", Path: base + "/boards", Paginated: true},
		{Name: "columns", Path: base + "/columns", Paginated: true},
		{Name: "checklists", Path: base + "/checklists", Paginated: true},
		{Name: "metrics", Path: base + "/metrics", Paginated: true},
		{Name: "publications", Path: base + "/publications", Paginated: true},
		{Name: "categories", Path: base + "/categories", Paginated: true},
		{Name: "attachments", Path: base + "/attachments", Paginated: true},
		{Name: "chartviews", Path: base + "/chartviews", Paginated: true},
		{Name: "calendars", Path: base + "/calendars", Paginated: true},
		{Name: "backups", Path: base + "/backups", Paginated: true},
	}
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
