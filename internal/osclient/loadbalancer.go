package osclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/amphorae"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/l7policies"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/listeners"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/loadbalancers"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/monitors"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/pools"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/floatingips"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"github.com/krisiasty/olb/internal/model"
)

// LB is a load-balancer list-row summary for the top-level list view.
type LB struct {
	ID                 string
	Name               string
	Provider           string
	VipAddress         string
	VipPortID          string
	VipSubnetID        string
	VipNetworkID       string
	AdditionalVIPs     []model.AdditionalVIP
	ProjectID          string
	ProjectName        string // owning project's name (shown in all-projects mode)
	ProvisioningStatus string
	OperatingStatus    string
}

// ListenerSummary carries the protocol endpoint facts absent from Octavia's
// status-tree response but needed to distinguish same-named listener rows.
type ListenerSummary struct {
	ID           string
	Protocol     string
	ProtocolPort int
}

// PoolSummary carries the compact configuration facts used in LB related rows.
type PoolSummary struct {
	ID                 string
	Name               string
	Protocol           string
	LBMethod           string
	MemberCount        int
	ListenerIDs        []string
	ProvisioningStatus string
	OperatingStatus    string
}

// Meta returns the LB-level facts the graph builder needs.
func (l LB) Meta() model.LBMeta {
	return model.LBMeta{
		VipAddress: l.VipAddress, VipPortID: l.VipPortID,
		VipSubnetID: l.VipSubnetID, VipNetworkID: l.VipNetworkID,
		AdditionalVIPs: l.AdditionalVIPs, Provider: l.Provider,
		ProjectID: l.ProjectID, ProjectName: l.ProjectName,
	}
}

// ErrUnavailable marks a feature that cannot be served because a required
// service client (Neutron/Nova) is absent from the catalog.
var ErrUnavailable = errors.New("service unavailable in this cloud/scope")

// ErrAdminRequired marks a surface reachable only with admin RBAC (amphorae).
var ErrAdminRequired = errors.New("requires admin")

// ListLoadBalancers uses the active credential strategy. A concrete selection
// is sent to Octavia as a project_id filter and also applied locally in case a
// cloud ignores or broadens the server-side filter.
func (c *Clients) ListLoadBalancers(ctx context.Context) ([]LB, error) {
	c.mu.Lock()
	allMode := c.allMode
	selected := c.selected
	services := c.activeServices
	if services == nil {
		services = c.services
	}
	c.mu.Unlock()

	queryProject := selected
	if allMode {
		queryProject = ProjectInfo{}
	}
	lbs, err := listWith(ctx, services, queryProject)
	if err != nil {
		return nil, err
	}
	if !allMode {
		return filterLoadBalancers(lbs, selected), nil
	}

	// Project enumeration is only for friendly names. Failure does not discard
	// the authoritative Octavia list; rows fall back to their project IDs.
	nameByID := c.projectNameMap(ctx)
	for i := range lbs {
		if lbs[i].ProjectName == "" {
			if n := nameByID[lbs[i].ProjectID]; n != "" {
				lbs[i].ProjectName = n
			}
		}
	}
	return lbs, nil
}

func filterLoadBalancers(lbs []LB, project ProjectInfo) []LB {
	if project.ID == "" {
		return lbs
	}
	filtered := make([]LB, 0, len(lbs))
	for _, lb := range lbs {
		if lb.ProjectID != project.ID {
			continue
		}
		lb.ProjectName = project.Name
		filtered = append(filtered, lb)
	}
	return filtered
}

// listWith issues an Octavia list using the supplied scoped clients. proj
// controls the optional server-side project_id query parameter.
func listWith(ctx context.Context, sc *serviceClients, proj ProjectInfo) ([]LB, error) {
	opts := loadbalancers.ListOpts{ProjectID: proj.ID}
	pages, err := loadbalancers.List(sc.lb, opts).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := loadbalancers.ExtractLoadBalancers(pages)
	if err != nil {
		return nil, err
	}
	out := make([]LB, 0, len(raw))
	for _, l := range raw {
		out = append(out, LB{
			ID: l.ID, Name: l.Name, Provider: l.Provider,
			VipAddress: l.VipAddress, VipPortID: l.VipPortID,
			VipSubnetID: l.VipSubnetID, VipNetworkID: l.VipNetworkID,
			AdditionalVIPs: additionalVIPMeta(l.AdditionalVips),
			ProjectID:      l.ProjectID, ProjectName: proj.Name,
			ProvisioningStatus: l.ProvisioningStatus, OperatingStatus: l.OperatingStatus,
		})
	}
	return out, nil
}

