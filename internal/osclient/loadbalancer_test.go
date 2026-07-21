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
	"github.com/krisiasty/olb/internal/model"
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

func TestFetchPoolDetailReturnsOverviewAttributes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/lbaas/pools/pool-1" {
			t.Errorf("request path = %q, want pool detail endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pool":{
			"id":"pool-1","name":"web","project_id":"project-1",
			"description":"Web backends","protocol":"HTTP","lb_algorithm":"ROUND_ROBIN",
			"admin_state_up":true,"members":[{"id":"member-1"}],
			"listeners":[{"id":"listener-1"}],"healthmonitor_id":"monitor-1",
			"subnet_id":"subnet-1","session_persistence":{"type":"APP_COOKIE","cookie_name":"SESSION"},
			"tls_enabled":true,"tls_versions":["TLSv1.2","TLSv1.3"],
			"alpn_protocols":["h2","http/1.1"],"tls_ciphers":"cipher-list",
			"tags":["blue","api"],"created_at":"2026-07-18T10:15:30Z",
			"updated_at":"2026-07-19T11:20:45Z"
		}}`))
	}))
	defer server.Close()

	sc := &serviceClients{lb: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2/",
	}}
	c := &Clients{services: sc, activeServices: sc}
	node := model.NewNode(model.TypePool, "pool-1", "web")
	node.OwningLBID = "lb-1"
	result, err := c.FetchDetail(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"project_id": "project-1", "description": "Web backends",
		"protocol": "HTTP", "lb_algorithm": "ROUND_ROBIN", "admin_state_up": "true",
		"member_count": "1", "listener_count": "1", "healthmonitor_id": "monitor-1",
		"subnet_id": "subnet-1", "session_persistence": "APP_COOKIE",
		"persistence_cookie": "SESSION", "tls_enabled": "true",
		"tls_versions": "TLSv1.2, TLSv1.3", "alpn_protocols": "h2, http/1.1",
		"tls_ciphers": "cipher-list", "tags": "blue, api",
		"created_at": "2026-07-18T10:15:30Z", "updated_at": "2026-07-19T11:20:45Z",
	}
	for key, value := range want {
		if result.Attrs[key] != value {
			t.Errorf("pool attribute %s = %q, want %q", key, result.Attrs[key], value)
		}
	}
}

func TestFetchMemberDetailReturnsOverviewAttributes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/lbaas/pools/pool-1/members/member-1" {
			t.Errorf("request path = %q, want member detail endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"member":{
			"id":"member-1","name":"api-1","project_id":"project-1",
			"subnet_id":"subnet-1","address":"10.0.0.5","protocol_port":8080,
			"weight":10,"backup":true,"admin_state_up":true,
			"monitor_address":"10.0.1.5","monitor_port":8081,"tags":["api","blue"],
			"created_at":"2026-07-18T10:15:30","updated_at":"2026-07-19T11:20:45"
		}}`))
	}))
	defer server.Close()

	sc := &serviceClients{lb: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2/",
	}}
	c := &Clients{services: sc, activeServices: sc}
	pool := model.NewNode(model.TypePool, "pool-1", "web")
	node := model.NewNode(model.TypeMember, "member-1", "api-1")
	node.OwningLBID = "lb-1"
	node.Parent = pool
	result, err := c.FetchDetail(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"name": "api-1", "project_id": "project-1", "subnet_id": "subnet-1",
		"address": "10.0.0.5", "port": "8080", "weight": "10", "backup": "true",
		"admin_state_up": "true", "monitor_address": "10.0.1.5", "monitor_port": "8081",
		"tags": "api, blue", "created_at": "2026-07-18T10:15:30Z",
		"updated_at": "2026-07-19T11:20:45Z",
	}
	for key, value := range want {
		if result.Attrs[key] != value {
			t.Errorf("member attribute %s = %q, want %q", key, result.Attrs[key], value)
		}
	}
}

