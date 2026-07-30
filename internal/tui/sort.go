package tui

import (
	"net/netip"
	"sort"
	"strings"
	"time"
)

// sortColumn is one selectable sort key for a top-level list. An empty key is
// the leading "default order" entry (natural API order); its value func is nil.
// New workspaces select defaultNameSortKey instead, so lists initially appear
// in human-readable name order while API order remains an explicit option. ip
// selects numeric IP-aware ordering instead of a lexical string compare.
type sortColumn struct {
	key   string
	label string
	ip    bool
	value func(entry) string
}

// sortColumns returns the sort options for the active top-level list: a leading
// "default order" entry followed by that view's name/id/IP columns. Non-list
// views are not sortable and return nil.
func (m Model) sortColumns() []sortColumn {
	if !m.loc.isTopLevelList() {
		return nil
	}
	def := sortColumn{key: "", label: "default order"}
	switch m.loc.listKind() {
	case kindLB:
		cols := []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.lb.Name }},
			{key: "id", label: "Load balancer ID", value: func(e entry) string { return e.lb.ID }},
		}
		if m.multiProjectScope {
			cols = append(cols, sortColumn{key: "project", label: "Project", value: func(e entry) string {
				if e.lb.ProjectName != "" {
					return e.lb.ProjectName
				}
				return e.lb.ProjectID
			}})
		}
		return append(cols, sortColumn{key: "vip", label: "VIP address", ip: true, value: func(e entry) string { return e.lb.VipAddress }})
	case kindVIP:
		return []sortColumn{
			def,
			{key: "address", label: "Address", ip: true, value: func(e entry) string { return e.vip.address }},
			{key: "floating_ip", label: "Floating IP", ip: true, value: func(e entry) string { return e.vip.floatingIP }},
			{key: "lb", label: "Load balancer", value: func(e entry) string { return e.vip.lbName }},
			{key: "port_id", label: "Port ID", value: func(e entry) string { return e.vip.portID }},
		}
	case kindListener:
		return []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.listener.Name }},
			{key: "id", label: "Listener ID", value: func(e entry) string { return e.listener.ID }},
			{key: "lb", label: "Load balancer", value: func(e entry) string { return e.lbName }},
		}
	case kindPool:
		return []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.pool.Name }},
			{key: "id", label: "Pool ID", value: func(e entry) string { return e.pool.ID }},
			{key: "lb", label: "Load balancer", value: func(e entry) string { return e.lbName }},
		}
	case kindAmphora:
		return []sortColumn{
			def,
			{key: "id", label: "Amphora ID", value: func(e entry) string { return e.node.ID }},
			{key: "lb_network_ip", label: "LB network IP", ip: true, value: func(e entry) string { return e.node.Attrs["lb_network_ip"] }},
			{key: "ha_ip", label: "HA IP", ip: true, value: func(e entry) string { return e.node.Attrs["ha_ip"] }},
			{key: "lb", label: "Load balancer", value: func(e entry) string { return e.lbName }},
			{key: "compute_id", label: "Compute ID", value: func(e entry) string { return e.node.Attrs["compute_id"] }},
		}
	case kindUser:
		return []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.user.Name }},
			{key: "id", label: "User ID", value: func(e entry) string { return e.user.ID }},
			{key: "description", label: "Description", value: func(e entry) string { return e.user.Description }},
			{key: "email", label: "Email", value: func(e entry) string { return e.user.Email }},
			{key: "domain_name", label: "Domain name", value: func(e entry) string { return e.user.DomainName }},
			{key: "domain_id", label: "Domain ID", value: func(e entry) string { return e.user.DomainID }},
			{key: "project_name", label: "Default project", value: func(e entry) string { return e.user.DefaultProjectName }},
			{key: "enabled", label: "Enabled", value: func(e entry) string { return enabledSortValue(e.user.Enabled) }},
		}
	case kindDomain:
		return []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.domain.Name }},
			{key: "id", label: "Domain ID", value: func(e entry) string { return e.domain.ID }},
			{key: "description", label: "Description", value: func(e entry) string { return e.domain.Description }},
			{key: "enabled", label: "Enabled", value: func(e entry) string { return enabledSortValue(e.domain.Enabled) }},
		}
	case kindGroup:
		return []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.group.Name }},
			{key: "id", label: "Group ID", value: func(e entry) string { return e.group.ID }},
			{key: "description", label: "Description", value: func(e entry) string { return e.group.Description }},
			{key: "domain_name", label: "Domain name", value: func(e entry) string { return e.group.DomainName }},
			{key: "domain_id", label: "Domain ID", value: func(e entry) string { return e.group.DomainID }},
		}
	case kindProject:
		return []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.project.Name }},
			{key: "id", label: "Project ID", value: func(e entry) string { return e.project.ID }},
			{key: "description", label: "Description", value: func(e entry) string { return e.project.Description }},
			{key: "domain_name", label: "Domain name", value: func(e entry) string { return e.project.DomainName }},
			{key: "domain_id", label: "Domain ID", value: func(e entry) string { return e.project.DomainID }},
			{key: "enabled", label: "Enabled", value: func(e entry) string { return enabledSortValue(e.project.Enabled) }},
		}
	case kindRole:
		cols := []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.role.Name }},
			{key: "id", label: "Role ID", value: func(e entry) string { return e.role.ID }},
		}
		if m.rolesRestriction != "" {
			return append(cols, sortColumn{key: "scope", label: "Scope", value: func(e entry) string {
				return tokenRoleScope(e.role)
			}})
		}
		return append(cols,
			sortColumn{key: "description", label: "Description", value: func(e entry) string { return e.role.Description }},
			sortColumn{key: "domain_name", label: "Domain name", value: func(e entry) string { return e.role.DomainName }},
		)
	case kindService:
		return []sortColumn{
			def,
			{key: "type", label: "Type", value: func(e entry) string { return e.service.Type }},
			{key: "id", label: "Service ID", value: func(e entry) string { return e.service.ID }},
			{key: "name", label: "Name", value: func(e entry) string { return e.service.Name }},
			{key: "description", label: "Description", value: func(e entry) string { return e.service.Description }},
			{key: "enabled", label: "Enabled", value: func(e entry) string { return enabledSortValue(e.service.Enabled) }},
		}
	case kindEndpoint:
		return []sortColumn{
			def,
			{key: "service", label: "Service", value: func(e entry) string { return endpointServiceLabel(e.endpoint) }},
			{key: "interface", label: "Interface", value: func(e entry) string { return e.endpoint.Interface }},
			{key: "region", label: "Region", value: func(e entry) string { return e.endpoint.RegionID }},
			{key: "url", label: "URL", value: func(e entry) string { return e.endpoint.URL }},
			{key: "enabled", label: "Enabled", value: func(e entry) string { return enabledSortValue(e.endpoint.Enabled) }},
		}
	case kindRegion:
		return []sortColumn{
			def,
			{key: "id", label: "Region", value: func(e entry) string { return e.region.ID }},
			{key: "parent", label: "Parent region", value: func(e entry) string { return e.region.ParentRegionID }},
			{key: "description", label: "Description", value: func(e entry) string { return e.region.Description }},
		}
	case kindInstance:
		cols := []sortColumn{
			def,
			{key: "name", label: "Name", value: func(e entry) string { return e.instance.Name }},
			{key: "id", label: "Instance ID", value: func(e entry) string { return e.instance.ID }},
			{key: "status", label: "Status", value: func(e entry) string { return e.instance.Status }},
		}
		if m.multiProjectScope {
			cols = append(cols, sortColumn{key: "project", label: "Project", value: func(e entry) string {
				if e.instance.ProjectName != "" {
					return e.instance.ProjectName
				}
				return e.instance.ProjectID
			}})
		}
		return append(cols,
			sortColumn{key: "flavor", label: "Flavor", value: func(e entry) string {
				if e.instance.FlavorName != "" {
					return e.instance.FlavorName
				}
				return e.instance.FlavorID
			}},
			sortColumn{key: "address", label: "Address", ip: true, value: func(e entry) string { return e.instance.PrimaryAddress }},
			sortColumn{key: "created", label: "Created", value: func(e entry) string { return e.instance.Created.Format(time.RFC3339Nano) }},
		)
	case kindHypervisor:
		return []sortColumn{
			def,
			{key: "hostname", label: "Hostname", value: func(e entry) string { return e.hypervisor.Hostname }},
			{key: "id", label: "Hypervisor ID", value: func(e entry) string { return e.hypervisor.ID }},
			{key: "state", label: "State", value: func(e entry) string { return e.hypervisor.State }},
			{key: "status", label: "Status", value: func(e entry) string { return e.hypervisor.Status }},
			{key: "type", label: "Type", value: func(e entry) string { return e.hypervisor.Type }},
			{key: "host_ip", label: "Host IP", ip: true, value: func(e entry) string { return e.hypervisor.HostIP }},
		}
	}
	return nil
}

