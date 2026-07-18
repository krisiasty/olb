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

// LBListIdentity is the sentinel identity for the top-level load balancer list.
var LBListIdentity = Identity{}
