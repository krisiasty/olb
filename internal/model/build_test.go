package model

import (
	"encoding/json"
	"testing"
)

// canonicalStatus is the Octavia api-ref sample status-show response: two
// listeners (one with a pool + health monitor + members, one with an L7
// policy), plus a top-level pools array that includes a shared pool and an
// orphan pool (source_ip_pool, attached to no listener).
const canonicalStatus = `
{
  "statuses": {
    "loadbalancer": {
      "name": "excellent_load_balancer",
      "id": "84faceee-cb97-48d0-93df-9e41d40d4cb4",
      "provisioning_status": "ACTIVE",
      "operating_status": "DEGRADED",
      "listeners": [
        {
          "name": "HTTP_listener",
          "id": "78febaf6-1e63-47c6-af5f-7b5e23fd7094",
          "provisioning_status": "ACTIVE",
          "operating_status": "DEGRADED",
          "pools": [
            {
              "name": "HTTP_pool",
              "id": "89a47f78-cf81-480b-ad74-bba4177eeb81",
              "provisioning_status": "ACTIVE",
              "operating_status": "DEGRADED",
              "healthmonitor": {"type": "HTTP", "id": "0b608787-ea2d-48c7-89a1-8b8c24fa3b17", "name": "HTTP_hm", "provisioning_status": "ACTIVE"},
              "members": [
                {"name": "", "address": "192.0.2.20", "protocol_port": 80, "id": "3c6857f4-057a-405a-9134-bdeaa8796c8a", "operating_status": "ERROR", "provisioning_status": "ACTIVE"},
                {"name": "", "address": "192.0.2.21", "protocol_port": 80, "id": "f7495909-1706-4c91-83b4-641dab6962ac", "operating_status": "ONLINE", "provisioning_status": "ACTIVE"}
              ]
            }
          ],
          "l7policies": []
        },
        {
          "name": "redirect_listener",
          "id": "1341fbaf-ad4f-4cfe-a943-ad5e14e664cb",
          "provisioning_status": "ACTIVE",
          "operating_status": "ONLINE",
          "pools": [],
          "l7policies": [
            {"action": "REDIRECT_TO_URL", "id": "2e8f3139-0673-43f9-aae4-c7a9460e3233", "name": "redirect_policy", "provisioning_status": "ACTIVE",
             "rules": [{"type": "PATH", "id": "27f3007a-a1cb-4e17-9696-0e578d617715", "provisioning_status": "ACTIVE"}]}
          ]
        }
      ],
      "pools": [
        {
          "name": "HTTP_pool",
          "id": "89a47f78-cf81-480b-ad74-bba4177eeb81",
          "provisioning_status": "ACTIVE",
          "operating_status": "DEGRADED",
          "healthmonitor": {"type": "HTTP", "id": "0b608787-ea2d-48c7-89a1-8b8c24fa3b17", "name": "HTTP_hm", "provisioning_status": "ACTIVE"},
          "members": [
            {"name": "", "address": "192.0.2.20", "protocol_port": 80, "id": "3c6857f4-057a-405a-9134-bdeaa8796c8a", "operating_status": "ERROR", "provisioning_status": "ACTIVE"},
            {"name": "", "address": "192.0.2.21", "protocol_port": 80, "id": "f7495909-1706-4c91-83b4-641dab6962ac", "operating_status": "ONLINE", "provisioning_status": "ACTIVE"}
          ]
        },
        {
          "name": "source_ip_pool",
          "id": "8189d6a9-646e-4d23-b742-548dab991951",
          "provisioning_status": "ACTIVE",
          "operating_status": "ONLINE",
          "healthmonitor": {},
          "members": []
        }
      ]
    }
  }
}`

