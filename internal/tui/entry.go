package tui

import (
	"strings"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// entryKind classifies a selectable row.
type entryKind int

const (
	entLB      entryKind = iota // a load balancer in the top-level list
	entChild                    // a containment child of the current node
	entRef                      // an outgoing reference edge ("→")
	entBackRef                  // an incoming back-reference ("←")
)

// entry is one selectable row. enter follows whatever is selected — a
// containment child or a reference — so there is no separate "jump" key.
type entry struct {
	kind entryKind

	lb   osclient.LB // set for entLB
	node *model.Node // child node, or resolved edge target
	edge *model.Edge // set for entRef / entBackRef

	label        string // target label, e.g. "pool:backend-v2"
	relationship string // edge relationship, e.g. "default pool"
	oper         string
	prov         string
	extra        string // list-only trailing facts (provider, vip)
}

func (e entry) filterText() string {
	return strings.ToLower(e.label + " " + e.relationship + " " + e.extra)
}

// identity returns the destination identity when this row is opened, plus
// whether the destination was reached via a reference/back-reference jump (for
// the breadcrumb's ↦ marker) and whether the target still needs resolving.
func (e entry) identity() (id model.Identity, viaRef bool, unresolved bool) {
	switch e.kind {
	case entLB:
		return model.Identity{Type: model.TypeLoadBalancer, ID: e.lb.ID, OwningLBID: e.lb.ID, Label: lbLabel(e.lb)}, false, false
	case entChild:
		return e.node.Identity(), false, false
	case entRef, entBackRef:
		if e.edge.Target != nil {
			return e.edge.Target.Identity(), true, false
		}
		return model.Identity{}, true, e.edge.Unresolved
	}
	return model.Identity{}, false, false
}

// lbEntries builds the top-level load balancer list rows. When showProject is
// set (all-projects mode) each row is prefixed with its owning project so the
// aggregated list stays legible.
func lbEntries(lbs []osclient.LB, showProject bool) []entry {
	es := make([]entry, 0, len(lbs))
	for _, lb := range lbs {
		extra := lb.Provider
		if lb.VipAddress != "" {
			extra = strings.TrimSpace(extra + " " + lb.VipAddress)
		}
		if showProject {
			proj := lb.ProjectName
			if proj == "" {
				proj = shortID(lb.ProjectID)
			}
			extra = strings.TrimSpace("@" + proj + "  " + extra)
		}
		es = append(es, entry{
			kind: entLB, lb: lb, label: lbLabel(lb),
			oper: lb.OperatingStatus, prov: lb.ProvisioningStatus, extra: extra,
		})
	}
	return es
}

// nodeEntries builds the rows for a node's location: its containment children,
// then its outgoing reference edges, then the back-references answering
// "who points at me?".
func nodeEntries(n *model.Node) []entry {
	var es []entry
	for _, c := range n.Children {
		es = append(es, entry{
			kind: entChild, node: c, label: c.Label(),
			oper: c.OperatingStatus, prov: c.ProvisioningStatus, extra: inlineAttrs(c),
		})
	}
	for _, ref := range n.Refs {
		es = append(es, refEntry(entRef, ref))
	}
	for _, br := range n.BackRefs {
		es = append(es, refEntry(entBackRef, br))
	}
	return es
}

func refEntry(kind entryKind, edge *model.Edge) entry {
	e := entry{kind: kind, edge: edge, relationship: edge.Label}
	switch {
	case edge.Target != nil:
		e.node = edge.Target
		e.label = edge.Target.Label()
		e.oper = edge.Target.OperatingStatus
		e.prov = edge.Target.ProvisioningStatus
	case edge.Unresolved:
		e.label = string(edge.TargetType) // e.g. "floatingip", "instance"
	case edge.Missing:
		e.label = string(edge.TargetType) + " (unavailable)"
	default:
		e.label = model.NodeType(edge.TargetType).Short() + ":" + shortID(edge.TargetID)
	}
	return e
}

func lbLabel(lb osclient.LB) string {
	if lb.Name != "" {
		return "lb:" + lb.Name
	}
	return "lb:" + shortID(lb.ID)
}

// inlineAttrs renders a node's most useful facts for the trailing column.
func inlineAttrs(n *model.Node) string {
	switch n.Type {
	case model.TypeListener:
		return joinAttrs(n, "protocol", "port")
	case model.TypePool:
		return joinAttrs(n, "lb_algorithm")
	case model.TypeMember:
		return joinAttrs(n, "address", "port")
	case model.TypeHealthMonitor:
		return joinAttrs(n, "type")
	case model.TypeL7Policy:
		return joinAttrs(n, "action")
	case model.TypeL7Rule:
		return joinAttrs(n, "type")
	case model.TypeVIP:
		return joinAttrs(n, "address")
	default:
		return ""
	}
}

func joinAttrs(n *model.Node, keys ...string) string {
	var parts []string
	for _, k := range keys {
		if v := n.Attrs[k]; v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// statusFilter cycles all → error → degraded.
type statusFilter int

const (
	statusAll statusFilter = iota
	statusError
	statusDegraded
)

func (f statusFilter) String() string {
	switch f {
	case statusError:
		return "error"
	case statusDegraded:
		return "degraded"
	default:
		return "all"
	}
}

func (f statusFilter) next() statusFilter { return (f + 1) % 3 }

func (f statusFilter) match(oper, prov string) bool {
	switch f {
	case statusError:
		return oper == "ERROR" || prov == "ERROR"
	case statusDegraded:
		return oper == "DEGRADED" || oper == "ERROR" || prov == "ERROR"
	default:
		return true
	}
}