func TestFetchAmphoraDetailReturnsOverviewAttributes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/octavia/amphorae/amp-1" {
			t.Errorf("request path = %q, want amphora detail endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"amphora":{
			"id":"amp-1","loadbalancer_id":"lb-1","compute_id":"server-1",
			"role":"MASTER","status":"ALLOCATED","lb_network_ip":"10.0.3.20",
			"ha_ip":"203.0.113.9","ha_port_id":"ha-port-1",
			"vrrp_port_id":"vrrp-port-1","vrrp_ip":"10.0.3.30",
			"vrrp_interface":"eth1","vrrp_id":1,"vrrp_priority":100,
			"cached_zone":"nova","image_id":"image-1","cert_busy":false,
			"cert_expiration":"2026-08-20T12:00:00",
			"created_at":"2026-07-18T10:15:30","updated_at":"2026-07-19T11:20:45"
		}}`))
	}))
	defer server.Close()

	sc := &serviceClients{lb: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2/",
	}}
	c := &Clients{services: sc, activeServices: sc}
	node := model.NewNode(model.TypeAmphora, "amp-1", "amp-1")
	node.OwningLBID = "lb-1"
	result, err := c.FetchDetail(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"role": "MASTER", "status": "ALLOCATED", "lb_network_ip": "10.0.3.20",
		"ha_ip": "203.0.113.9", "ha_port_id": "ha-port-1", "compute_id": "server-1",
		"vrrp_port_id": "vrrp-port-1", "vrrp_ip": "10.0.3.30", "vrrp_interface": "eth1",
		"vrrp_id": "1", "vrrp_priority": "100", "cached_zone": "nova", "image_id": "image-1",
		"cert_expiration": "2026-08-20T12:00:00Z", "cert_busy": "false",
		"created_at": "2026-07-18T10:15:30Z", "updated_at": "2026-07-19T11:20:45Z",
	}
	for key, value := range want {
		if result.Attrs[key] != value {
			t.Errorf("amphora attribute %s = %q, want %q", key, result.Attrs[key], value)
		}
	}
}

func TestFetchHealthMonitorDetailReturnsSummaryAttributes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/lbaas/healthmonitors/hm-1" {
			t.Errorf("request path = %q, want health-monitor detail endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"healthmonitor":{
			"id":"hm-1","name":"api-health","type":"HTTP","delay":5,"timeout":3,
			"max_retries":2,"max_retries_down":3,"admin_state_up":true,
			"http_method":"GET","url_path":"/health","expected_codes":"200",
			"project_id":"project-1","created_at":"2026-07-18T10:15:30Z",
			"updated_at":"2026-07-19T11:20:45Z","tags":["api","blue"],
			"http_version":1.1,"domain_name":"api.example.test"
		}}`))
	}))
	defer server.Close()

	sc := &serviceClients{lb: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2/",
	}}
	c := &Clients{services: sc, activeServices: sc}
	node := model.NewNode(model.TypeHealthMonitor, "hm-1", "api-health")
	node.OwningLBID = "lb-1"
	result, err := c.FetchDetail(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"type": "HTTP", "delay": "5", "timeout": "3",
		"max_retries": "2", "max_retries_down": "3", "admin_state_up": "true",
		"http_method": "GET", "url_path": "/health", "expected_codes": "200",
		"project_id": "project-1", "created_at": "2026-07-18T10:15:30Z",
		"updated_at": "2026-07-19T11:20:45Z", "tags": "api, blue",
		"http_version": "1.1", "domain_name": "api.example.test",
	}
	for key, value := range want {
		if result.Attrs[key] != value {
			t.Errorf("health-monitor attribute %s = %q, want %q", key, result.Attrs[key], value)
		}
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

func TestListFloatingIPMappingsUsesOneProjectFilteredNeutronList(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("project_id"); got != "project-1" {
			t.Errorf("project_id query = %q, want project-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"floatingips":[
			{"id":"fip-1","port_id":"port-1","fixed_ip_address":"10.0.0.10","floating_ip_address":"198.51.100.10"},
			{"id":"unbound","floating_ip_address":"198.51.100.20"}
		]}`))
	}))
	defer server.Close()

	sc := &serviceClients{network: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2.0/",
	}}
	c := &Clients{services: sc, activeServices: sc, selected: ProjectInfo{ID: "project-1"}}
	items, err := c.ListFloatingIPMappings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("Neutron requests = %d, want one collection request", requests)
	}
	if len(items) != 1 || items[0] != (FloatingIPMapping{
		PortID: "port-1", FixedIP: "10.0.0.10", FloatingIP: "198.51.100.10",
	}) {
		t.Fatalf("floating-IP mappings = %+v", items)
	}
}
