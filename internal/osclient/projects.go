package osclient

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

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
