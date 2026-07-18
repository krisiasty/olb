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

// Clients bundles the authenticated provider and its service clients, plus the
// current scope, the retained auth options needed to re-scope, and the switch
// capability decided at auth time.
type Clients struct {
	Provider *gophercloud.ProviderClient
	LB       *gophercloud.ServiceClient // Octavia (required)
	Identity *gophercloud.ServiceClient // Keystone v3 (required)
	Network  *gophercloud.ServiceClient // Neutron (optional; floating IPs)
	Compute  *gophercloud.ServiceClient // Nova (optional; member instances)

	Region  string
	Project ProjectInfo
	Switch  SwitchCapability

	// baseAuth retains the resolved credentials (sans a fixed scope) so a
	// project switch can request a new scoped token. Holding the password in
	// memory is inherent to re-scoping and is the documented trade-off.
	baseAuth gophercloud.AuthOptions
	endpoint gophercloud.EndpointOpts
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

	provider, err := openstack.AuthenticatedClient(ctx, *ao)
	if err != nil {
		return nil, fmt.Errorf("authenticating to OpenStack: %w", err)
	}

	eo := gophercloud.EndpointOpts{Region: region, Availability: gophercloud.AvailabilityPublic}
	c := &Clients{Provider: provider, Region: region, baseAuth: *ao, endpoint: eo}

	if c.LB, err = openstack.NewLoadBalancerV2(provider, eo); err != nil {
		return nil, fmt.Errorf("no Octavia (load-balancer) endpoint in the service catalog: %w", err)
	}
	if c.Identity, err = openstack.NewIdentityV3(provider, eo); err != nil {
		return nil, fmt.Errorf("no Keystone (identity) endpoint in the service catalog: %w", err)
	}
	// Neutron and Nova are optional: their absence degrades the floating-IP and
	// member-instance edges gracefully rather than being fatal.
	c.Network, _ = openstack.NewNetworkV2(provider, eo)
	c.Compute, _ = openstack.NewComputeV2(provider, eo)

	c.Project = currentProject(provider)
	c.Switch = detectSwitchCapability(ao)

	return c, nil
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
