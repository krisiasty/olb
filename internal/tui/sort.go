package tui

import (
	"net/netip"
	"sort"
	"strings"
)

// sortColumn is one selectable sort key for a top-level list. Per the design,
// only name, id, and IP-address columns are offered. An empty key is the
// leading "default order" entry (natural API order); its value func is nil. ip
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
		if m.allProjects {
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
	}
	return nil
}

// activeSortColumn resolves the workspace's stored sort key to a live column for
// the current view. It reports false for the default order and for a stored key
// that no longer applies (e.g. "project" after leaving the all-projects view).
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
