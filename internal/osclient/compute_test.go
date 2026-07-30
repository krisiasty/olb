package osclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestListInstancesFollowsAuthenticationScope(t *testing.T) {
	tests := []struct {
		name        string
		scope       ScopeInfo
		wantProject string
	}{
		{
			name: "project", scope: ScopeInfo{Kind: ScopeProject, ID: "project-1", Name: "alpha"},
			wantProject: "alpha",
		},
		{
			name: "system", scope: ScopeInfo{Kind: ScopeSystem, ID: "all", Name: "all"},
		},
		{
			name: "domain", scope: ScopeInfo{Kind: ScopeDomain, ID: "default", Name: "Default"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2.1/servers/detail" {
					t.Errorf("request path = %q, want Nova detailed server list", r.URL.Path)
				}
				if got := r.URL.Query().Get("all_tenants"); got != "" {
					t.Errorf("all_tenants = %q, want no override for the active token scope", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"servers":[{
					"id":"server-1","name":"api-1","status":"ACTIVE",
					"tenant_id":"project-1","user_id":"user-1",
					"flavor":{"id":"flavor-1","original_name":"m1.small"},
					"image":{"id":"image-1"},
					"addresses":{
						"public":[{"addr":"203.0.113.10","version":4}],
						"private":[{"addr":"10.0.0.12","version":4}]
					},
					"OS-EXT-AZ:availability_zone":"nova",
					"OS-EXT-SRV-ATTR:host":"compute-service-01",
					"OS-EXT-SRV-ATTR:hypervisor_hostname":"compute-01",
					"OS-EXT-SRV-ATTR:instance_name":"instance-0000012a",
					"created":"2026-07-30T08:15:00Z",
					"updated":"2026-07-30T08:20:00Z"
				}],"servers_links":[]}`))
			}))
			defer server.Close()

			sc := &serviceClients{compute: &gophercloud.ServiceClient{
				ProviderClient: &gophercloud.ProviderClient{},
				Endpoint:       server.URL + "/v2.1/",
			}}
			client := &Clients{services: sc, activeServices: sc, scope: tt.scope}
			got, err := client.ListInstances(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("instances = %+v", got)
			}
			instance := got[0]
			if instance.ID != "server-1" || instance.Name != "api-1" || instance.Status != "ACTIVE" {
				t.Fatalf("instance identity/state = %+v", instance)
			}
			if instance.ProjectID != "project-1" || instance.ProjectName != tt.wantProject {
				t.Fatalf("instance project = %q/%q", instance.ProjectName, instance.ProjectID)
			}
			if instance.FlavorID != "flavor-1" || instance.FlavorName != "m1.small" || instance.ImageID != "image-1" {
				t.Fatalf("instance image/flavor = %+v", instance)
			}
			if instance.InstanceName != "instance-0000012a" {
				t.Fatalf("libvirt instance name = %q", instance.InstanceName)
			}
			if instance.Host != "compute-service-01" || instance.HypervisorHostname != "compute-01" {
				t.Fatalf("instance placement hosts = %q / %q", instance.Host, instance.HypervisorHostname)
			}
			if instance.PrimaryAddress != "10.0.0.12" || len(instance.Addresses) != 2 ||
				instance.Addresses[0] != "private=10.0.0.12" || instance.Addresses[1] != "public=203.0.113.10" {
				t.Fatalf("instance addresses = %v (primary %q)", instance.Addresses, instance.PrimaryAddress)
			}
		})
	}
}

func TestListInstancesReportsMissingNova(t *testing.T) {
	client := &Clients{activeServices: &serviceClients{}}
	if _, err := client.ListInstances(context.Background()); err == nil {
		t.Fatal("missing Nova client should return an error")
	}
}

func TestListInstancesSystemScopeUsesOrdinaryVisibleServerList(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("all_tenants"); got != "" {
			t.Errorf("all_tenants = %q, want ordinary scope-aware list", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[{"id":"server-1","name":"visible","status":"ACTIVE"}],"servers_links":[]}`))
	}))
	defer server.Close()

	sc := &serviceClients{compute: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2.1/",
	}}
	client := &Clients{
		services: sc, activeServices: sc,
		scope: ScopeInfo{Kind: ScopeSystem, ID: "all", Name: "all"},
	}
	got, err := client.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Nova list calls = %d, want one ordinary list", calls)
	}
	if len(got) != 1 || got[0].ID != "server-1" {
		t.Fatalf("fallback instances = %+v", got)
	}
}

func TestListInstancesTranslatesForbiddenToRBACError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, `{"forbidden":{"code":403}}`, http.StatusForbidden)
	}))
	defer server.Close()

	sc := &serviceClients{compute: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2.1/",
	}}
	client := &Clients{
		services: sc, activeServices: sc,
		scope: ScopeInfo{Kind: ScopeSystem, ID: "all", Name: "all"},
	}
	if _, err := client.ListInstances(context.Background()); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("forbidden Nova list error = %v, want ErrAdminRequired", err)
	}
	if calls != 1 {
		t.Fatalf("Nova list calls = %d, want one ordinary list", calls)
	}
}
