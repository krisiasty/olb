package tui

import (
	"fmt"
	"strings"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// listKind identifies which top-level list view is active. Each is reached by a
// number key within its area (see area.go) and rendered as a table; the LB list
// is the historical default and the others are cross-cutting views.
type listKind int

const (
	kindLB listKind = iota
	kindVIP
	kindListener
	kindPool
	kindAmphora
	kindUser
	kindDomain
	kindGroup
	kindProject
	kindRole
	kindService
	kindEndpoint
	kindRegion
	kindInstance
)

// listIdentity is the synthetic history identity for a top-level list kind.
func (k listKind) identity() model.Identity {
	switch k {
	case kindVIP:
		return model.VIPListIdentity
	case kindListener:
		return model.ListenerListIdentity
	case kindPool:
		return model.PoolListIdentity
	case kindAmphora:
		return model.AmphoraListIdentity
	case kindUser:
		return model.UserListIdentity
	case kindDomain:
		return model.DomainListIdentity
	case kindGroup:
		return model.GroupListIdentity
	case kindProject:
		return model.ProjectListIdentity
	case kindRole:
		return model.RoleListIdentity
	case kindService:
		return model.ServiceListIdentity
	case kindEndpoint:
		return model.EndpointListIdentity
	case kindRegion:
		return model.RegionListIdentity
	case kindInstance:
		return model.InstanceListIdentity
	default:
		return model.LBListIdentity
	}
}

// rootLabel is the breadcrumb root shown while this list is the active boundary.
func (k listKind) rootLabel() string {
	switch k {
	case kindVIP:
		return "virtual IPs"
	case kindListener:
		return "listeners"
	case kindPool:
		return "pools"
	case kindAmphora:
		return "amphorae"
	case kindUser:
		return "users"
	case kindDomain:
		return "domains"
	case kindGroup:
		return "groups"
	case kindProject:
		return "projects"
	case kindRole:
		return "roles"
	case kindService:
		return "services"
	case kindEndpoint:
		return "endpoints"
	case kindRegion:
		return "regions"
	case kindInstance:
		return "instances"
	default:
		return "load balancers"
	}
}

// listKindOf maps a top-level list identity to its kind (kindLB for anything
// that is not a resource list, including the LB list).
func listKindOf(id model.Identity) listKind {
	switch id.Type {
	case model.TypeVIP:
		return kindVIP
	case model.TypeListener:
		return kindListener
	case model.TypePool:
		return kindPool
	case model.TypeAmphora:
		return kindAmphora
	case model.TypeUser:
		return kindUser
	case model.TypeDomain:
		return kindDomain
	case model.TypeGroup:
		return kindGroup
	case model.TypeProject:
		return kindProject
	case model.TypeRole:
		return kindRole
	case model.TypeService:
		return kindService
	case model.TypeEndpoint:
		return kindEndpoint
	case model.TypeRegion:
		return kindRegion
	case model.TypeInstance:
		return kindInstance
	default:
		return kindLB
	}
}

func (l location) isTopLevelList() bool { return l.node == nil && l.id.IsTopLevelList() }

func (l location) listKind() listKind { return listKindOf(l.id) }

// vipRow is one VIP address, derived from the load-balancer list rather than a
// standalone API object. A load balancer contributes its primary VIP plus one
// row per additional VIP.
type vipRow struct {
	address    string
	floatingIP string
	portID     string
	subnetID   string
	networkID  string
	lbID       string
	lbName     string
	nodeID     string // VIP node id used to drill into the owning LB's tree
	additional bool
}

type vipAddressKey struct {
	portID  string
	address string
}

// deriveVIPs expands the load-balancer list into one row per VIP address. The
// lookup retains mappings only for visible VIPs, which bounds memory in a
// broader system/domain scope where Neutron may return unrelated floating IPs.
func deriveVIPs(lbs []osclient.LB, mappings []osclient.FloatingIPMapping) []vipRow {
	wanted := make(map[vipAddressKey]struct{}, len(lbs))
	for _, lb := range lbs {
		if lb.VipAddress != "" {
			wanted[vipAddressKey{portID: lb.VipPortID, address: lb.VipAddress}] = struct{}{}
		}
		for _, extra := range lb.AdditionalVIPs {
			if extra.Address != "" {
				wanted[vipAddressKey{portID: lb.VipPortID, address: extra.Address}] = struct{}{}
			}
		}
	}
	floatingIPs := make(map[vipAddressKey]string, len(wanted))
	for _, mapping := range mappings {
		key := vipAddressKey{portID: mapping.PortID, address: mapping.FixedIP}
		if _, visible := wanted[key]; visible {
			floatingIPs[key] = mapping.FloatingIP
		}
	}
	rows := make([]vipRow, 0, len(lbs))
	for _, lb := range lbs {
		if lb.VipAddress != "" {
			rows = append(rows, vipRow{
				address: lb.VipAddress, floatingIP: floatingIPs[vipAddressKey{portID: lb.VipPortID, address: lb.VipAddress}],
				portID:   lb.VipPortID,
				subnetID: lb.VipSubnetID, networkID: lb.VipNetworkID,
				lbID: lb.ID, lbName: lb.Name, nodeID: lb.VipPortID,
			})
		}
		for _, extra := range lb.AdditionalVIPs {
			if extra.Address == "" {
				continue
			}
			rows = append(rows, vipRow{
				address: extra.Address, floatingIP: floatingIPs[vipAddressKey{portID: lb.VipPortID, address: extra.Address}],
				portID:   lb.VipPortID,
				subnetID: extra.SubnetID, networkID: lb.VipNetworkID,
				lbID: lb.ID, lbName: lb.Name,
				nodeID:     model.AdditionalVIPID(lb.ID, model.AdditionalVIP{Address: extra.Address, SubnetID: extra.SubnetID}),
				additional: true,
			})
		}
	}
	return rows
}

// lbNameByID maps load-balancer IDs to names from the currently loaded LB list,
// so resource rows can label their owning load balancer.
func (m Model) lbNameByID() map[string]string {
	names := make(map[string]string, len(m.lbs))
	for _, lb := range m.lbs {
		names[lb.ID] = lb.Name
	}
	return names
}

// --- entry builders -------------------------------------------------------

func vipEntries(vips []vipRow) []entry {
	es := make([]entry, 0, len(vips))
	for _, v := range vips {
		label := "vip:" + v.address
		es = append(es, entry{
			kind: entVIP, vip: v, lbName: v.lbName,
			label: label, extra: strings.TrimSpace(v.floatingIP + " " + v.lbName + " " + v.subnetID + " " + v.networkID),
		})
	}
	return es
}

func listenerEntries(rows []osclient.ListenerRow, lbNames map[string]string) []entry {
	es := make([]entry, 0, len(rows))
	for _, r := range rows {
		name := r.Name
		if name == "" {
			name = shortID(r.ID)
		}
		lbName := lbNames[r.LBID]
		es = append(es, entry{
			kind: entListener, listener: r, lbName: lbName,
			label: "listener:" + name, oper: r.OperatingStatus, prov: r.ProvisioningStatus,
			extra: strings.TrimSpace(fmt.Sprintf("%s %d %s", r.Protocol, r.ProtocolPort, lbName)),
		})
	}
	return es
}

func poolEntries(rows []osclient.PoolRow, lbNames map[string]string) []entry {
	es := make([]entry, 0, len(rows))
	for _, r := range rows {
		name := r.Name
		if name == "" {
			name = shortID(r.ID)
		}
		lbName := lbNames[r.LBID]
		es = append(es, entry{
			kind: entPool, pool: r, lbName: lbName,
			label: "pool:" + name, oper: r.OperatingStatus, prov: r.ProvisioningStatus,
			extra: strings.TrimSpace(fmt.Sprintf("%s %s %s", r.Protocol, r.LBMethod, lbName)),
		})
	}
	return es
}

func amphoraEntries(nodes []*model.Node, lbNames map[string]string, filterToLBs bool) []entry {
	es := make([]entry, 0, len(nodes))
	for _, n := range nodes {
		lbName, visible := lbNames[n.OwningLBID]
		if filterToLBs && !visible {
			continue
		}
		es = append(es, entry{
			kind: entAmphora, node: n, lbName: lbName,
			label: "amphora:" + shortID(n.ID), prov: n.ProvisioningStatus,
			extra: strings.TrimSpace(n.Attrs["role"] + " " + n.Attrs["lb_network_ip"] + " " + lbName),
		})
	}
	return es
}

func instanceEntries(rows []osclient.Instance) []entry {
	es := make([]entry, 0, len(rows))
	for _, instance := range rows {
		name := instance.Name
		if name == "" {
			name = shortID(instance.ID)
		}
		es = append(es, entry{
			kind: entInstance, instance: instance,
			label: "instance:" + name, oper: instance.Status,
			extra: strings.Join([]string{
				instance.ProjectName, instance.ProjectID,
				instance.FlavorName, instance.FlavorID,
				strings.Join(instance.Addresses, " "),
			}, " "),
		})
	}
	return es
}

// --- table columns & cells ------------------------------------------------

// columnTitles returns the table headers for the active top-level list. The d
// toggle (showIDs) relabels the object and owning-LB columns to their id form.
func (m Model) columnTitles() []string {
	switch m.loc.listKind() {
	case kindVIP:
		return []string{"ADDRESS", "FLOATING IP", "PORT ID", "SUBNET", "NETWORK", m.lbColTitle()}
	case kindListener:
		obj := "NAME"
		if m.showIDs {
			obj = "LISTENER ID"
		}
		return []string{obj, "PROTOCOL", "PORT", m.lbColTitle(), "PROVISIONING", "OPERATING"}
	case kindPool:
		obj := "NAME"
		if m.showIDs {
			obj = "POOL ID"
		}
		return []string{obj, "PROTOCOL", "ALGORITHM", "MEMBERS", m.lbColTitle(), "PROVISIONING", "OPERATING"}
	case kindAmphora:
		return []string{"AMPHORA ID", "ROLE", "STATUS", "LB NETWORK IP", "HA IP", m.lbColTitle(), "COMPUTE ID"}
	case kindUser:
		return userColumnTitles(m.showIDs)
	case kindDomain:
		return domainColumnTitles(m.showIDs)
	case kindGroup:
		return groupColumnTitles(m.showIDs)
	case kindProject:
		return projectColumnTitles(m.showIDs)
	case kindRole:
		return roleColumnTitles(m.showIDs, m.rolesRestriction != "")
	case kindService:
		return serviceColumnTitles(m.showIDs)
	case kindEndpoint:
		return endpointColumnTitles(m.showIDs)
	case kindRegion:
		return regionColumnTitles(m.showIDs)
	case kindInstance:
		obj, flavor := "NAME", "FLAVOR"
		if m.showIDs {
			obj, flavor = "INSTANCE ID", "FLAVOR ID"
		}
		cols := []string{obj, "STATUS"}
		if m.multiProjectScope {
			project := "PROJECT"
			if m.showIDs {
				project = "PROJECT ID"
			}
			cols = append(cols, project)
		}
		return append(cols, flavor, "ADDRESSES", "CREATED")
	default:
		return m.lbColumnTitles()
	}
}

// lbColTitle is the owning-load-balancer column header, id or name per the toggle.
func (m Model) lbColTitle() string {
	if m.showIDs {
		return "LOAD BALANCER ID"
	}
	return "LOAD BALANCER"
}

// rowCells returns the table cells for one row, per its kind and the id toggle.
func (m Model) rowCells(e entry) []string {
	switch e.kind {
	case entVIP:
		v := e.vip
		return []string{v.address, displayValue(v.floatingIP), idCell(v.portID, m.showIDs), idCell(v.subnetID, m.showIDs),
			idCell(v.networkID, m.showIDs), lbNameCell(v.lbName, v.lbID, m.showIDs)}
	case entListener:
		r := e.listener
		return []string{lbNameCell(r.Name, r.ID, m.showIDs), listenerProtocolLabel(r.Protocol), fmt.Sprintf("%d", r.ProtocolPort),
			lbNameCell(e.lbName, r.LBID, m.showIDs), r.ProvisioningStatus, r.OperatingStatus}
	case entPool:
		r := e.pool
		return []string{lbNameCell(r.Name, r.ID, m.showIDs), r.Protocol, r.LBMethod, fmt.Sprintf("%d", r.MemberCount),
			lbNameCell(e.lbName, r.LBID, m.showIDs), r.ProvisioningStatus, r.OperatingStatus}
	case entAmphora:
		n := e.node
		return []string{idCell(n.ID, m.showIDs), n.Attrs["role"], n.Attrs["status"],
			n.Attrs["lb_network_ip"], n.Attrs["ha_ip"], lbNameCell(e.lbName, n.OwningLBID, m.showIDs),
			idCell(n.Attrs["compute_id"], m.showIDs)}
	case entUser:
		return userRowCells(e, m.showIDs)
	case entDomain:
		return domainRowCells(e, m.showIDs)
	case entUserGroup:
		return groupRowCells(e, m.showIDs)
	case entProject:
		return projectRowCells(e, m.showIDs)
	case entRole:
		return roleRowCells(e, m.showIDs)
	case entService:
		return serviceRowCells(e, m.showIDs)
	case entEndpoint:
		return endpointRowCells(e, m.showIDs)
	case entRegion:
		return regionRowCells(e, m.showIDs)
	case entInstance:
		instance := e.instance
		first := lbNameCell(instance.Name, instance.ID, m.showIDs)
		flavor := lbNameCell(instance.FlavorName, instance.FlavorID, m.showIDs)
		cells := []string{first, instance.Status}
		if m.multiProjectScope {
			cells = append(cells, lbNameCell(instance.ProjectName, instance.ProjectID, m.showIDs))
		}
		return append(cells, flavor, displayValue(strings.Join(instance.Addresses, ", ")), formatTableTime(instance.Created))
	default:
		return m.lbRowCells(e)
	}
}

// topLevelFilterText follows the active display mode: name-oriented columns
// contribute names, while ID-oriented columns contribute IDs. Other visible
// columns remain searchable in either mode.
func (m Model) topLevelFilterText(e entry) string {
	cells := m.rowCells(e)
	if e.kind == entUser && e.user.Service {
		// The gear marker denotes a service account; retain its documented
		// textual alias even though the word itself is not printed in the row.
		cells = append(cells, "service")
	}
	return strings.ToLower(strings.Join(cells, " "))
}

// statusColumnSet returns the column indices to color by status for the active
// list: the trailing PROVISIONING/OPERATING pair for LB/listener/pool, the single
// STATUS column for amphorae, and none for VIPs.
func (m Model) statusColumnSet(ncols int) map[int]bool {
	switch m.loc.listKind() {
	case kindVIP, kindUser, kindDomain, kindGroup, kindProject, kindRole, kindService, kindEndpoint, kindRegion:
		return map[int]bool{}
	case kindAmphora:
		return map[int]bool{2: true} // STATUS
	case kindInstance:
		return map[int]bool{1: true} // STATUS
	default:
		return map[int]bool{ncols - 1: true, ncols - 2: true}
	}
}

// idCell shows a UUID-ish value: full in id mode, shortened otherwise.
func idCell(id string, showIDs bool) string {
	if showIDs {
		return id
	}
	return shortID(id)
}
