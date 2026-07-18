package model

// Reference resolution refines the coarse edges wired at build time using
// per-object detail loaded lazily on landing. It is idempotent so it can run
// again after a detail refetch.

// ResolveListenerDefaultPool upgrades a listener's generic pool association to a
// precise "default pool" edge once the listener's default_pool_id is known.
func (t *Tree) ResolveListenerDefaultPool(listenerID, defaultPoolID string) {
	ln := t.Node(listenerID)
	if ln == nil {
		return
	}
	ln.RefsResolved = true
	if defaultPoolID == "" {
		return
	}
	pool := t.Node(defaultPoolID)
	if pool == nil {
		return
	}
	for _, e := range ln.Refs {
		if e.Kind == Reference && e.TargetID == defaultPoolID {
			e.Label = "default pool"
			for _, b := range pool.BackRefs {
				if b.Kind == BackReference && b.TargetID == ln.ID {
					b.Label = "default pool"
				}
			}
			return
		}
	}
	ln.AddRef("default pool", pool)
}

// ResolveL7PolicyRedirect wires the reference edge from an L7 policy to its
// redirect pool. The edge exists only when the action is REDIRECT_TO_POOL;
// REDIRECT_TO_URL, REDIRECT_PREFIX, and REJECT have no target.
func (t *Tree) ResolveL7PolicyRedirect(policyID, action, redirectPoolID string) {
	pol := t.Node(policyID)
	if pol == nil {
		return
	}
	pol.RefsResolved = true
	if action != "REDIRECT_TO_POOL" || redirectPoolID == "" {
		return
	}
	if pool := t.Node(redirectPoolID); pool != nil {
		pol.AddRef("redirect pool", pool)
	}
}

// Attach registers an externally-resolved node (a floating IP or Nova instance
// that lives outside the status tree) so history can re-resolve it later.
func (t *Tree) Attach(n *Node) {
	if n != nil {
		t.register(n)
	}
}

// ResolveEdge fills in a previously-unresolved reference edge. A nil target
// marks the edge missing (the boundary object does not exist, e.g. an internal
// LB with no floating IP); a non-nil target wires the inverse back-reference.
func (n *Node) ResolveEdge(label string, target *Node) {
	for _, e := range n.Refs {
		if e.Unresolved && e.Label == label {
			e.Unresolved = false
			if target == nil {
				e.Missing = true
				return
			}
			e.Target = target
			e.TargetID = target.ID
			e.TargetType = target.Type
			target.BackRefs = append(target.BackRefs, &Edge{
				Kind: BackReference, Label: label, Target: n,
				TargetType: n.Type, TargetID: n.ID,
			})
			return
		}
	}
}

// HasUnresolvedRef reports whether the node has an unresolved edge with the
// given label still pending a lazy lookup.
func (n *Node) HasUnresolvedRef(label string) bool {
	for _, e := range n.Refs {
		if e.Unresolved && e.Label == label {
			return true
		}
	}
	return false
}
