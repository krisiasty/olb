package osclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

// projNamesTTL bounds how long resolved project names are reused.
const projNamesTTL = 5 * time.Minute

// SelectProject resolves the command-line project shortcut against the user's
// available project scopes and authenticates to the selected scope.
func (c *Clients) SelectProject(ctx context.Context, selector string) error {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	if current := c.CurrentScope(); current.Kind == ScopeProject &&
		(current.ID == selector || current.Name == selector) {
		return nil
	}
	scopes, err := c.ListScopes(ctx)
	if err != nil {
		return fmt.Errorf("select initial project %q: %w", selector, err)
	}
	available := make([]ProjectInfo, 0, len(scopes))
	scopeByID := make(map[string]ScopeInfo, len(scopes))
	for _, scope := range scopes {
		if scope.Kind != ScopeProject {
			continue
		}
		available = append(available, ProjectInfo{
			ID: scope.ID, Name: scope.Name, DomainID: scope.DomainID,
		})
		scopeByID[scope.ID] = scope
	}
	target, err := resolveProjectSelector(available, selector)
	if err != nil {
		return err
	}
	return c.SwitchScope(ctx, scopeByID[target.ID])
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
		return ProjectInfo{}, fmt.Errorf("project %q is not accessible; use p in the TUI to see available scopes", selector)
	case 1:
		return matches[0], nil
	default:
		return ProjectInfo{}, fmt.Errorf("project name %q is ambiguous; use its project ID instead", selector)
	}
}

// projectNameMap labels resource rows and identity relations using only
// projects visible to the active token scope.
func (c *Clients) projectNameMap(ctx context.Context) map[string]string {
	c.mu.Lock()
	if c.projNames != nil && time.Since(c.projNamesAt) < projNamesTTL {
		cached := c.projNames
		c.mu.Unlock()
		return cached
	}
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()
	if sc == nil || sc.identity == nil {
		return nil
	}

	pager := projects.ListAvailable(sc.identity)
	switch scope.Kind {
	case ScopeSystem:
		pager = projects.List(sc.identity, projects.ListOpts{})
	case ScopeDomain:
		pager = projects.List(sc.identity, projects.ListOpts{DomainID: scope.ID})
	}
	pages, err := pager.AllPages(ctx)
	if err != nil {
		return nil
	}
	items, err := projects.ExtractProjects(pages)
	if err != nil {
		return nil
	}
	names := make(map[string]string, len(items))
	for _, project := range items {
		if !project.IsDomain && project.ID != "" && project.Name != "" {
			names[project.ID] = project.Name
		}
	}
	if len(names) > 0 {
		c.mu.Lock()
		c.projNames = names
		c.projNamesAt = time.Now()
		c.mu.Unlock()
	}
	return names
}

// mergeProjectNames combines two best-effort sources, preferring primary.
func mergeProjectNames(primary, fallback []ProjectInfo) map[string]string {
	names := make(map[string]string, len(primary)+len(fallback))
	for _, list := range [][]ProjectInfo{primary, fallback} {
		for _, project := range list {
			if project.ID == "" || project.Name == "" {
				continue
			}
			if _, exists := names[project.ID]; !exists {
				names[project.ID] = project.Name
			}
		}
	}
	return names
}
