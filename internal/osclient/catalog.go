package osclient

import (
	"context"
	"sort"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/endpoints"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/regions"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/services"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
)

// This file covers the Keystone service catalog (services, endpoints, regions)
// and the current token / "whoami".

// Service is a catalog service — a capability (compute, image, network, …) the
// cloud exposes, reachable through its endpoints.
type Service struct {
	ID          string
	Type        string // e.g. "compute", "image", "identity"
	Name        string
	Description string
	Enabled     bool
}

// ListServices lists the catalog services. A 403 degrades to ErrAdminRequired so
// the UI can show an explanatory empty list.
func (c *Clients) ListServices(ctx context.Context) ([]Service, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	pages, err := services.List(client, services.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := services.ExtractServices(pages)
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(items))
	for _, s := range items {
		out = append(out, Service{ID: s.ID, Type: s.Type, Name: serviceName(s), Description: serviceDescription(s), Enabled: s.Enabled})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// serviceName reads a service's optional name (Keystone leaves it in Extra when
// not a first-class column on older microversions).
func serviceName(s services.Service) string {
	if s.Name != "" {
		return s.Name
	}
	if v, ok := s.Extra["name"].(string); ok {
		return v
	}
	return ""
}

func serviceDescription(s services.Service) string {
	if s.Description != "" {
		return s.Description
	}
	if v, ok := s.Extra["description"].(string); ok {
		return v
	}
	return ""
}

// Endpoint is one access URL for a service: an interface (public/internal/admin)
// in a region.
type Endpoint struct {
	ID          string
	ServiceID   string
	ServiceType string // resolved from the services list (best effort)
	ServiceName string // resolved from the services list (best effort)
	Interface   string // "public", "internal", or "admin"
	RegionID    string
	URL         string
	Description string
	Enabled     bool
}

// ListEndpoints lists the catalog endpoints, resolving each one's service type
// and name from the services list so a standalone endpoints view is legible. A
// 403 degrades to ErrAdminRequired; a failure to resolve service names is
// non-fatal (rows fall back to the bare service id).
func (c *Clients) ListEndpoints(ctx context.Context) ([]Endpoint, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	pages, err := endpoints.List(client, endpoints.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := endpoints.ExtractEndpoints(pages)
	if err != nil {
		return nil, err
	}

	// Resolve service type/name best-effort; a denial here just leaves them blank.
	svcType := map[string]string{}
	svcName := map[string]string{}
	if svcs, serr := c.ListServices(ctx); serr == nil {
		for _, s := range svcs {
			svcType[s.ID] = s.Type
			svcName[s.ID] = s.Name
		}
	}

	out := make([]Endpoint, 0, len(items))
	for _, e := range items {
		out = append(out, Endpoint{
			ID: e.ID, ServiceID: e.ServiceID, ServiceType: svcType[e.ServiceID], ServiceName: svcName[e.ServiceID],
			Interface: string(e.Availability), RegionID: e.Region, URL: e.URL, Description: e.Description, Enabled: e.Enabled,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceType != out[j].ServiceType {
			return out[i].ServiceType < out[j].ServiceType
		}
		if out[i].RegionID != out[j].RegionID {
			return out[i].RegionID < out[j].RegionID
		}
		return endpointInterfaceRank(out[i].Interface) < endpointInterfaceRank(out[j].Interface)
	})
	return out, nil
}

// endpointInterfaceRank orders interfaces public < internal < admin so an
// endpoint list reads in the order operators expect.
func endpointInterfaceRank(iface string) int {
	switch iface {
	case "public":
		return 0
	case "internal":
		return 1
	case "admin":
		return 2
	}
	return 3
}

// Region is a catalog region — a named location, optionally nested under a parent
// region, that endpoints belong to.
type Region struct {
	ID             string
	Description    string
	ParentRegionID string
}

// ListRegions lists the catalog regions. A 403 degrades to ErrAdminRequired.
func (c *Clients) ListRegions(ctx context.Context) ([]Region, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	pages, err := regions.List(client, regions.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := regions.ExtractRegions(pages)
	if err != nil {
		return nil, err
	}
	out := make([]Region, 0, len(items))
	for _, r := range items {
		out = append(out, Region{ID: r.ID, Description: r.Description, ParentRegionID: r.ParentRegionID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// TokenInfo describes the current authentication token — who you are and what
// scope, roles, and lifetime the active credential carries. It is derived from
// the cached auth response, so reading it makes no network call.
type TokenInfo struct {
	Available       bool // false when no token detail could be read
	UserName        string
	UserID          string
	UserDomain      string
	UserDomainID    string
	UserDomainName  string
	ScopeType       string // "project", "domain", "system", or "unscoped"
	ScopeName       string
	ScopeID         string
	ScopeDomain     string // display name (or ID) of a project scope's owning domain
	ScopeDomainID   string
	ScopeDomainName string
	Roles           []string
	RoleDetails     []TokenRole
	ExpiresAt       time.Time
}

// TokenRole is an effective role embedded in the active token. Unlike the
// privileged role catalog, the token exposes only the roles effective in its
// current scope, but it does include stable role IDs and names.
type TokenRole struct {
	ID   string
	Name string
}

// CurrentToken reports the active token's identity, scope, roles, and expiry
// from the retained auth result — no network call.
func (c *Clients) CurrentToken() TokenInfo {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	var provider *gophercloud.ProviderClient
	if sc != nil {
		provider = sc.provider
	}
	c.mu.Unlock()
	if provider == nil {
		return TokenInfo{}
	}
	ar := provider.GetAuthResult()
	if ar == nil {
		return TokenInfo{}
	}
	cr, ok := ar.(tokens.CreateResult)
	if !ok {
		return TokenInfo{}
	}

	info := TokenInfo{Available: true, ScopeType: "unscoped"}
	if u, err := cr.ExtractUser(); err == nil && u != nil {
		info.UserName, info.UserID = u.Name, u.ID
		info.UserDomainID, info.UserDomainName = u.Domain.ID, u.Domain.Name
		info.UserDomain = firstNonEmpty(info.UserDomainName, info.UserDomainID)
	}
	if p, err := cr.ExtractProject(); err == nil && p != nil && p.ID != "" {
		info.ScopeType, info.ScopeName, info.ScopeID = "project", p.Name, p.ID
		info.ScopeDomainID, info.ScopeDomainName = p.Domain.ID, p.Domain.Name
		info.ScopeDomain = firstNonEmpty(info.ScopeDomainName, info.ScopeDomainID)
	} else if d, err := cr.ExtractDomain(); err == nil && d != nil && d.ID != "" {
		info.ScopeType, info.ScopeName, info.ScopeID = "domain", d.Name, d.ID
	} else {
		var body struct {
			System *struct {
				All bool `json:"all"`
			} `json:"system"`
		}
		if err := cr.ExtractInto(&body); err == nil && body.System != nil && body.System.All {
			info.ScopeType, info.ScopeName, info.ScopeID = "system", "all", "all"
		}
	}
	if roles, err := cr.ExtractRoles(); err == nil {
		for _, r := range roles {
			info.Roles = append(info.Roles, r.Name)
			info.RoleDetails = append(info.RoleDetails, TokenRole{ID: r.ID, Name: r.Name})
		}
		sort.Strings(info.Roles)
		sort.Slice(info.RoleDetails, func(i, j int) bool { return info.RoleDetails[i].Name < info.RoleDetails[j].Name })
	}
	if t, err := cr.ExtractToken(); err == nil && t != nil {
		info.ExpiresAt = t.ExpiresAt
	}
	return info
}
