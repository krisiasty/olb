// Package model defines the in-memory load-balancer graph: typed nodes joined
// by containment and reference edges, both traversable in either direction.
//
// The object graph is deliberately not a tree. Containment (LB -> listener ->
// pool -> member) is the backbone, but pools are shared (the default pool of
// several listeners, or the redirect target of several L7 policies) and some
// edges cross out of Octavia entirely (a VIP -> Neutron port -> floating IP, a
// member -> Nova instance). Every edge is therefore explicit and reversible so
// the most valuable debugging query — "who points at this pool?" — is
// answerable, which a tree can never do.
package model

// NodeType enumerates the kinds of objects in the graph.
type NodeType string

const (
	TypeLoadBalancer  NodeType = "loadbalancer"
	TypeVIP           NodeType = "vip"
	TypeFloatingIP    NodeType = "floatingip"
	TypeListener      NodeType = "listener"
	TypePool          NodeType = "pool"
	TypeMember        NodeType = "member"
	TypeHealthMonitor NodeType = "healthmonitor"
	TypeL7Policy      NodeType = "l7policy"
	TypeL7Rule        NodeType = "l7rule"
	TypeAmphora       NodeType = "amphora"
	TypeInstance      NodeType = "instance" // Nova server backing a member
	TypeCOECluster    NodeType = "coecluster"
	TypeKubeService   NodeType = "kubernetesservice"
)

// Short returns a compact type label used in breadcrumbs and list entries.
func (t NodeType) Short() string {
	switch t {
	case TypeLoadBalancer:
		return "lb"
	case TypeHealthMonitor:
		return "monitor"
	case TypeFloatingIP:
		return "fip"
	case TypeCOECluster:
		return "cluster"
	case TypeKubeService:
		return "service"
	default:
		return string(t)
	}
}

// EdgeKind distinguishes containment (backbone) from reference (cross-cutting)
// links. Back-references are the inverse of reference edges, computed so a node
// can answer "who points at me?".
type EdgeKind int

const (
	// Containment is a parent->child backbone edge.
	Containment EdgeKind = iota
	// Reference is a forward pointer to another node (may be shared / cross-LB).
	Reference
	// BackReference is the inverse of a Reference edge ("who points at me").
	BackReference
)

// Edge is a typed, directional link to another node. For reference edges the
// target may be unresolved (only an identity is known) or missing (the target
// object could not be located in the current tree, e.g. it was deleted).
type Edge struct {
	Kind EdgeKind
	// Label describes the relationship, e.g. "default pool", "redirect pool",
	// "instance", "floating IP". Empty for plain containment.
	Label string
	// Target is the resolved node, or nil when Unresolved/Missing.
	Target *Node
	// TargetType/TargetID identify the target even when Target is nil.
	TargetType NodeType
	TargetID   string
	// Unresolved marks a reference whose target has not been fetched yet.
	Unresolved bool
	// Missing marks a reference whose target could not be found.
	Missing bool
}

// Node is a single object in the graph.
type Node struct {
	Type NodeType
	ID   string
	Name string

	ProvisioningStatus string
	OperatingStatus    string

	// OwningLBID is the load balancer whose status tree this node belongs to.
	// It is part of the history identity so a node can be re-resolved later.
	OwningLBID string

	// Attrs holds small display facts shown inline (protocol:port, address,
	// algorithm, monitor type, ...). Populated from the status tree and enriched
	// by lazy detail loads.
	Attrs map[string]string

	// Children are containment edges, in display order.
	Children []*Node
	// Parent is the containment parent (nil for the LB root and for the LB list).
	Parent *Node

	// Refs are outgoing reference edges (this node points at others).
	Refs []*Edge
	// BackRefs are incoming reference edges (others point at this node).
	BackRefs []*Edge

	// Raw is the raw API object for the y/j/detail views. Initially the reduced
	// status-tree object; replaced by the full object once a lazy show completes.
	Raw any
	// DetailLoaded reports whether a per-object show has enriched this node.
	DetailLoaded bool
	// RefsResolved reports whether lazy reference resolution has run for this node.
	RefsResolved bool
}

// NewNode constructs a node with an initialized attribute map.
func NewNode(t NodeType, id, name string) *Node {
	return &Node{Type: t, ID: id, Name: name, Attrs: map[string]string{}}
}

// Label is the human label shown in lists and breadcrumbs, e.g. "listener:https".
func (n *Node) Label() string {
	name := n.Name
	if name == "" {
		name = shortID(n.ID)
	}
	return n.Type.Short() + ":" + name
}

// SetAttr records a display attribute if the value is non-empty.
func (n *Node) SetAttr(k, v string) {
	if v == "" {
		return
	}
	if n.Attrs == nil {
		n.Attrs = map[string]string{}
	}
	n.Attrs[k] = v
}

// addChild appends c as a containment child and sets its parent pointer.
func (n *Node) addChild(c *Node) {
	c.Parent = n
	n.Children = append(n.Children, c)
}

// AddRef records a forward reference edge from n to target and the inverse
// back-reference on target. Duplicate edges (same kind+label+target id) are
// ignored so repeated resolution passes are idempotent.
func (n *Node) AddRef(label string, target *Node) {
	if target == nil {
		return
	}
	for _, e := range n.Refs {
		if e.Kind == Reference && e.Label == label && e.TargetID == target.ID {
			return
		}
	}
	n.Refs = append(n.Refs, &Edge{
		Kind: Reference, Label: label, Target: target,
		TargetType: target.Type, TargetID: target.ID,
	})
	target.BackRefs = append(target.BackRefs, &Edge{
		Kind: BackReference, Label: label, Target: n,
		TargetType: n.Type, TargetID: n.ID,
	})
}

// AddUnresolvedRef records a reference edge whose target is not (yet) a node in
// this tree — e.g. a member's Nova instance or a VIP's floating IP that has not
// been looked up. Rendered as a jump entry; resolved lazily on landing.
func (n *Node) AddUnresolvedRef(label string, targetType NodeType, targetID string) {
	for _, e := range n.Refs {
		if e.Kind == Reference && e.Label == label && e.TargetID == targetID && targetID != "" {
			return
		}
	}
	n.Refs = append(n.Refs, &Edge{
		Kind: Reference, Label: label, TargetType: targetType,
		TargetID: targetID, Unresolved: true,
	})
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
