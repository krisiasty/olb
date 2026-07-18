package osclient

import (
	"context"
	"fmt"
	"sort"

	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

// SwitchErrorKind distinguishes the failure points the spec insists must not be
// conflated: a user shown a project list who then can't switch needs a
// different message than one who couldn't list projects at all.
type SwitchErrorKind int

const (
	// EnumerationFailed: GET /v3/auth/projects errored (token/endpoint issue).
	EnumerationFailed SwitchErrorKind = iota
	// NoRoleOnProject: enumeration and switching both work, but the re-scope
	// auth request was rejected for this specific project.
	NoRoleOnProject
	// CannotReScope: the auth method itself forbids re-scoping.
	CannotReScope
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
	identity := c.sel.identity
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

// SwitchProject re-authenticates with a token scoped to the chosen project and
// rebuilds the service clients against it. A project-scoped token's scope is
// immutable, so switching is a fresh authentication, not a mutation of the
// current token; it therefore requires the retained credentials.
func (c *Clients) SwitchProject(ctx context.Context, target ProjectInfo) error {
	if !c.Switch.CanSwitch {
		return &SwitchError{Kind: CannotReScope, Reason: c.Switch.Reason, Suggest: c.Switch.Suggest, Project: target.Name}
	}

	sc, err := c.buildScoped(ctx, target.ID)
	if err != nil {
		return &SwitchError{
			Kind:    NoRoleOnProject,
			Reason:  fmt.Sprintf("Your account doesn't have a role on project %q.", displayName(target)),
			Suggest: "Ask an administrator to grant access, or pick a different project.",
			Project: target.Name,
			err:     err,
		}
	}
	if sc.project.Name == "" {
		sc.project.Name = target.Name
	}

	c.mu.Lock()
	c.sel = sc
	c.allMode = false
	c.scoped = map[string]*serviceClients{sc.project.ID: sc}
	c.lbProject = map[string]ProjectInfo{}
	c.mu.Unlock()
	return nil
}

// EnterAllProjects switches the tool into all-projects mode: subsequent listing
// aggregates every load balancer the user can see (the admin global list plus a
// per-project sweep of role-assigned projects), and drilling into one re-scopes
// to its owning project on demand where needed.
//
// It does not require re-scoping capability: an admin's global list works from
// the current scope alone, and the per-project sweep simply degrades (is
// skipped) when credentials can't re-scope.
func (c *Clients) EnterAllProjects(ctx context.Context) error {
	c.mu.Lock()
	c.allMode = true
	c.lbProject = map[string]ProjectInfo{}
	c.mu.Unlock()
	return nil
}

func displayName(p ProjectInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}
