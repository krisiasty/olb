package osclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
)

func TestListHypervisorAcceleratorsLoadsManyProvidersWithBoundedConcurrency(t *testing.T) {
	const providerCount = 16
	var active atomic.Int32
	var maximum atomic.Int32

	providers := make([]map[string]any, 0, providerCount)
	for index := range providerCount {
		providers = append(providers, map[string]any{
			"uuid":                 fmt.Sprintf("provider-%02d", index),
			"name":                 fmt.Sprintf("compute-gpu_0000:%02X:00.0", 0x80+index),
			"generation":           1,
			"root_provider_uuid":   "hypervisor-1",
			"parent_provider_uuid": "hypervisor-1",
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.Header.Get("OpenStack-API-Version"); got != "placement 1.18" {
			t.Errorf("Placement microversion = %q, want placement 1.18", got)
		}
		if r.URL.Path == "/resource_providers" {
			if got := r.URL.Query().Get("in_tree"); got != "hypervisor-1" {
				t.Errorf("in_tree = %q", got)
			}
			if got := r.URL.Query().Get("required"); got != managedPCITrait {
				t.Errorf("required trait = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource_providers": providers})
			return
		}

		now := active.Add(1)
		for {
			seen := maximum.Load()
			if now <= seen || maximum.CompareAndSwap(seen, now) {
				break
			}
		}
		defer active.Add(-1)
		time.Sleep(2 * time.Millisecond)

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "resource_providers" {
			http.NotFound(w, r)
			return
		}
		index := 0
		_, _ = fmt.Sscanf(parts[1], "provider-%02d", &index)
		resourceClass := "CUSTOM_GPU_NVIDIA_RTX_PRO_6000WM"
		switch parts[2] {
		case "inventories":
			total := 1
			if index == 1 {
				total = 16
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource_provider_generation": 1,
				"inventories": map[string]any{
					resourceClass: map[string]any{
						"allocation_ratio": 1.0, "min_unit": 1, "max_unit": total,
						"reserved": 0, "step_size": 1, "total": total,
					},
				},
			})
		case "allocations":
			allocations := map[string]any{}
			if index == 0 {
				allocations["instance-00"] = map[string]any{
					"resources": map[string]int{resourceClass: 1},
				}
			}
			if index == 1 {
				allocations["instance-a"] = map[string]any{
					"resources": map[string]int{resourceClass: 5},
				}
				allocations["instance-b"] = map[string]any{
					"resources": map[string]int{resourceClass: 6},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource_provider_generation": 1,
				"allocations":                  allocations,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	placement := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/",
		Type:           "placement",
	}
	sc := &serviceClients{placement: placement}
	client := &Clients{services: sc, activeServices: sc}
	got, err := client.ListHypervisorAccelerators(context.Background(), "hypervisor-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != providerCount {
		t.Fatalf("accelerator rows = %d, want %d", len(got), providerCount)
	}
	if got[0].PCIAddress != "0000:80:00.0" ||
		got[0].DisplayName != "NVIDIA RTX PRO 6000WM" ||
		got[0].Used != 1 || len(got[0].Allocations) != 1 {
		t.Fatalf("first accelerator = %+v", got[0])
	}
	if got[1].Total != 16 || got[1].Used != 11 || len(got[1].Allocations) != 2 {
		t.Fatalf("pooled accelerator inventory = %+v", got[1])
	}
	if max := maximum.Load(); max <= 1 || max > acceleratorRequestConcurrency {
		t.Fatalf("concurrent Placement requests = %d, want 2..%d", max, acceleratorRequestConcurrency)
	}
	if placement.Microversion != "" {
		t.Fatalf("shared Placement client microversion changed to %q", placement.Microversion)
	}
}

func TestListHypervisorAcceleratorsHandlesPlacementAvailabilityAndRBAC(t *testing.T) {
	client := &Clients{activeServices: &serviceClients{}}
	if _, err := client.ListHypervisorAccelerators(context.Background(), "hypervisor-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing Placement error = %v, want ErrUnavailable", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errors":[{"status":403}]}`, http.StatusForbidden)
	}))
	defer server.Close()
	placement := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/",
		Type:           "placement",
	}
	client = &Clients{activeServices: &serviceClients{placement: placement}}
	if _, err := client.ListHypervisorAccelerators(context.Background(), "hypervisor-1"); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("forbidden Placement error = %v, want ErrAdminRequired", err)
	}
}

func TestAcceleratorProviderFormatting(t *testing.T) {
	if got := acceleratorPCIAddress("compute_0001:c1:00.0"); got != "0001:C1:00.0" {
		t.Fatalf("PCI address = %q", got)
	}
	if got := acceleratorDisplayName("CUSTOM_GPU_NVIDIA_ADA_L40S"); got != "NVIDIA ADA L40S" {
		t.Fatalf("accelerator display name = %q", got)
	}
}
