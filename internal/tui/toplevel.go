package tui

import (
	"fmt"
	"strings"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// listKind identifies which top-level list view is active. Each is reached by a
// number key (1-5) and rendered as a table; the LB list is the historical
// default and the others are cross-cutting views of the same objects.
type listKind int

const (
	kindLB listKind = iota
	kindVIP
	kindListener
	kindPool
	kindAmphora
)

// topLevelKinds is the number-key order: 1=LB, 2=VIP, 3=listener, 4=pool,
// 5=amphora. The index is the key digit minus one.
var topLevelKinds = [...]listKind{kindLB, kindVIP, kindListener, kindPool, kindAmphora}

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
	portID     string
	subnetID   string
	networkID  string
	lbID       string
	lbName     string
	nodeID     string // VIP node id used to drill into the owning LB's tree
	additional bool
}

// deriveVIPs expands the load-balancer list into one row per VIP address.
func deriveVIPs(lbs []osclient.LB) []vipRow {
	rows := make([]vipRow, 0, len(lbs))
	for _, lb := range lbs {
		if lb.VipAddress != "" {
			rows = append(rows, vipRow{
				address: lb.VipAddress, portID: lb.VipPortID,
				subnetID: lb.VipSubnetID, networkID: lb.VipNetworkID,
				lbID: lb.ID, lbName: lb.Name, nodeID: lb.VipPortID,
			})
		}
		for _, extra := range lb.AdditionalVIPs {
			if extra.Address == "" {
				continue
			}
			rows = append(rows, vipRow{
				address: extra.Address, portID: lb.VipPortID,
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
			label: label, extra: strings.TrimSpace(v.lbName + " " + v.subnetID + " " + v.networkID),
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

// --- table columns & cells ------------------------------------------------

// columnTitles returns the table headers for the active top-level list. The d
// toggle (showIDs) relabels the object and owning-LB columns to their id form.
func (m Model) columnTitles() []string {
	switch m.loc.listKind() {
	case kindVIP:
		return []string{"ADDRESS", "PORT ID", "SUBNET", "NETWORK", m.lbColTitle()}
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
		return []string{v.address, idCell(v.portID, m.showIDs), idCell(v.subnetID, m.showIDs),
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
	default:
		return m.lbRowCells(e)
	}
}

// statusColumnSet returns the column indices to color by status for the active
// list: the trailing PROVISIONING/OPERATING pair for LB/listener/pool, the single
// STATUS column for amphorae, and none for VIPs.
func (m Model) statusColumnSet(ncols int) map[int]bool {
	switch m.loc.listKind() {
	case kindVIP:
		return map[int]bool{}
	case kindAmphora:
		return map[int]bool{2: true} // STATUS
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
