// Package osclient wires OpenStack authentication and the Octavia / Neutron /
// Nova / Keystone service clients, and exposes the data operations the TUI
// needs (list load balancers, fetch a status tree, load per-object detail,
// and list accessible projects).
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
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"

	"github.com/krisiasty/olb/internal/telemetry"
)

// Options holds the auth-related inputs captured from CLI flags. Empty fields
// are treated as "not provided" and fall through to env / clouds.yaml.
type Options struct {
	Cloud   string // --os-cloud / OS_CLOUD
	Region  string // --os-region-name / OS_REGION_NAME
	Project string // --project: initial presentation filter (name or ID)

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

type authenticateConfig struct {
	apiLogger *telemetry.APILogger
}

// AuthenticateOption configures optional HTTP instrumentation without mixing
// it into the OpenStack credential options.
type AuthenticateOption func(*authenticateConfig)

// WithAPILogger enables sanitized HTTP request/response logging on the same
// transport that gathers in-memory telemetry.
func WithAPILogger(logger *telemetry.APILogger) AuthenticateOption {
	return func(config *authenticateConfig) {
		config.apiLogger = logger
	}
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
}

// ProjectInfo identifies a project scope.
type ProjectInfo struct {
	ID       string
	Name     string
	DomainID string
}

// SwitchCapability describes whether the project filter can be shown. Project
// selection never changes the authenticated token; failures to enumerate the
// available projects are instead reported when the selector is opened.
type SwitchCapability struct {
	CanSwitch bool
	Reason    string
	Suggest   string
}

// serviceClients is the set of OpenStack service clients created from the
// program's original authentication scope. It remains immutable for the life
// of the program; TUI project selection is a data filter, not re-authentication.
type serviceClients struct {
	provider *gophercloud.ProviderClient
	lb       *gophercloud.ServiceClient // Octavia (required)
	identity *gophercloud.ServiceClient // Keystone v3 (required)
	network  *gophercloud.ServiceClient // Neutron (optional; floating IPs)
	compute  *gophercloud.ServiceClient // Nova (optional; member instances)
	project  ProjectInfo
}

// Clients holds the original authenticated service clients plus the current
// presentation filter. The authenticated scope is deliberately immutable: an
// admin must not lose cluster-wide visibility after selecting a project.
type Clients struct {
	Region string
	Switch SwitchCapability

	mu        sync.Mutex
	services  *serviceClients
	telemetry *telemetry.Collector
	selected  ProjectInfo // project whose rows are shown when allMode is false
	allMode   bool

	// projNames caches the ID→display-name map used to label all-projects rows,
	// so repeated (auto-)refreshes don't re-enumerate Keystone every time.
	projNames   map[string]string
	projNamesAt time.Time
}

// Authenticate resolves credentials from CLI/env/clouds.yaml, authenticates,
// builds the service clients, and determines the project-switch capability.
func Authenticate(ctx context.Context, o Options, options ...AuthenticateOption) (*Clients, error) {
	o.applyToEnv()
	config := authenticateConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

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
		Switch:    SwitchCapability{CanSwitch: true},
		telemetry: telemetry.NewCollector(telemetry.DefaultSlowThreshold),
	}

	// Authenticate exactly once with the credentials' original scope.
	sc, err := buildServiceClients(ctx, *ao, gophercloud.EndpointOpts{
		Region: region, Availability: gophercloud.AvailabilityPublic,
	}, c.telemetry, config.apiLogger)
	if err != nil {
		return nil, err
	}
	c.services = sc
	c.selected = sc.project
	return c, nil
}

func buildServiceClients(ctx context.Context, ao gophercloud.AuthOptions, endpoint gophercloud.EndpointOpts, collector *telemetry.Collector, apiLogger *telemetry.APILogger) (*serviceClients, error) {
	ao.AllowReauth = true

	provider, err := openstack.NewClient(ao.IdentityEndpoint)
	if err != nil {
		return nil, fmt.Errorf("authenticating to OpenStack: %w", err)
	}
	provider.HTTPClient = http.Client{Transport: telemetry.NewTransport(http.DefaultTransport, collector, apiLogger)}
	if err = openstack.Authenticate(ctx, provider, ao); err != nil {
		return nil, fmt.Errorf("authenticating to OpenStack: %w", err)
	}
	sc := &serviceClients{provider: provider}
	if sc.lb, err = openstack.NewLoadBalancerV2(provider, endpoint); err != nil {
		return nil, fmt.Errorf("no Octavia (load-balancer) endpoint in the service catalog: %w", err)
	}
	if sc.identity, err = openstack.NewIdentityV3(provider, endpoint); err != nil {
		return nil, fmt.Errorf("no Keystone (identity) endpoint in the service catalog: %w", err)
	}
	// Neutron and Nova are optional: their absence degrades the floating-IP and
	// member-instance edges gracefully rather than being fatal.
	sc.network, _ = openstack.NewNetworkV2(provider, endpoint)
	sc.compute, _ = openstack.NewComputeV2(provider, endpoint)

	sc.project = currentProject(provider)
	return sc, nil
}

// clientsForLB always returns the original service clients. Project selection
// must never reduce (or otherwise mutate) the authorization used for drill-in.
func (c *Clients) clientsForLB(_ context.Context, _ string) (*serviceClients, error) {
	c.mu.Lock()
	services := c.services
	c.mu.Unlock()
	return services, nil
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
