// Package osclient wires OpenStack authentication and the Octavia / Neutron /
// Nova / Keystone / Magnum service clients, and exposes the data operations the TUI
// needs (list load balancers, fetch a status tree, load per-object detail,
// and list selectable projects).
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
	Project string // --project: initial project selection (name or ID)

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

// ScopeKind identifies the Keystone scope carried by an authentication token.
type ScopeKind string

const (
	ScopeSystem  ScopeKind = "system"
	ScopeDomain  ScopeKind = "domain"
	ScopeProject ScopeKind = "project"
)

// ScopeInfo identifies one scope the current user can authenticate to.
// DomainName is populated for project scopes so the selector can group projects
// without performing extra calls while it renders.
type ScopeInfo struct {
	Kind       ScopeKind
	ID         string
	Name       string
	DomainID   string
	DomainName string
}

// Label returns the most useful human-readable scope identifier available.
func (s ScopeInfo) Label() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Kind == ScopeSystem {
		return "all"
	}
	return s.ID
}

// Equal reports whether two values identify the same Keystone scope.
func (s ScopeInfo) Equal(other ScopeInfo) bool {
	return s.Kind == other.Kind && s.ID == other.ID
}

// serviceClients is one consistently authenticated set of OpenStack clients.
type serviceClients struct {
	provider   *gophercloud.ProviderClient
	lb         *gophercloud.ServiceClient // Octavia (optional outside LB browsing)
	identity   *gophercloud.ServiceClient // Keystone v3 (required)
	network    *gophercloud.ServiceClient // Neutron (optional; floating IPs)
	compute    *gophercloud.ServiceClient // Nova (optional; member instances)
	keyManager *gophercloud.ServiceClient // Barbican (optional; TLS certificates)
	container  *gophercloud.ServiceClient // Magnum (optional; Kubernetes relations)
	scope      ScopeInfo
}

type scopeAuthFunc func(context.Context, ScopeInfo) (*serviceClients, error)

// Clients retains the startup token for re-scoping and the service clients for
// the single currently active authentication scope.
type Clients struct {
	Region string

	mu             sync.Mutex
	services       *serviceClients // immutable startup scope
	activeServices *serviceClients
	scopeAuth      scopeAuthFunc
	telemetry      *telemetry.Collector
	scope          ScopeInfo

	// projNames caches the ID→display-name map used to label cross-project rows.
	projNames   map[string]string
	projNamesAt time.Time
	// domainNames caches the domain ID→name map used to label identity objects,
	// on the same TTL as projNames.
	domainNames   map[string]string
	domainNamesAt time.Time
	// serviceUserIDs caches the set of user IDs holding a role on the service
	// project (the convention that marks a user as a service/system account), on
	// the same TTL as projNames. A nil map means "not yet computed / unavailable";
	// detection then falls back to the well-known-name heuristic alone.
	serviceUserIDs   map[string]bool
	serviceUserIDsAt time.Time
}

// Authenticate resolves credentials from CLI/env/clouds.yaml, authenticates,
// builds the service clients, and records the token's actual scope.
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
		telemetry: telemetry.NewCollector(telemetry.DefaultSlowThreshold),
	}

	// Authenticate exactly once with the credentials' original scope.
	endpoint := gophercloud.EndpointOpts{
		Region: region, Availability: gophercloud.AvailabilityPublic,
	}
	sc, err := buildServiceClients(ctx, *ao, endpoint, c.telemetry, config.apiLogger)
	if err != nil {
		return nil, err
	}
	c.services = sc
	c.activeServices = sc
	c.scope = sc.scope
	baseAuth := *ao
	c.scopeAuth = func(ctx context.Context, target ScopeInfo) (*serviceClients, error) {
		subjectToken := sc.provider.Token()
		if subjectToken == "" {
			return nil, fmt.Errorf("authenticate for %s scope %s: startup token is unavailable", target.Kind, target.Label())
		}
		scopedAuth, err := scopedAuthOptions(baseAuth.IdentityEndpoint, subjectToken, target)
		if err != nil {
			return nil, err
		}
		scoped, err := buildServiceClients(ctx, scopedAuth, endpoint, c.telemetry, config.apiLogger)
		if err != nil {
			return nil, fmt.Errorf("authenticate for %s scope %s: %w", target.Kind, target.Label(), err)
		}
		if scoped.scope.Kind != "" && !scoped.scope.Equal(target) {
			return nil, fmt.Errorf("authenticate for %s scope %s returned %s scope %s",
				target.Kind, target.Label(), scoped.scope.Kind, scoped.scope.Label())
		}
		if scoped.scope.Kind == "" {
			scoped.scope = target
		}
		return scoped, nil
	}
	return c, nil
}

