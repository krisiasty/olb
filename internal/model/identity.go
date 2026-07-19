package model

// Identity is the lightweight, durable handle stored in navigation history.
//
// History entries are identities, not pointers or snapshots: jumps can cross
// into other LB hierarchies, and other users/processes can create, change, or
// delete objects between visits. An identity is therefore re-resolved against
// live (or cached) state on every revisit, rather than trusting a stale node
// pointer. It carries only what is needed to re-locate the object plus a cached
// label for the picker overlay.
type Identity struct {
	Type       NodeType
	ID         string
	OwningLBID string
	// Label is cached purely for the history picker so it can render without a
	// re-resolution round trip. It may be stale; the resolved node wins on land.
	Label string
}

// Identity returns the durable identity of a node.
func (n *Node) Identity() Identity {
	lb := n.OwningLBID
	if n.Type == TypeLoadBalancer && lb == "" {
		lb = n.ID
	}
	return Identity{Type: n.Type, ID: n.ID, OwningLBID: lb, Label: n.Label()}
}

// Equal reports whether two identities refer to the same object.
func (i Identity) Equal(o Identity) bool {
	return i.Type == o.Type && i.ID == o.ID && i.OwningLBID == o.OwningLBID
}

// IsLBList reports whether this identity is the synthetic "load balancer list"
// root (empty ID and type). The list root is represented by the zero Identity.
func (i Identity) IsLBList() bool {
	return i.ID == "" && i.Type == ""
}

// IsResourceList reports whether this identity is a synthetic top-level list of a
// resource type (a type with no concrete object ID), e.g. the listeners list.
// The LB list is the zero identity and is reported by IsLBList instead.
func (i Identity) IsResourceList() bool {
	return i.ID == "" && i.OwningLBID == "" && i.Type != ""
}

// IsTopLevelList reports whether this identity is any top-level list root — the
// LB list or a resource list. These are the breadcrumb/history boundaries.
func (i Identity) IsTopLevelList() bool {
	return i.IsLBList() || i.IsResourceList()
}

// Sentinel identities for the top-level list views. Each carries only a type so
// it round-trips through navigation history like the LB list, which is the zero
// identity below.
var (
	LBListIdentity       = Identity{}
	VIPListIdentity      = Identity{Type: TypeVIP}
	ListenerListIdentity = Identity{Type: TypeListener}
	PoolListIdentity     = Identity{Type: TypePool}
	AmphoraListIdentity  = Identity{Type: TypeAmphora}
)