// GetTree fetches a load balancer's status tree (one call for the whole nested
// structure) plus its top-level facts (VIP, provider) and builds the graph.
// A hint from the list avoids the extra get; without one (e.g. history
// re-resolution) the get supplies VIP/provider and doubles as an existence
// check — a 404 surfaces so the caller can mark a history entry dead.
func (c *Clients) GetTree(ctx context.Context, lbID string, hint *model.LBMeta) (*model.Tree, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	var meta model.LBMeta
	if hint != nil {
		meta = *hint
	} else {
		lb, err := loadbalancers.Get(ctx, sc.lb, lbID).Extract()
		if err != nil {
			return nil, err
		}
		meta = model.LBMeta{
			VipAddress: lb.VipAddress, VipPortID: lb.VipPortID,
			VipSubnetID: lb.VipSubnetID, VipNetworkID: lb.VipNetworkID,
			AdditionalVIPs: additionalVIPMeta(lb.AdditionalVips),
			Provider:       lb.Provider, ProjectID: lb.ProjectID,
		}
	}

	res := loadbalancers.GetStatuses(ctx, sc.lb, lbID)
	if res.Err != nil {
		return nil, res.Err
	}
	// Re-decode the raw response into our own reduced types so we are robust to
	// the field-name variation between deployments and gophercloud's structs.
	buf, err := json.Marshal(res.Body)
	if err != nil {
		return nil, fmt.Errorf("re-encoding status tree: %w", err)
	}
	var wrapper struct {
		Statuses model.StatusTree `json:"statuses"`
	}
	if err := json.Unmarshal(buf, &wrapper); err != nil {
		return nil, fmt.Errorf("decoding status tree: %w", err)
	}
	return model.Build(&wrapper.Statuses, meta), nil
}

func additionalVIPMeta(vips []loadbalancers.AdditionalVip) []model.AdditionalVIP {
	out := make([]model.AdditionalVIP, 0, len(vips))
	for _, vip := range vips {
		out = append(out, model.AdditionalVIP{Address: vip.IPAddress, SubnetID: vip.SubnetID})
	}
	return out
}

// DetailResult is the outcome of a lazy per-object show. It carries only data —
// the raw object plus display attributes and any reference-edge resolution to
// apply — so the caller can mutate the shared graph on the UI goroutine,
// keeping the fetch itself free of shared-state writes (Bubble Tea commands run
// concurrently with Update).
type DetailResult struct {
	Raw   map[string]any
	Attrs map[string]string

	// ListenerDefaultPoolID is set (possibly to "") when the node is a listener,
	// so the caller can upgrade its default-pool reference edge.
	ListenerDefaultPoolID string
	IsListener            bool

	// L7Action / L7RedirectPoolID are set when the node is an L7 policy, so the
	// caller can wire the redirect-pool edge when the action is REDIRECT_TO_POOL.
	L7Action         string
	L7RedirectPoolID string
	IsL7Policy       bool
}

