package osclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/loadbalancers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/floatingips"
)

func TestListLoadBalancersSendsSelectedProjectFilterToOctavia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("project_id"); got != "project-b" {
			t.Errorf("project_id query = %q, want project-b", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"loadbalancers":[{"id":"lb-b","name":"lb-b","project_id":"project-b"}],"loadbalancers_links":[]}`))
	}))
	defer server.Close()

	sc := &serviceClients{lb: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2/lbaas/",
	}}
	c := &Clients{
		services:       sc,
		activeServices: sc,
		selected:       ProjectInfo{ID: "project-b", Name: "beta"},
		globalAdmin:    true,
	}
	got, err := c.ListLoadBalancers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProjectID != "project-b" || got[0].ProjectName != "beta" {
		t.Fatalf("load balancers = %+v", got)
	}
}

func TestFormatAPITimeUsesUTCAndOmitsZeroValue(t *testing.T) {
	if got := formatAPITime(time.Time{}); got != "" {
		t.Fatalf("zero API time = %q, want empty", got)
	}

	value := time.Date(2026, time.July, 19, 13, 20, 45, 987_000_000, time.FixedZone("CEST", 2*60*60))
	if got, want := formatAPITime(value), "2026-07-19T11:20:45Z"; got != want {
		t.Fatalf("formatted API time = %q, want %q", got, want)
	}
}

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
