// Package osclient wires OpenStack authentication and the Octavia / Neutron /
// Nova / Keystone service clients, and exposes the data operations the TUI
// needs (list load balancers, fetch a status tree, load per-object detail,
// list accessible projects, re-scope to another project).
//
// Auth sources follow python-openstackclient conventions so existing
// credentials work unchanged: OS_* environment variables, clouds.yaml (selected
// via --os-cloud / OS_CLOUD), and CLI flags. Precedence is CLI > env >
// clouds.yaml, achieved by overlaying CLI flags onto the environment before
// handing off to gophercloud's clientconfig, which already resolves env over
// clouds.yaml.
package osclient

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
)

// Options holds the auth-related inputs captured from CLI flags. Empty fields
// are treated as "not provided" and fall through to env / clouds.yaml.
type Options struct {
	Cloud   string // --os-cloud / OS_CLOUD
	Region  string // --os-region-name / OS_REGION_NAME
	Project string // --project: initial project scope (name), an alias for OS_PROJECT_NAME

	AuthURL           string
	Username          string
	Password          string
	UserDomainName    string
	ProjectName       string
	ProjectID         string
	ProjectDomainName string
	Token             string

	ApplicationCredentialID     string
	ApplicationCredentialName   string
	ApplicationCredentialSecret string
}

// applyToEnv overlays the non-empty CLI options onto the process environment so
// that clientconfig's env>clouds.yaml resolution yields CLI>env>clouds.yaml.
func (o Options) applyToEnv() {
	set := func(k, v string) {
		if v != "" {
			_ = os.Setenv(k, v)
		}
	}
	set("OS_AUTH_URL", o.AuthURL)
	set("OS_USERNAME", o.Username)
	set("OS_PASSWORD", o.Password)
	set("OS_USER_DOMAIN_NAME", o.UserDomainName)
	set("OS_PROJECT_NAME", o.ProjectName)
	set("OS_PROJECT_ID", o.ProjectID)
	set("OS_PROJECT_DOMAIN_NAME", o.ProjectDomainName)
	set("OS_TOKEN", o.Token)
	set("OS_APPLICATION_CREDENTIAL_ID", o.ApplicationCredentialID)
	set("OS_APPLICATION_CREDENTIAL_NAME", o.ApplicationCredentialName)
	set("OS_APPLICATION_CREDENTIAL_SECRET", o.ApplicationCredentialSecret)
	set("OS_REGION_NAME", o.Region)
	// --project is the user-facing initial-scope selector; it wins over
	// --os-project-name when both are given.
	set("OS_PROJECT_NAME", o.Project)
}

// ProjectInfo identifies a project scope.
type ProjectInfo struct {
	ID       string
	Name     string
	DomainID string
}

// SwitchCapability describes whether the current auth method permits switching
// to another project, and — when it does not — a specific reason and suggestion.
// Determined up front, right after auth, so the selector can be shown disabled
// with the reason inline rather than hard-failing mid-flow.
type SwitchCapability struct {
	CanSwitch bool
	Reason    string
	Suggest   string
}

// serviceClients is a set of OpenStack service clients scoped to one project.
type serviceClients struct {
	provider *gophercloud.ProviderClient
	lb       *gophercloud.ServiceClient // Octavia (required)
	identity *gophercloud.ServiceClient // Keystone v3 (required)
	network  *gophercloud.ServiceClient // Neutron (optional; floating IPs)
	compute  *gophercloud.ServiceClient // Nova (optional; member instances)
	project  ProjectInfo
}

// Clients holds the current project selection plus, for all-projects mode, a
// cache of per-project scoped clients. A project-scoped token's scope is
// immutable, so a non-admin can only read a load balancer while scoped to its
// owning project; all-projects mode therefore keeps one scoped client set per
// accessible project and picks the right one per operation.
type Clients struct {
	Region string
	Switch SwitchCapability

	// baseAuth retains the resolved credentials (sans a fixed scope) so a
	// project switch can request a new scoped token. Holding the password in
	// memory is inherent to re-scoping and is the documented trade-off.
	baseAuth gophercloud.AuthOptions
	endpoint gophercloud.EndpointOpts

	mu        sync.Mutex
	sel       *serviceClients            // current single-project selection
	scoped    map[string]*serviceClients // projectID -> scoped clients (cache)
	allMode   bool                       // list across all accessible projects
	lbProject map[string]ProjectInfo     // lbID -> owning project (all-projects mode)
}

// Authenticate resolves credentials from CLI/env/clouds.yaml, authenticates,
// builds the service clients, and determines the project-switch capability.
func Authenticate(ctx context.Context, o Options) (*Clients, error) {
	o.applyToEnv()

	cloud := o.Cloud
	if cloud == "" {
		cloud = os.Getenv("OS_CLOUD")
	}
	region := o.Region
	if region == "" {
		region = os.Getenv("OS_REGION_NAME")
	}

	ao, err := clientconfig.AuthOptions(&clientconfig.ClientOpts{Cloud: cloud, RegionName: region})
	if err != nil {
		return nil, fmt.Errorf("resolving OpenStack credentials: %w", err)
	}
	if ao.IdentityEndpoint == "" {
		return nil, fmt.Errorf("no auth URL found: set OS_AUTH_URL, --os-auth-url, or select a cloud with --os-cloud")
	}
	ao.AllowReauth = true

	c := &Clients{
		Region:    region,
		Switch:    detectSwitchCapability(ao),
		baseAuth:  *ao,
		endpoint:  gophercloud.EndpointOpts{Region: region, Availability: gophercloud.AvailabilityPublic},
		scoped:    map[string]*serviceClients{},
		lbProject: map[string]ProjectInfo{},
	}

	// Initial login: use the credentials' own scope (empty projectID).
	sc, err := c.buildScoped(ctx, "")
	if err != nil {
		return nil, err
	}
	c.sel = sc
	if sc.project.ID != "" {
		c.scoped[sc.project.ID] = sc
	}
	return c, nil
}