// FetchDetail fetches the full per-object configuration for a node (the lazy
// `show` not present in the status tree) and returns it as data. It does not
// mutate the node or tree; the caller applies DetailResult on the UI goroutine.
func (c *Clients) FetchDetail(ctx context.Context, n *model.Node) (DetailResult, error) {
	res := DetailResult{Attrs: map[string]string{}}
	sc, err := c.clientsForLB(ctx, n.OwningLBID)
	if err != nil {
		return res, err
	}
	switch n.Type {
	case model.TypeLoadBalancer:
		r := loadbalancers.Get(ctx, sc.lb, n.ID)
		lb, err := r.Extract()
		if err != nil {
			return res, err
		}
		res.Attrs["provider"] = lb.Provider
		res.Attrs["vip_address"] = lb.VipAddress
		res.Attrs["admin_state_up"] = boolStr(lb.AdminStateUp)
		res.Attrs["flavor_id"] = lb.FlavorID
		res.Attrs["created_at"] = formatAPITime(lb.CreatedAt)
		res.Attrs["updated_at"] = formatAPITime(lb.UpdatedAt)
		if description := strings.TrimSpace(lb.Description); description != "" {
			res.Attrs["description"] = description
		}
		res.Raw = innerRaw(r.Body, "loadbalancer")

	case model.TypeVIP:
		if sc.network == nil {
			return res, ErrUnavailable
		}
		portID := n.Attrs["port_id"]
		subnetID := n.Attrs["subnet_id"]
		networkID := n.Attrs["network_id"]
		res.Raw = map[string]any{"vip": n.Raw}

		if portID != "" {
			r := ports.Get(ctx, sc.network, portID)
			port, err := r.Extract()
			if err != nil {
				return res, err
			}
			res.Attrs["port_name"] = port.Name
			res.Attrs["port_id"] = port.ID
			if networkID == "" {
				networkID = port.NetworkID
			}
			securityGroups := strings.Join(port.SecurityGroups, ", ")
			if securityGroups == "" {
				securityGroups = "none"
			}
			res.Attrs["security_group_ids"] = securityGroups
			res.Raw["port"] = innerRaw(r.Body, "port")
		}

		if subnetID != "" {
			r := subnets.Get(ctx, sc.network, subnetID)
			subnet, err := r.Extract()
			if err != nil {
				return res, err
			}
			res.Attrs["subnet_name"] = subnet.Name
			res.Attrs["subnet_id"] = subnet.ID
			if networkID == "" {
				networkID = subnet.NetworkID
			}
			res.Raw["subnet"] = innerRaw(r.Body, "subnet")
		}

		if networkID != "" {
			r := networks.Get(ctx, sc.network, networkID)
			network, err := r.Extract()
			if err != nil {
				return res, err
			}
			res.Attrs["network_name"] = network.Name
			res.Attrs["network_id"] = network.ID
			res.Raw["network"] = innerRaw(r.Body, "network")
		}

	case model.TypeListener:
		r := listeners.Get(ctx, sc.lb, n.ID)
		ln, err := r.Extract()
		if err != nil {
			return res, err
		}
		raw := innerRaw(r.Body, "listener")
		res.Attrs["protocol"] = ln.Protocol
		res.Attrs["port"] = fmt.Sprintf("%d", ln.ProtocolPort)
		res.Attrs["admin_state_up"] = boolStr(ln.AdminStateUp)
		if ln.ConnLimit >= 0 {
			res.Attrs["connection_limit"] = fmt.Sprintf("%d", ln.ConnLimit)
		} else {
			res.Attrs["connection_limit"] = "unlimited"
		}
		if description := strings.TrimSpace(ln.Description); description != "" {
			res.Attrs["description"] = description
		}
		if created := rawString(raw, "created_at"); created != "" {
			res.Attrs["created_at"] = created
		}
		if updated := rawString(raw, "updated_at"); updated != "" {
			res.Attrs["updated_at"] = updated
		}
		if len(ln.AllowedCIDRs) > 0 {
			res.Attrs["allowed_cidrs"] = strings.Join(ln.AllowedCIDRs, ", ")
		}
		if ln.Protocol == "TERMINATED_HTTPS" {
			res.Attrs["certificate_ref"] = ln.DefaultTlsContainerRef
			res.Attrs["sni_certificate_count"] = fmt.Sprintf("%d", len(ln.SniContainerRefs))
			if len(ln.TLSVersions) > 0 {
				res.Attrs["tls_versions"] = strings.Join(ln.TLSVersions, ", ")
			}
			if len(ln.ALPNProtocols) > 0 {
				res.Attrs["alpn_protocols"] = strings.Join(ln.ALPNProtocols, ", ")
			}
			if ln.DefaultTlsContainerRef != "" {
				certificate, certErr := c.listenerCertificate(ctx, sc, ln.DefaultTlsContainerRef)
				if certErr != nil {
					res.Attrs["certificate_error"] = certErr.Error()
				} else {
					res.Attrs["certificate_name"] = certificate.Name
					res.Attrs["certificate_subject"] = certificate.Subject
					res.Attrs["certificate_issuer"] = certificate.Issuer
					res.Attrs["certificate_not_before"] = formatAPITime(certificate.NotBefore)
					res.Attrs["certificate_not_after"] = formatAPITime(certificate.NotAfter)
				}
			}
		}
		res.IsListener = true
		res.ListenerDefaultPoolID = ln.DefaultPoolID
		res.Raw = raw

	case model.TypePool:
		r := pools.Get(ctx, sc.lb, n.ID)
		p, err := r.Extract()
		if err != nil {
			return res, err
		}
		raw := innerRaw(r.Body, "pool")
		res.Attrs["lb_algorithm"] = p.LBMethod
		res.Attrs["protocol"] = p.Protocol
		res.Attrs["admin_state_up"] = boolStr(p.AdminStateUp)
		res.Attrs["project_id"] = p.ProjectID
		res.Attrs["member_count"] = fmt.Sprintf("%d", len(p.Members))
		res.Attrs["listener_count"] = fmt.Sprintf("%d", len(p.Listeners))
		res.Attrs["healthmonitor_id"] = p.MonitorID
		res.Attrs["subnet_id"] = p.SubnetID
		res.Attrs["tls_enabled"] = boolStr(p.TLSEnabled)
		if description := strings.TrimSpace(p.Description); description != "" {
			res.Attrs["description"] = description
		}
		if p.Persistence.Type != "" {
			res.Attrs["session_persistence"] = p.Persistence.Type
		}
		if p.Persistence.CookieName != "" {
			res.Attrs["persistence_cookie"] = p.Persistence.CookieName
		}
		if len(p.TLSVersions) > 0 {
			res.Attrs["tls_versions"] = strings.Join(p.TLSVersions, ", ")
		}
		if len(p.ALPNProtocols) > 0 {
			res.Attrs["alpn_protocols"] = strings.Join(p.ALPNProtocols, ", ")
		}
		if p.TLSCiphers != "" {
			res.Attrs["tls_ciphers"] = p.TLSCiphers
		}
		if len(p.Tags) > 0 {
			res.Attrs["tags"] = strings.Join(p.Tags, ", ")
		}
		if created := rawString(raw, "created_at"); created != "" {
			res.Attrs["created_at"] = created
		}
		if updated := rawString(raw, "updated_at"); updated != "" {
			res.Attrs["updated_at"] = updated
		}
		res.Raw = raw

	case model.TypeMember:
		poolID := parentID(n)
		if poolID == "" {
			return res, fmt.Errorf("member detail needs its pool")
		}
		r := pools.GetMember(ctx, sc.lb, poolID, n.ID)
		m, err := r.Extract()
		if err != nil {
			return res, err
		}
		res.Attrs["address"] = m.Address
		res.Attrs["port"] = fmt.Sprintf("%d", m.ProtocolPort)
		res.Attrs["weight"] = fmt.Sprintf("%d", m.Weight)
		res.Attrs["backup"] = boolStr(m.Backup)
		res.Raw = innerRaw(r.Body, "member")

	case model.TypeHealthMonitor:
		r := monitors.Get(ctx, sc.lb, n.ID)
		m, err := r.Extract()
		if err != nil {
			return res, err
		}
		raw := innerRaw(r.Body, "healthmonitor")
		res.Attrs["type"] = m.Type
		res.Attrs["delay"] = fmt.Sprintf("%d", m.Delay)
		res.Attrs["timeout"] = fmt.Sprintf("%d", m.Timeout)
		res.Attrs["max_retries"] = fmt.Sprintf("%d", m.MaxRetries)
		res.Attrs["max_retries_down"] = fmt.Sprintf("%d", m.MaxRetriesDown)
		res.Attrs["admin_state_up"] = boolStr(m.AdminStateUp)
		res.Attrs["project_id"] = m.ProjectID
		if m.HTTPMethod != "" {
			res.Attrs["http_method"] = m.HTTPMethod
		}
		if m.URLPath != "" {
			res.Attrs["url_path"] = m.URLPath
		}
		if m.ExpectedCodes != "" {
			res.Attrs["expected_codes"] = m.ExpectedCodes
		}
		for _, key := range []string{"created_at", "updated_at", "domain_name", "http_version"} {
			if value := rawString(raw, key); value != "" {
				res.Attrs[key] = value
			}
		}
		if tags := rawStringList(raw, "tags"); tags != "" {
			res.Attrs["tags"] = tags
		}
		res.Raw = raw

	case model.TypeL7Policy:
		r := l7policies.Get(ctx, sc.lb, n.ID)
		p, err := r.Extract()
		if err != nil {
			return res, err
		}
		res.Attrs["action"] = p.Action
		if p.RedirectURL != "" {
			res.Attrs["redirect_url"] = p.RedirectURL
		}
		res.IsL7Policy = true
		res.L7Action = p.Action
		res.L7RedirectPoolID = p.RedirectPoolID
		res.Raw = innerRaw(r.Body, "l7policy")

	case model.TypeL7Rule:
		policyID := parentID(n)
		if policyID == "" {
			return res, fmt.Errorf("l7rule detail needs its policy")
		}
		r := l7policies.GetRule(ctx, sc.lb, policyID, n.ID)
		rule, err := r.Extract()
		if err != nil {
			return res, err
		}
		res.Attrs["type"] = rule.RuleType
		res.Attrs["compare_type"] = rule.CompareType
		res.Attrs["value"] = rule.Value
		res.Raw = innerRaw(r.Body, "rule")

	default:
		// Floating IP / instance / amphora carry their detail from the
		// resolution step; expose whatever raw object is already attached.
		if m, ok := n.Raw.(map[string]any); ok {
			res.Raw = m
			return res, nil
		}
		return res, fmt.Errorf("no detail available for %s", n.Type)
	}
	return res, nil
}

