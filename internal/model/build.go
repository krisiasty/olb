package model

import "fmt"

// Tree is a fully built load-balancer graph plus an index for O(1) lookup by
// object ID, used for reference resolution and history re-resolution.
type Tree struct {
	Root  *Node
	Meta  LBMeta
	index map[string]*Node
}

// Node returns the node with the given ID within this tree, or nil.
func (t *Tree) Node(id string) *Node {
	if t == nil {
		return nil
	}
	return t.index[id]
}

// register indexes n (and is safe to call repeatedly).
func (t *Tree) register(n *Node) {
	if n.ID != "" {
		t.index[n.ID] = n
	}
}

// Build assembles the in-memory graph from a status show response plus the few
// LB-level facts that live outside it (VIP, provider). Only containment and the
// reference edges derivable from the status-tree nesting are wired here — no
// extra API calls. Precise reference edges (a listener's default pool, an L7
// policy's redirect pool) and cross-service edges (floating IP, Nova instance)
// are attached lazily on landing; see ResolveListenerRefs / ResolveL7PolicyRef.
func Build(st *StatusTree, meta LBMeta) *Tree {
	lb := st.LoadBalancer
	t := &Tree{Meta: meta, index: map[string]*Node{}}

	root := NewNode(TypeLoadBalancer, lb.ID, lb.Name)
	root.OwningLBID = lb.ID
	root.ProvisioningStatus = lb.ProvisioningStatus
	root.OperatingStatus = lb.OperatingStatus
	root.SetAttr("provider", meta.Provider)
	root.Raw = st
	t.Root = root
	t.register(root)

	// VIPs are properties of the LB, not part of the status tree. Model them as
	// synthetic children so each fixed address can carry its own floating-IP
	// boundary edge. Additional VIPs share the primary VIP's Neutron port.
	if meta.VipAddress != "" {
		vip := NewNode(TypeVIP, meta.VipPortID, meta.VipAddress)
		vip.OwningLBID = lb.ID
		vip.SetAttr("address", meta.VipAddress)
		vip.SetAttr("port_id", meta.VipPortID)
		vip.SetAttr("vip_kind", "primary")
		vip.Raw = map[string]any{
			"vip_address": meta.VipAddress, "vip_port_id": meta.VipPortID,
			"vip_kind": "primary",
		}
		vip.DetailLoaded = true // the VIP has no separate show; its facts are inline
		// The floating IP is a Neutron lookup against the VIP port; often absent
		// (internal LBs). Rendered as a jump entry, resolved on landing.
		vip.AddUnresolvedRef("floating IP", TypeFloatingIP, meta.VipPortID)
		root.addChild(vip)
		if meta.VipPortID != "" {
			t.register(vip)
		}
	}
	for _, extra := range meta.AdditionalVIPs {
		if extra.Address == "" {
			continue
		}
		vip := NewNode(TypeVIP, additionalVIPID(lb.ID, extra), extra.Address)
		vip.OwningLBID = lb.ID
		vip.SetAttr("address", extra.Address)
		vip.SetAttr("port_id", meta.VipPortID)
		vip.SetAttr("subnet_id", extra.SubnetID)
		vip.SetAttr("vip_kind", "additional")
		vip.Raw = map[string]any{
			"ip_address": extra.Address, "subnet_id": extra.SubnetID,
			"vip_port_id": meta.VipPortID, "vip_kind": "additional",
		}
		vip.DetailLoaded = true
		vip.AddUnresolvedRef("floating IP", TypeFloatingIP, meta.VipPortID)
		root.addChild(vip)
		t.register(vip)
	}

	// Canonical, de-duplicated pool nodes come from the LB-level pools array,
	// which includes shared pools and pools attached to no listener.
	for i := range lb.Pools {
		p := buildPool(t, &lb.Pools[i], lb.ID)
		root.addChild(p)
	}

	for i := range lb.Listeners {
		sl := &lb.Listeners[i]
		ln := NewNode(TypeListener, sl.ID, sl.Name)
		ln.OwningLBID = lb.ID
		ln.ProvisioningStatus = sl.ProvisioningStatus
		ln.OperatingStatus = sl.OperatingStatus
		ln.Raw = sl
		root.addChild(ln)
		t.register(ln)

		// L7 policies are contained by the listener; their rules by the policy.
		for j := range sl.L7Policies {
			sp := &sl.L7Policies[j]
			pol := NewNode(TypeL7Policy, sp.ID, sp.Name)
			pol.OwningLBID = lb.ID
			pol.ProvisioningStatus = sp.ProvisioningStatus
			pol.OperatingStatus = sp.OperatingStatus
			pol.SetAttr("action", sp.Action)
			pol.Raw = sp
			ln.addChild(pol)
			t.register(pol)

			for k := range sp.Rules {
				sr := &sp.Rules[k]
				r := NewNode(TypeL7Rule, sr.ID, ruleName(sr))
				r.OwningLBID = lb.ID
				r.ProvisioningStatus = sr.ProvisioningStatus
				r.OperatingStatus = sr.OperatingStatus
				r.SetAttr("type", sr.Type)
				r.Raw = sr
				pol.addChild(r)
				t.register(r)
			}
		}

		// The pools nested under a listener are exactly the pools that listener
		// is associated with (its default pool plus any redirect targets of its
		// policies). We can record the association now; which one is the default
		// is refined by a lazy listener show.
		for j := range sl.Pools {
			sp := &sl.Pools[j]
			pn := t.Node(sp.ID)
			if pn == nil {
				// Some Octavia deployments expose pools only beneath listeners,
				// without repeating them in loadbalancer.pools. Promote the first
				// occurrence to a canonical root child so it remains discoverable.
				pn = buildPool(t, sp, lb.ID)
				root.addChild(pn)
			}
			if pn != nil {
				ln.AddRef("pool", pn)
			}
		}
	}

	return t
}

