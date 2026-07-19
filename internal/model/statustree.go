package model

// The types below mirror the reduced object graph returned by
//
//	GET /v2/lbaas/loadbalancers/{id}/status   ("loadbalancer status show")
//
// This single call returns the whole nested tree with provisioning_status and
// operating_status on every node, which is why the tool builds structure from
// it rather than fanning out N+1 list calls. The full per-object configuration
// (algorithms, weights, thresholds, and crucially the default_pool_id /
// redirect_pool_id that back the reference edges) is NOT in this response and
// is loaded lazily via per-object show; see build.go.
//
// Field-name notes verified against the canonical Octavia api-ref sample:
//   - the health monitor key is "healthmonitor" (no underscore); some
//     deployments emit "health_monitor" — both are accepted.
//   - the loadbalancer carries a top-level "pools" array listing every pool
//     (including shared pools and pools attached to no listener), which is the
//     authoritative, de-duplicated pool set.
//   - an absent health monitor serializes as an empty object {}, so presence is
//     judged by a non-empty id, not by the pointer being non-nil.

// StatusTree is the top-level wrapper of a status show response.
type StatusTree struct {
	LoadBalancer StatusLB `json:"loadbalancer"`
}

type StatusLB struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	ProvisioningStatus string           `json:"provisioning_status"`
	OperatingStatus    string           `json:"operating_status"`
	Listeners          []StatusListener `json:"listeners"`
	Pools              []StatusPool     `json:"pools"`
}

type StatusListener struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	ProvisioningStatus string           `json:"provisioning_status"`
	OperatingStatus    string           `json:"operating_status"`
	Pools              []StatusPool     `json:"pools"`
	L7Policies         []StatusL7Policy `json:"l7policies"`
}

type StatusPool struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	ProvisioningStatus string               `json:"provisioning_status"`
	OperatingStatus    string               `json:"operating_status"`
	HealthMonitor      *StatusHealthMonitor `json:"healthmonitor"`
	HealthMonitorAlt   *StatusHealthMonitor `json:"health_monitor"`
	Members            []StatusMember       `json:"members"`
}

// monitor returns the health monitor under whichever key the deployment used,
// or nil when the pool has none (absent, or serialized as an empty object).
func (p StatusPool) monitor() *StatusHealthMonitor {
	if p.HealthMonitor != nil && p.HealthMonitor.ID != "" {
		return p.HealthMonitor
	}
	if p.HealthMonitorAlt != nil && p.HealthMonitorAlt.ID != "" {
		return p.HealthMonitorAlt
	}
	return nil
}

type StatusHealthMonitor struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Type               string `json:"type"`
	ProvisioningStatus string `json:"provisioning_status"`
	OperatingStatus    string `json:"operating_status"`
}

type StatusMember struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Address            string `json:"address"`
	ProtocolPort       int    `json:"protocol_port"`
	ProvisioningStatus string `json:"provisioning_status"`
	OperatingStatus    string `json:"operating_status"`
}

type StatusL7Policy struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Action             string         `json:"action"`
	ProvisioningStatus string         `json:"provisioning_status"`
	OperatingStatus    string         `json:"operating_status"`
	Rules              []StatusL7Rule `json:"rules"`
}

type StatusL7Rule struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	ProvisioningStatus string `json:"provisioning_status"`
	OperatingStatus    string `json:"operating_status"`
}

// LBMeta carries the few LB-level facts that live outside the status tree but
// are already known from the load-balancer list (or a cheap get): the VIP,
// provider driver, and owning project. The provider decides whether the
// amphora branch applies. ProjectName can be empty when Keystone cannot supply
// a friendly name; ProjectID remains authoritative.
type LBMeta struct {
	VipAddress     string
	VipPortID      string
	VipSubnetID    string
	VipNetworkID   string
	AdditionalVIPs []AdditionalVIP
	Provider       string
	ProjectID      string
	ProjectName    string
}

// AdditionalVIP is an extra fixed address attached to the load balancer's VIP
// port. Octavia identifies the requested network by subnet; Neutron identifies
// any floating IP association by the concrete fixed address.
type AdditionalVIP struct {
	Address  string
	SubnetID string
}

// IsOVN reports whether the LB is backed by the OVN provider driver, for which
// no amphora objects exist and L7 support is limited/absent.
func (m LBMeta) IsOVN() bool {
	return m.Provider == "ovn"
}
