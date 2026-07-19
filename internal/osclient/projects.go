package osclient

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

// projNamesTTL bounds how long a resolved project-name map is reused before the
// next all-projects refresh re-enumerates Keystone. Projects change rarely, so a
// few minutes keeps newly-created projects resolvable without per-refresh load.
const projNamesTTL = 5 * time.Minute

// SelectProject applies the command-line project selector as the same local
// presentation filter used by the TUI. It never changes the token or service
// clients created from --os-project-name / OS_PROJECT_NAME / clouds.yaml.
func (c *Clients) SelectProject(ctx context.Context, selector string) error {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	if current := c.CurrentProject(); current.ID == selector || current.Name == selector {
		return c.SwitchProject(ctx, current)
	}
	available, err := c.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("select initial project %q: %w", selector, err)
	}
	target, err := resolveProjectSelector(available, selector)
	if err != nil {
		return err
	}
	return c.SwitchProject(ctx, target)
}

func resolveProjectSelector(available []ProjectInfo, selector string) (ProjectInfo, error) {
	for _, project := range available {
		if project.ID == selector {
			return project, nil
		}
	}
	var matches []ProjectInfo
	for _, project := range available {
		if project.Name == selector {
			matches = append(matches, project)
		}
	}
	switch len(matches) {
	case 0:
		return ProjectInfo{}, fmt.Errorf("project %q is not accessible; use p in the TUI to see available projects", selector)
	case 1:
		return matches[0], nil
	default:
		return ProjectInfo{}, fmt.Errorf("project name %q is ambiguous; use its project ID instead", selector)
	}
}

// SwitchErrorKind identifies project-selector failures.
type SwitchErrorKind int

const (
	// EnumerationFailed: GET /v3/auth/projects errored (token/endpoint issue).
	EnumerationFailed SwitchErrorKind = iota
)

// SwitchError carries a specific, actionable reason and suggestion.
type SwitchError struct {
	Kind    SwitchErrorKind
	Reason  string
	Suggest string
	Project string
	err     error
}

func (e *SwitchError) Error() string {
	if e.Suggest != "" {
		return e.Reason + " " + e.Suggest
	}
	return e.Reason
}

func (e *SwitchError) Unwrap() error { return e.err }

// ListProjects returns the projects the current token may access, via Keystone
// GET /v3/auth/projects. Unlike `project list` this works for regular
// (non-admin) users, which is why the selector uses it.
func (c *Clients) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	c.mu.Lock()
	identity := c.services.identity
	c.mu.Unlock()
	pages, err := projects.ListAvailable(identity).AllPages(ctx)
	if err != nil {
		return nil, &SwitchError{
			Kind:    EnumerationFailed,
			Reason:  "Couldn't list accessible projects from the identity service.",
			Suggest: "Check that the token is valid and the Keystone endpoint is reachable.",
			err:     err,
		}
	}
	ps, err := projects.ExtractProjects(pages)
	if err != nil {
		return nil, &SwitchError{
			Kind:    EnumerationFailed,
			Reason:  "Couldn't parse the accessible-projects response from the identity service.",
			Suggest: "Check that the token is valid and the Keystone endpoint is reachable.",
			err:     err,
		}
	}
	out := make([]ProjectInfo, 0, len(ps))
	for _, p := range ps {
		if p.IsDomain {
			continue
		}
		out = append(out, ProjectInfo{ID: p.ID, Name: p.Name, DomainID: p.DomainID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// listAllProjects enumerates every project via the admin Keystone listing
// (GET /v3/projects). Unlike ListProjects (GET /v3/auth/projects, limited to the
// token's own assignments) this covers the whole cluster, which is what
// all-projects rows need to show names rather than bare IDs. Requires admin RBAC;
// a 403 is translated to ErrAdminRequired so callers can fall back gracefully.
func (c *Clients) listAllProjects(ctx context.Context) ([]ProjectInfo, error) {
	c.mu.Lock()
	identity := c.services.identity
	c.mu.Unlock()
	pages, err := projects.List(identity, projects.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	ps, err := projects.ExtractProjects(pages)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, 0, len(ps))
	for _, p := range ps {
		if p.IsDomain {
			continue
		}
		out = append(out, ProjectInfo{ID: p.ID, Name: p.Name, DomainID: p.DomainID})
	}
	return out, nil
}

// projectNameMap returns a best-effort project ID→name map for labeling
// all-projects rows, cached for projNamesTTL. The admin full listing is the
// authoritative source (Octavia lists LBs cluster-wide, but the token is usually
// assigned to only a few projects, so the accessible list alone leaves most rows
// showing a bare ID); the accessible list fills any gaps and is the sole source
// for non-admins, where a 403 on the admin listing is expected. An empty map is
// returned (never nil) if both enumerations fail, so rows fall back to their IDs.
func (c *Clients) projectNameMap(ctx context.Context) map[string]string {
	c.mu.Lock()
	if c.projNames != nil && time.Since(c.projNamesAt) < projNamesTTL {
		cached := c.projNames
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	// The admin full listing is authoritative; the accessible list supplements it
	// (and, for non-admins where the admin call 403s, is the sole source). Both
	// enumerations are best-effort — a nil slice from a failed call simply
	// contributes no names.
	var admin, accessible []ProjectInfo
	if all, err := c.listAllProjects(ctx); err == nil {
		admin = all
	}
	if acc, err := c.ListProjects(ctx); err == nil {
		accessible = acc
	}
	names := mergeProjectNames(admin, accessible)

	// Only cache a non-empty result; a total failure shouldn't be pinned for the
	// full TTL when the next refresh might succeed.
	if len(names) > 0 {
		c.mu.Lock()
		c.projNames = names
		c.projNamesAt = time.Now()
		c.mu.Unlock()
	}
	return names
}

// mergeProjectNames builds an ID→name map from the authoritative admin listing
// overlaid by the accessible listing, which only fills IDs the admin list did
// not cover (so admin names win on overlap). Projects without a name are skipped.
func mergeProjectNames(admin, accessible []ProjectInfo) map[string]string {
	names := make(map[string]string, len(admin)+len(accessible))
	for _, p := range admin {
		if p.Name != "" {
			names[p.ID] = p.Name
		}
	}
	for _, p := range accessible {
		if p.Name == "" {
			continue
		}
		if _, ok := names[p.ID]; !ok {
			names[p.ID] = p.Name
		}
	}
	return names
}

// SwitchProject changes only the presentation filter. The original token and
// service clients remain untouched, preserving admin/global authorization.
func (c *Clients) SwitchProject(_ context.Context, target ProjectInfo) error {
	c.mu.Lock()
	c.selected = target
	c.allMode = false
	c.mu.Unlock()
	return nil
}

// EnterAllProjects removes the presentation filter while retaining the exact
// authentication scope with which the program started.
func (c *Clients) EnterAllProjects(_ context.Context) error {
	c.mu.Lock()
	c.allMode = true
	c.mu.Unlock()
	return nil
}
