package osclient

import (
	"testing"

	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/loadbalancers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/floatingips"
)

func TestFilterLoadBalancersCanReturnToOriginalAllProjectsList(t *testing.T) {
	all := []LB{
		{ID: "lb-a1", ProjectID: "a"},
		{ID: "lb-b1", ProjectID: "b"},
		{ID: "lb-c1", ProjectID: "c"},
	}

	filtered := filterLoadBalancers(all, ProjectInfo{ID: "b", Name: "project-b"})
	if len(filtered) != 1 || filtered[0].ID != "lb-b1" {
		t.Fatalf("project filter returned %+v", filtered)
	}
	if filtered[0].ProjectName != "project-b" {
		t.Fatalf("filtered row project name = %q", filtered[0].ProjectName)
	}
	if len(all) != 3 {
		t.Fatalf("project filtering mutated the original all-projects list: %+v", all)
	}

	restored := filterLoadBalancers(all, ProjectInfo{})
	if len(restored) != len(all) {
		t.Fatalf("clearing the filter returned %d rows, want %d", len(restored), len(all))
	}
}

func TestAdditionalVIPMetaPreservesAddressAndSubnet(t *testing.T) {
	got := additionalVIPMeta([]loadbalancers.AdditionalVip{
		{IPAddress: "10.0.0.20", SubnetID: "subnet-20"},
		{IPAddress: "10.0.0.30", SubnetID: "subnet-30"},
	})
	if len(got) != 2 || got[0].Address != "10.0.0.20" || got[0].SubnetID != "subnet-20" ||
		got[1].Address != "10.0.0.30" || got[1].SubnetID != "subnet-30" {
		t.Fatalf("additional VIP conversion = %+v", got)
	}
}

func TestFloatingIPNodesAreKeyedByFixedAddress(t *testing.T) {
	nodes := floatingIPNodes([]floatingips.FloatingIP{
		{ID: "fip-primary", FixedIP: "10.0.0.10", FloatingIP: "198.51.100.10", PortID: "vip-port", Status: "ACTIVE"},
		{ID: "fip-additional", FixedIP: "10.0.0.20", FloatingIP: "198.51.100.20", PortID: "vip-port", Status: "ACTIVE"},
	})
	if len(nodes) != 2 {
		t.Fatalf("got %d floating-IP nodes, want 2", len(nodes))
	}
	for fixed, floating := range map[string]string{
		"10.0.0.10": "198.51.100.10",
		"10.0.0.20": "198.51.100.20",
	} {
		node := nodes[fixed]
		if node == nil || node.Attrs["floating_ip"] != floating || node.Attrs["fixed_ip"] != fixed {
			t.Errorf("mapping for %s = %+v, want floating address %s", fixed, node, floating)
			continue
		}
		raw, ok := node.Raw.(map[string]any)
		if !ok || raw["fixed_ip_address"] != fixed {
			t.Errorf("raw floating IP for %s does not retain fixed address: %#v", fixed, node.Raw)
		}
	}
}
