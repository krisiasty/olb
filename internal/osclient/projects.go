package osclient

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/loadbalancers"
	"github.com/gophercloud/gophercloud/v2/pagination"
)

// projNamesTTL bounds how long a resolved project-name map is reused before the
// next all-projects refresh re-enumerates Keystone. Projects change rarely, so a
// few minutes keeps newly-created projects resolvable without per-refresh load.
const projNamesTTL = 5 * time.Minute

// SelectProject resolves the command-line project selector and applies the same
// scoped or global-admin behavior used by the TUI project switcher.
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
	// EnumerationFailed: the configured Keystone project enumeration failed.
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

// ListProjects returns projects appropriate for the configured credential
// strategy. Regular mode discovers scopeable projects through
// GET /v3/auth/projects; explicit global-admin mode uses GET /v3/projects.
func (c *Clients) ListProjects(ctx context.Context) ([]ProjectInfo, error) {
	c.mu.Lock()
	identity := c.services.identity
	globalAdmin := c.globalAdmin
	c.mu.Unlock()
	if globalAdmin {
		ps, err := c.listAllProjects(ctx)
		if err != nil {
			return nil, &SwitchError{
				Kind:    EnumerationFailed,
				Reason:  "Couldn't list all projects from the identity service.",
				Suggest: "Check that --global-admin credentials may use Keystone GET /v3/projects.",
				err:     err,
			}
		}
		return ps, nil
	}
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
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// validateGlobalAdmin fails early when an explicitly global credential cannot
// enumerate Keystone projects or perform a cross-project Octavia read. The
// foreign-project request is bounded to one row and does not mutate cloud state.
func (c *Clients) validateGlobalAdmin(ctx context.Context) error {
	all, err := c.listAllProjects(ctx)
	if err != nil {
		return fmt.Errorf("--global-admin requires Keystone permission to list all projects: %w", err)
	}
	names := mergeProjectNames(all, nil)
	if len(names) > 0 {
		c.mu.Lock()
		c.projNames = names
		c.projNamesAt = time.Now()
		c.mu.Unlock()
	}

	c.mu.Lock()
	startupProjectID := c.services.project.ID
	lb := c.services.lb
	c.mu.Unlock()
	var foreign ProjectInfo
	for _, project := range all {
		if project.ID != startupProjectID {
			foreign = project
			break
		}
	}
	if foreign.ID == "" {
		return nil
	}
	pages := loadbalancers.List(lb, loadbalancers.ListOpts{ProjectID: foreign.ID, Limit: 1})
	if err := pages.EachPage(ctx, func(context.Context, pagination.Page) (bool, error) {
		return false, nil
	}); err != nil {
		return fmt.Errorf("--global-admin requires cross-project Octavia read access: %w", err)
	}
	return nil
}

// projectNameMap returns a best-effort project ID→name map for labeling global
// rows, cached for projNamesTTL. The administrative listing is authoritative;
// regular-mode scope discovery is retained as a graceful fallback.
func (c *Clients) projectNameMap(ctx context.Context) map[string]string {
	c.mu.Lock()
	if c.projNames != nil && time.Since(c.projNamesAt) < projNamesTTL {
		cached := c.projNames
		c.mu.Unlock()
		return cached
	}
	globalAdmin := c.globalAdmin
	c.mu.Unlock()

	// Both enumerations are best-effort. Global mode does not repeat the same
	// administrative request through ListProjects.
	var admin, accessible []ProjectInfo
	if all, err := c.listAllProjects(ctx); err == nil {
		admin = all
	}
	if !globalAdmin {
		if acc, err := c.ListProjects(ctx); err == nil {
			accessible = acc
		}
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

// SwitchProject selects a concrete project. Explicit global-admin mode retains
// the startup clients and changes only the target filter; regular mode obtains
// and activates a new project-scoped service client.
func (c *Clients) SwitchProject(ctx context.Context, target ProjectInfo) error {
	if target.ID == "" {
		return fmt.Errorf("cannot switch to a project without an ID")
	}
	c.mu.Lock()
	if c.globalAdmin {
		c.activeServices = c.services
		c.selected = target
		c.allMode = false
		c.mu.Unlock()
		return nil
	}
	scopeProject := c.scopeProject
	c.mu.Unlock()
	if scopeProject == nil {
		return fmt.Errorf("project-scoped authentication is unavailable")
	}

	scoped, err := scopeProject(ctx, target)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.activeServices = scoped
	c.selected = target
	c.allMode = false
	c.mu.Unlock()
	return nil
}

// EnterAllProjects restores the exact authentication scope with which the
// program started, including any global/admin visibility it provided.
func (c *Clients) EnterAllProjects(_ context.Context) error {
	c.mu.Lock()
	if !c.globalAdmin {
		c.mu.Unlock()
		return fmt.Errorf("all-projects view requires --global-admin")
	}
	c.activeServices = c.services
	c.allMode = true
	c.mu.Unlock()
	return nil
}