func additionalVIPID(lbID string, vip AdditionalVIP) string {
	key := vip.SubnetID
	if key == "" {
		key = vip.Address
	}
	return lbID + "/additional-vip/" + key
}

func buildPool(t *Tree, sp *StatusPool, lbID string) *Node {
	// A shared pool can appear both at LB level and nested under listeners; keep
	// a single canonical node.
	if existing := t.Node(sp.ID); existing != nil {
		return existing
	}
	p := NewNode(TypePool, sp.ID, sp.Name)
	p.OwningLBID = lbID
	p.ProvisioningStatus = sp.ProvisioningStatus
	p.OperatingStatus = sp.OperatingStatus
	p.SetAttr("member_count", fmt.Sprintf("%d", len(sp.Members)))
	p.Raw = sp
	t.register(p)

	if hm := sp.monitor(); hm != nil {
		m := NewNode(TypeHealthMonitor, hm.ID, hm.Name)
		m.OwningLBID = lbID
		m.ProvisioningStatus = hm.ProvisioningStatus
		m.OperatingStatus = hm.OperatingStatus
		m.SetAttr("type", hm.Type)
		m.Raw = hm
		p.addChild(m)
		t.register(m)
	}

	for i := range sp.Members {
		sm := &sp.Members[i]
		mem := NewNode(TypeMember, sm.ID, memberName(sm))
		mem.OwningLBID = lbID
		mem.ProvisioningStatus = sm.ProvisioningStatus
		mem.OperatingStatus = sm.OperatingStatus
		mem.SetAttr("address", sm.Address)
		if sm.ProtocolPort != 0 {
			mem.SetAttr("port", fmt.Sprintf("%d", sm.ProtocolPort))
		}
		mem.Raw = sm
		// A member address usually corresponds to a Nova instance; resolved on
		// landing via a compute lookup by fixed IP.
		if sm.Address != "" {
			mem.AddUnresolvedRef("instance", TypeInstance, sm.Address)
		}
		p.addChild(mem)
		t.register(mem)
	}
	return p
}

func ruleName(r *StatusL7Rule) string {
	if r.Type != "" {
		return r.Type
	}
	return shortID(r.ID)
}

func memberName(m *StatusMember) string {
	if m.Name != "" {
		return m.Name
	}
	if m.Address != "" {
		if m.ProtocolPort != 0 {
			return fmt.Sprintf("%s:%d", m.Address, m.ProtocolPort)
		}
		return m.Address
	}
	return shortID(m.ID)
}
