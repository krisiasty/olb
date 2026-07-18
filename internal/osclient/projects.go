package osclient

import (
	"context"
	"fmt"
	"sort"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
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
	pages, err := projects.ListAvailable(c.Identity).AllPages(ctx)
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

	ao := c.baseAuth
	// Re-scope with credentials, never by re-presenting the old scoped token.
	ao.TokenID = ""
	ao.Scope = &gophercloud.AuthScope{ProjectID: target.ID}
	ao.AllowReauth = true

	provider, err := openstack.AuthenticatedClient(ctx, ao)
	if err != nil {
		return &SwitchError{
			Kind:    NoRoleOnProject,
			Reason:  fmt.Sprintf("Your account doesn't have a role on project %q.", displayName(target)),
			Suggest: "Ask an administrator to grant access, or pick a different project.",
			Project: target.Name,
			err:     err,
		}
	}

	lb, err := openstack.NewLoadBalancerV2(provider, c.endpoint)
	if err != nil {
		return &SwitchError{Kind: NoRoleOnProject, Reason: "Switched project has no Octavia endpoint.", Project: target.Name, err: err}
	}
	identity, err := openstack.NewIdentityV3(provider, c.endpoint)
	if err != nil {
		return &SwitchError{Kind: NoRoleOnProject, Reason: "Switched project has no identity endpoint.", Project: target.Name, err: err}
	}

	c.Provider = provider
	c.LB = lb
	c.Identity = identity
	c.Network, _ = openstack.NewNetworkV2(provider, c.endpoint)
	c.Compute, _ = openstack.NewComputeV2(provider, c.endpoint)
	c.Project = currentProject(provider)
	if c.Project.ID == "" {
		c.Project = target
	}
	return nil
}

func displayName(p ProjectInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}