// LBStats returns the byte/connection counters shown in the load-balancer
// overview (distinct from status show).
func (c *Clients) LBStats(ctx context.Context, lbID string) (map[string]any, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	s, err := loadbalancers.GetStats(ctx, sc.lb, lbID).Extract()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"active_connections": s.ActiveConnections,
		"total_connections":  s.TotalConnections,
		"bytes_in":           s.BytesIn,
		"bytes_out":          s.BytesOut,
		"request_errors":     s.RequestErrors,
	}, nil
}

// ListenerStats returns the byte/connection counters for one listener.
func (c *Clients) ListenerStats(ctx context.Context, lbID, listenerID string) (map[string]any, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	s, err := listeners.GetStats(ctx, sc.lb, listenerID).Extract()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"active_connections": s.ActiveConnections,
		"total_connections":  s.TotalConnections,
		"bytes_in":           s.BytesIn,
		"bytes_out":          s.BytesOut,
		"request_errors":     s.RequestErrors,
	}, nil
}

// ListListenerSummaries fetches every listener for one load balancer in a
// single filtered request. The status tree contains listener identities and
// health, but omits protocol and protocol_port.
func (c *Clients) ListListenerSummaries(ctx context.Context, lbID string) (map[string]ListenerSummary, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	pages, err := listeners.List(sc.lb, listeners.ListOpts{LoadbalancerID: lbID}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	items, err := listeners.ExtractListeners(pages)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ListenerSummary, len(items))
	for _, listener := range items {
		out[listener.ID] = ListenerSummary{
			ID: listener.ID, Protocol: listener.Protocol, ProtocolPort: listener.ProtocolPort,
		}
	}
	return out, nil
}