func scopedAuthOptions(identityEndpoint, subjectToken string, target ScopeInfo) (gophercloud.AuthOptions, error) {
	scope := &gophercloud.AuthScope{}
	switch target.Kind {
	case ScopeSystem:
		scope.System = true
	case ScopeDomain:
		if target.ID == "" {
			return gophercloud.AuthOptions{}, fmt.Errorf("cannot authenticate to a domain scope without an ID")
		}
		scope.DomainID = target.ID
	case ScopeProject:
		if target.ID == "" {
			return gophercloud.AuthOptions{}, fmt.Errorf("cannot authenticate to a project scope without an ID")
		}
		scope.ProjectID = target.ID
	default:
		return gophercloud.AuthOptions{}, fmt.Errorf("unsupported authentication scope %q", target.Kind)
	}
	return gophercloud.AuthOptions{
		IdentityEndpoint: identityEndpoint,
		TokenID:          subjectToken,
		Scope:            scope,
		AllowReauth:      true,
	}, nil
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
	sc.lb, _ = openstack.NewLoadBalancerV2(provider, endpoint)
	if sc.identity, err = openstack.NewIdentityV3(provider, endpoint); err != nil {
		return nil, fmt.Errorf("no Keystone (identity) endpoint in the service catalog: %w", err)
	}
	// Cross-service clients are optional: their absence degrades the associated
	// related objects gracefully rather than being fatal.
	sc.network, _ = openstack.NewNetworkV2(provider, endpoint)
	sc.compute, _ = openstack.NewComputeV2(provider, endpoint)
	sc.keyManager, _ = openstack.NewKeyManagerV1(provider, endpoint)
	sc.container, _ = openstack.NewContainerInfraV1(provider, endpoint)

	sc.scope = currentScope(provider)
	return sc, nil
}

// activeClients returns the service clients matching the active token scope.
func (c *Clients) activeClients() (*serviceClients, error) {
	c.mu.Lock()
	services := c.activeServices
	if services == nil {
		services = c.services
	}
	c.mu.Unlock()
	if services == nil {
		return nil, ErrUnavailable
	}
	return services, nil
}

func (c *Clients) activeLBClients(_ context.Context, _ string) (*serviceClients, error) {
	services, err := c.activeClients()
	if err != nil {
		return nil, err
	}
	if services.lb == nil {
		return nil, ErrUnavailable
	}
	return services, nil
}

// currentScope extracts the authoritative scope from the token returned by
// Keystone. A token has at most one of system, domain, or project scope.
func currentScope(provider *gophercloud.ProviderClient) ScopeInfo {
	ar := provider.GetAuthResult()
	if ar == nil {
		return ScopeInfo{}
	}
	cr, ok := ar.(tokens.CreateResult)
	if !ok {
		return ScopeInfo{}
	}
	if p, err := cr.ExtractProject(); err == nil && p != nil {
		return ScopeInfo{
			Kind: ScopeProject, ID: p.ID, Name: p.Name,
			DomainID: p.Domain.ID, DomainName: p.Domain.Name,
		}
	}
	if d, err := cr.ExtractDomain(); err == nil && d != nil {
		return ScopeInfo{Kind: ScopeDomain, ID: d.ID, Name: d.Name}
	}
	var body struct {
		System *struct {
			All bool `json:"all"`
		} `json:"system"`
	}
	if err := cr.ExtractInto(&body); err == nil && body.System != nil && body.System.All {
		return ScopeInfo{Kind: ScopeSystem, ID: "all", Name: "all"}
	}
	return ScopeInfo{}
}