// defaultNameSortKey returns the human-readable identity column selected when
// a list workspace is first created. A few OpenStack resources do not have a
// name; those use the closest equivalent visible identity (service, region ID,
// load balancer name, or hostname).
func defaultNameSortKey(kind listKind) string {
	switch kind {
	case kindVIP, kindAmphora:
		return "lb"
	case kindEndpoint:
		return "service"
	case kindRegion:
		return "id"
	case kindHypervisor:
		return "hostname"
	default:
		return "name"
	}
}

// enabledSortValue maps the boolean enabled flag to a stable sort key. Ascending
// order groups disabled users ("no") before enabled ones ("yes").
func enabledSortValue(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

// activeSortColumn resolves the workspace's stored sort key to a live column for
// the current view. It reports false for the default order and for a stored key
// that no longer applies (e.g. "project" after entering a project scope).
func (m Model) activeSortColumn() (sortColumn, bool) {
	if m.sortKey == "" {
		return sortColumn{}, false
	}
	for _, c := range m.sortColumns() {
		if c.key == m.sortKey && c.value != nil {
			return c, true
		}
	}
	return sortColumn{}, false
}

// sortEntries orders the visible rows by the active sort column, ascending. The
// stable sort preserves API order for ties; it is a no-op when no sort is active
// (including on every non-top-level view, whose sortColumns is empty).
func (m Model) sortEntries(rows []entry) {
	col, ok := m.activeSortColumn()
	if !ok {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return col.less(col.value(rows[i]), col.value(rows[j]))
	})
}

func (c sortColumn) less(a, b string) bool {
	if c.ip {
		return ipLess(a, b)
	}
	return strings.ToLower(strings.TrimSpace(a)) < strings.ToLower(strings.TrimSpace(b))
}

// ipLess orders IP-address strings numerically (so 10.0.0.2 precedes 10.0.0.10).
// Unparseable or empty values — e.g. an internal LB with no floating IP — sort
// last, keeping real addresses at the top of an ascending sort.
func ipLess(a, b string) bool {
	ipA, errA := netip.ParseAddr(strings.TrimSpace(a))
	ipB, errB := netip.ParseAddr(strings.TrimSpace(b))
	switch {
	case errA == nil && errB == nil:
		return ipA.Less(ipB)
	case errA == nil:
		return true
	case errB == nil:
		return false
	default:
		return strings.TrimSpace(a) < strings.TrimSpace(b)
	}
}