// buildScoped authenticates and builds the service clients scoped to projectID.
// An empty projectID uses the base credentials' own scope (the initial login);
// a non-empty projectID re-scopes with the retained credentials.
func (c *Clients) buildScoped(ctx context.Context, projectID string) (*serviceClients, error) {
	ao := c.baseAuth
	if projectID != "" {
		ao.TokenID = "" // re-scope with credentials, never the old scoped token
		ao.Scope = &gophercloud.AuthScope{ProjectID: projectID}
	}
	ao.AllowReauth = true

	provider, err := openstack.AuthenticatedClient(ctx, ao)
	if err != nil {
		return nil, fmt.Errorf("authenticating to OpenStack: %w", err)
	}
	sc := &serviceClients{provider: provider}
	if sc.lb, err = openstack.NewLoadBalancerV2(provider, c.endpoint); err != nil {
		return nil, fmt.Errorf("no Octavia (load-balancer) endpoint in the service catalog: %w", err)
	}
	if sc.identity, err = openstack.NewIdentityV3(provider, c.endpoint); err != nil {
		return nil, fmt.Errorf("no Keystone (identity) endpoint in the service catalog: %w", err)
	}
	// Neutron and Nova are optional: their absence degrades the floating-IP and
	// member-instance edges gracefully rather than being fatal.
	sc.network, _ = openstack.NewNetworkV2(provider, c.endpoint)
	sc.compute, _ = openstack.NewComputeV2(provider, c.endpoint)

	sc.project = currentProject(provider)
	if sc.project.ID == "" && projectID != "" {
		sc.project = ProjectInfo{ID: projectID}
	}
	return sc, nil
}

// scopedClients returns cached-or-built service clients scoped to proj.
func (c *Clients) scopedClients(ctx context.Context, proj ProjectInfo) (*serviceClients, error) {
	c.mu.Lock()
	if sc, ok := c.scoped[proj.ID]; ok {
		c.mu.Unlock()
		return sc, nil
	}
	sel := c.sel
	c.mu.Unlock()
	if proj.ID == "" || (sel != nil && proj.ID == sel.project.ID) {
		return sel, nil
	}
	sc, err := c.buildScoped(ctx, proj.ID)
	if err != nil {
		return nil, err
	}
	if sc.project.Name == "" {
		sc.project.Name = proj.Name
	}
	c.mu.Lock()
	c.scoped[proj.ID] = sc
	c.mu.Unlock()
	return sc, nil
}

// clientsForLB returns the service clients scoped to the project owning lbID.
// In single-project mode that is always the current selection; in all-projects
// mode it (re-)scopes to the LB's project so a non-admin can read it.
func (c *Clients) clientsForLB(ctx context.Context, lbID string) (*serviceClients, error) {
	c.mu.Lock()
	allMode := c.allMode
	proj, ok := c.lbProject[lbID]
	sel := c.sel
	c.mu.Unlock()
	if !allMode || !ok || (sel != nil && proj.ID == sel.project.ID) {
		return sel, nil
	}
	return c.scopedClients(ctx, proj)
}

// currentProject extracts the scoped project from the authentication result.
func currentProject(provider *gophercloud.ProviderClient) ProjectInfo {
	ar := provider.GetAuthResult()
	if ar == nil {
		return ProjectInfo{}
	}
	cr, ok := ar.(tokens.CreateResult)
	if !ok {
		return ProjectInfo{}
	}
	p, err := cr.ExtractProject()
	if err != nil || p == nil {
		return ProjectInfo{}
	}
	return ProjectInfo{ID: p.ID, Name: p.Name, DomainID: p.Domain.ID}
}

// detectSwitchCapability decides, from the resolved auth options, whether the
// project selector can re-scope. The auth method is known at this point, so the
// decision is made before the user attempts a switch.
func detectSwitchCapability(ao *gophercloud.AuthOptions) SwitchCapability {
	switch {
	case ao.ApplicationCredentialID != "" || ao.ApplicationCredentialName != "":
		return SwitchCapability{
			Reason:  "Application credentials are locked to the project they were created for.",
			Suggest: "To switch, use user credentials, or create separate app creds per project and select them via --os-cloud.",
		}
	case ao.Username == "" && ao.UserID == "" && ao.TokenID != "":
		return SwitchCapability{
			Reason:  "Authenticated with a pre-issued token, which can't be re-scoped to another project.",
			Suggest: "To switch projects, authenticate with credentials (clouds.yaml or OS_USERNAME/OS_PASSWORD) or an application credential with access to multiple projects.",
		}
	default:
		return SwitchCapability{CanSwitch: true}
	}
}