// ListPoolSummaries fetches every pool for one load balancer in a single
// filtered request. Besides enriching status-tree pools, this is an
// authoritative fallback for deployments that omit loadbalancer.pools.
func (c *Clients) ListPoolSummaries(ctx context.Context, lbID string) (map[string]PoolSummary, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	pages, err := pools.List(sc.lb, pools.ListOpts{LoadbalancerID: lbID}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	items, err := pools.ExtractPools(pages)
	if err != nil {
		return nil, err
	}
	out := make(map[string]PoolSummary, len(items))
	for _, pool := range items {
		listenerIDs := make([]string, 0, len(pool.Listeners))
		for _, listener := range pool.Listeners {
			listenerIDs = append(listenerIDs, listener.ID)
		}
		out[pool.ID] = PoolSummary{
			ID: pool.ID, Name: pool.Name, Protocol: pool.Protocol, LBMethod: pool.LBMethod,
			MemberCount: len(pool.Members), ProvisioningStatus: pool.ProvisioningStatus,
			OperatingStatus: pool.OperatingStatus, ListenerIDs: listenerIDs,
		}
	}
	return out, nil
}

// ResolveFloatingIPs looks up every floating IP mapped to the load balancer's
// VIP port, keyed by its fixed IP address. A multi-VIP port may have a distinct
// floating IP for each primary/additional address. The active credential
// strategy determines whether the clients are project-scoped or global.
func (c *Clients) ResolveFloatingIPs(ctx context.Context, lbID, portID string) (map[string]*model.Node, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	if sc.network == nil {
		return nil, ErrUnavailable
	}
	if portID == "" {
		return map[string]*model.Node{}, nil
	}
	pages, err := floatingips.List(sc.network, floatingips.ListOpts{PortID: portID}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	fips, err := floatingips.ExtractFloatingIPs(pages)
	if err != nil {
		return nil, err
	}
	return floatingIPNodes(fips), nil
}

func floatingIPNodes(fips []floatingips.FloatingIP) map[string]*model.Node {
	out := make(map[string]*model.Node, len(fips))
	for _, f := range fips {
		if f.FixedIP == "" {
			continue
		}
		node := model.NewNode(model.TypeFloatingIP, f.ID, f.FloatingIP)
		node.ProvisioningStatus = f.Status
		node.SetAttr("floating_ip", f.FloatingIP)
		node.SetAttr("fixed_ip", f.FixedIP)
		node.SetAttr("port_id", f.PortID)
		node.Raw = rawFIP(f)
		node.DetailLoaded = true
		out[f.FixedIP] = node
	}
	return out
}

// ResolveInstance finds the Nova server whose fixed IP matches a member address.
// lbID selects the project scope. Best-effort: returns (nil, nil) if no server
// matches, ErrUnavailable if Nova is not in scope.
func (c *Clients) ResolveInstance(ctx context.Context, lbID, address string) (*model.Node, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	if sc.compute == nil {
		return nil, ErrUnavailable
	}
	if address == "" {
		return nil, nil
	}
	pages, err := servers.List(sc.compute, servers.ListOpts{IP: address}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	srvs, err := servers.ExtractServers(pages)
	if err != nil {
		return nil, err
	}
	if len(srvs) == 0 {
		return nil, nil
	}
	s := srvs[0]
	node := model.NewNode(model.TypeInstance, s.ID, s.Name)
	node.OperatingStatus = s.Status
	node.SetAttr("status", s.Status)
	node.SetAttr("address", address)
	node.Raw = map[string]any{"id": s.ID, "name": s.Name, "status": s.Status}
	node.DetailLoaded = true
	return node, nil
}

// ListAmphorae returns the amphora VMs backing a load balancer. Admin-only:
// a 403 is translated to ErrAdminRequired so the caller can degrade gracefully
// rather than surface a raw error. Not applicable to OVN-backed LBs.
func (c *Clients) ListAmphorae(ctx context.Context, lbID string) ([]*model.Node, error) {
	sc, err := c.clientsForLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	pages, err := amphorae.List(sc.lb, amphorae.ListOpts{LoadbalancerID: lbID}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	as, err := amphorae.ExtractAmphorae(pages)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Node, 0, len(as))
	for _, a := range as {
		out = append(out, amphoraNode(a, lbID))
	}
	return out, nil
}

// amphoraNode builds a model node from an amphora, keyed for both the per-LB
// overview and the top-level amphorae list. owningLBID lets the caller supply the
// query-scoped LB for the per-LB list or the amphora's own loadbalancer_id for
// the cluster-wide list.
func amphoraNode(a amphorae.Amphora, owningLBID string) *model.Node {
	n := model.NewNode(model.TypeAmphora, a.ID, a.ID)
	n.OwningLBID = owningLBID
	n.ProvisioningStatus = a.Status
	n.SetAttr("role", a.Role)
	n.SetAttr("status", a.Status)
	n.SetAttr("lb_network_ip", a.LBNetworkIP)
	n.SetAttr("ha_ip", a.HAIP)
	n.SetAttr("compute_id", a.ComputeID)
	n.Raw = map[string]any{
		"id": a.ID, "loadbalancer_id": a.LoadbalancerID, "compute_id": a.ComputeID,
		"role": a.Role, "status": a.Status, "lb_network_ip": a.LBNetworkIP, "ha_ip": a.HAIP,
	}
	n.DetailLoaded = true
	return n
}

// innerRaw pulls the wrapped object out of a gophercloud response body,
// e.g. {"listener": {...}} -> {...}.
func innerRaw(body any, key string) map[string]any {
	m, ok := body.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if inner, ok := m[key].(map[string]any); ok {
		return inner
	}
	return m
}

func rawString(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func rawStringList(raw map[string]any, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	var values []string
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				values = append(values, text)
			}
		}
	case []string:
		for _, item := range items {
			if text := strings.TrimSpace(item); text != "" {
				values = append(values, text)
			}
		}
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return strings.Join(values, ", ")
}

func rawFIP(f floatingips.FloatingIP) map[string]any {
	return map[string]any{
		"id": f.ID, "floating_ip_address": f.FloatingIP, "fixed_ip_address": f.FixedIP, "port_id": f.PortID,
		"status": f.Status, "description": f.Description,
	}
}

func parentID(n *model.Node) string {
	if n.Parent != nil {
		return n.Parent.ID
	}
	return ""
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func formatAPITime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
