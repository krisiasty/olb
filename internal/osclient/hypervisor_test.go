package osclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestListHypervisorsExtractsDetailedInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2.1/os-hypervisors/detail" {
			t.Errorf("request path = %q, want hypervisor detail list", r.URL.Path)
		}
		if got := r.Header.Get("X-OpenStack-Nova-API-Version"); got != "2.53" {
			t.Errorf("Nova microversion = %q, want 2.53 for hypervisor UUIDs", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hypervisors":[{
			"id":"c48f6247-abe4-4a24-824e-ea39e108874f","hypervisor_hostname":"compute-01",
			"hypervisor_type":"QEMU","hypervisor_version":8002000,
			"state":"up","status":"enabled","host_ip":"192.0.2.21",
			"service":{"id":"service-1","host":"compute-01","disabled_reason":null},
			"current_workload":2,"running_vms":12,
			"vcpus":64,"vcpus_used":28,
			"memory_mb":262144,"memory_mb_used":98304,
			"local_gb":1800,"local_gb_used":740,"disk_available_least":920,
			"free_disk_gb":1060,"free_ram_mb":163840,
			"cpu_info":{"vendor":"Intel","arch":"x86_64","model":"Skylake-Server",
				"features":["vmx","aes"],"topology":{"cells":2,"sockets":2,"cores":16,"threads":2}}
		}],"hypervisors_links":[]}`))
	}))
	defer server.Close()

	sc := &serviceClients{compute: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2.1/",
		Type:           "compute",
	}}
	client := &Clients{services: sc, activeServices: sc}
	got, err := client.ListHypervisors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("hypervisors = %+v", got)
	}
	h := got[0]
	if h.ID != "c48f6247-abe4-4a24-824e-ea39e108874f" || h.Hostname != "compute-01" || h.Type != "QEMU" ||
		h.State != "up" || h.Status != "enabled" || h.HostIP != "192.0.2.21" {
		t.Fatalf("hypervisor identity/state = %+v", h)
	}
	if sc.compute.Microversion != "" {
		t.Fatalf("shared compute client microversion changed to %q", sc.compute.Microversion)
	}
	if h.VCPUs != 64 || h.VCPUsUsed != 28 || h.MemoryMB != 262144 ||
		h.MemoryMBUsed != 98304 || h.LocalGB != 1800 || h.LocalGBUsed != 740 ||
		h.RunningVMs != 12 {
		t.Fatalf("hypervisor capacity = %+v", h)
	}
	if h.CPUVendor != "Intel" || h.CPUModel != "Skylake-Server" || h.CPUArch != "x86_64" ||
		h.CPUCells != 2 || h.CPUSockets != 2 || h.CPUCores != 16 || h.CPUThreads != 2 {
		t.Fatalf("hypervisor CPU = %+v", h)
	}
}

func TestListHypervisorsTranslatesForbiddenToRBACError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"forbidden":{"code":403}}`, http.StatusForbidden)
	}))
	defer server.Close()
	sc := &serviceClients{compute: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v2.1/",
		Type:           "compute",
	}}
	client := &Clients{activeServices: sc}
	if _, err := client.ListHypervisors(context.Background()); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("forbidden hypervisor list = %v, want ErrAdminRequired", err)
	}
}