func buildCanonical(t *testing.T, provider string) *Tree {
	t.Helper()
	var w struct {
		Statuses StatusTree `json:"statuses"`
	}
	if err := json.Unmarshal([]byte(canonicalStatus), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return Build(&w.Statuses, LBMeta{VipAddress: "203.0.113.50", VipPortID: "port-1", Provider: provider})
}

func TestBuildGraphShape(t *testing.T) {
	tr := buildCanonical(t, "amphora")
	root := tr.Root

	if root.Type != TypeLoadBalancer || root.Name != "excellent_load_balancer" {
		t.Fatalf("bad root: %+v", root)
	}

	// LB children: VIP, two canonical pools, two listeners, amphorae placeholder.
	counts := map[NodeType]int{}
	for _, c := range root.Children {
		counts[c.Type]++
	}
	if counts[TypeVIP] != 1 {
		t.Errorf("want 1 VIP child, got %d", counts[TypeVIP])
	}
	if counts[TypePool] != 2 {
		t.Errorf("want 2 pool children (incl. orphan source_ip_pool), got %d", counts[TypePool])
	}
	if counts[TypeListener] != 2 {
		t.Errorf("want 2 listener children, got %d", counts[TypeListener])
	}
	if counts[TypeAmphora] != 1 {
		t.Errorf("want amphorae placeholder for amphora provider, got %d", counts[TypeAmphora])
	}

	// The HTTP pool has a health monitor child and two member children.
	pool := tr.Node("89a47f78-cf81-480b-ad74-bba4177eeb81")
	if pool == nil {
		t.Fatal("HTTP_pool not indexed")
	}
	var hm, members int
	for _, c := range pool.Children {
		switch c.Type {
		case TypeHealthMonitor:
			hm++
		case TypeMember:
			members++
		}
	}
	if hm != 1 || members != 2 {
		t.Errorf("HTTP_pool: want 1 monitor + 2 members, got %d + %d", hm, members)
	}

	// The orphan pool's empty {} healthmonitor must not become a child.
	orphan := tr.Node("8189d6a9-646e-4d23-b742-548dab991951")
	if orphan == nil {
		t.Fatal("source_ip_pool not indexed")
	}
	for _, c := range orphan.Children {
		if c.Type == TypeHealthMonitor {
			t.Errorf("orphan pool should have no health monitor (empty {}), got %v", c)
		}
	}
}

func TestBackReference(t *testing.T) {
	tr := buildCanonical(t, "amphora")
	// The HTTP listener is nested over HTTP_pool, so the pool should have a
	// back-reference answering "who points at me?".
	pool := tr.Node("89a47f78-cf81-480b-ad74-bba4177eeb81")
	listener := tr.Node("78febaf6-1e63-47c6-af5f-7b5e23fd7094")
	if pool == nil || listener == nil {
		t.Fatal("missing nodes")
	}
	var found bool
	for _, br := range pool.BackRefs {
		if br.Kind == BackReference && br.Target == listener {
			found = true
		}
	}
	if !found {
		t.Errorf("HTTP_pool should back-reference HTTP_listener; backrefs=%d", len(pool.BackRefs))
	}
	// And the forward edge exists on the listener.
	var fwd bool
	for _, r := range listener.Refs {
		if r.Kind == Reference && r.Target == pool {
			fwd = true
		}
	}
	if !fwd {
		t.Errorf("HTTP_listener should reference HTTP_pool")
	}
}

func TestReferenceResolution(t *testing.T) {
	tr := buildCanonical(t, "amphora")
	listener := tr.Node("78febaf6-1e63-47c6-af5f-7b5e23fd7094")
	// Simulate a lazy listener show resolving the default pool.
	tr.ResolveListenerDefaultPool(listener.ID, "89a47f78-cf81-480b-ad74-bba4177eeb81")
	var labelled bool
	for _, r := range listener.Refs {
		if r.TargetID == "89a47f78-cf81-480b-ad74-bba4177eeb81" && r.Label == "default pool" {
			labelled = true
		}
	}
	if !labelled {
		t.Errorf("default-pool edge not upgraded after resolution")
	}
}

func TestOVNHasNoAmphora(t *testing.T) {
	tr := buildCanonical(t, "ovn")
	for _, c := range tr.Root.Children {
		if c.Type == TypeAmphora {
			t.Errorf("OVN-backed LB must not have an amphora branch")
		}
	}
}

func TestUnresolvedEdges(t *testing.T) {
	tr := buildCanonical(t, "amphora")
	// VIP carries an unresolved floating-IP edge; members carry an unresolved
	// instance edge.
	vip := tr.Node("port-1")
	if vip == nil || !vip.HasUnresolvedRef("floating IP") {
		t.Errorf("VIP should have an unresolved floating-IP edge")
	}
	member := tr.Node("3c6857f4-057a-405a-9134-bdeaa8796c8a")
	if member == nil || !member.HasUnresolvedRef("instance") {
		t.Errorf("member should have an unresolved instance edge")
	}
}
