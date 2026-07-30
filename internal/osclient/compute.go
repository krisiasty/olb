package osclient

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

// Instance is the Nova server summary used by the compute area's instance list
// and detail view. Project, flavor, and image names are best-effort because
// Nova may expose only their IDs to the active token.
type Instance struct {
	ID                 string            `json:"id" yaml:"id"`
	Name               string            `json:"name" yaml:"name"`
	Status             string            `json:"status" yaml:"status"`
	ProjectID          string            `json:"project_id,omitempty" yaml:"project_id,omitempty"`
	ProjectName        string            `json:"project_name,omitempty" yaml:"project_name,omitempty"`
	UserID             string            `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	FlavorID           string            `json:"flavor_id,omitempty" yaml:"flavor_id,omitempty"`
	FlavorName         string            `json:"flavor_name,omitempty" yaml:"flavor_name,omitempty"`
	ImageID            string            `json:"image_id,omitempty" yaml:"image_id,omitempty"`
	ImageName          string            `json:"image_name,omitempty" yaml:"image_name,omitempty"`
	Addresses          []string          `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	PrimaryAddress     string            `json:"-" yaml:"-"`
	AvailabilityZone   string            `json:"availability_zone,omitempty" yaml:"availability_zone,omitempty"`
	Host               string            `json:"host,omitempty" yaml:"host,omitempty"`
	HypervisorHostname string            `json:"hypervisor_hostname,omitempty" yaml:"hypervisor_hostname,omitempty"`
	InstanceName       string            `json:"instance_name,omitempty" yaml:"instance_name,omitempty"`
	KeyName            string            `json:"key_name,omitempty" yaml:"key_name,omitempty"`
	Created            time.Time         `json:"created,omitempty" yaml:"created,omitempty"`
	Updated            time.Time         `json:"updated,omitempty" yaml:"updated,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Hypervisor is the Nova compute-host summary used by the compute area's
// hypervisor list and detail view. Capacity fields may be absent on newer Nova
// microversions; a zero total is therefore treated as unavailable by the UI.
type Hypervisor struct {
	ID                 string   `json:"id" yaml:"id"`
	Hostname           string   `json:"hostname" yaml:"hostname"`
	Type               string   `json:"type,omitempty" yaml:"type,omitempty"`
	Version            int      `json:"version,omitempty" yaml:"version,omitempty"`
	State              string   `json:"state,omitempty" yaml:"state,omitempty"`
	Status             string   `json:"status,omitempty" yaml:"status,omitempty"`
	HostIP             string   `json:"host_ip,omitempty" yaml:"host_ip,omitempty"`
	ServiceHost        string   `json:"service_host,omitempty" yaml:"service_host,omitempty"`
	ServiceID          string   `json:"service_id,omitempty" yaml:"service_id,omitempty"`
	DisabledReason     string   `json:"disabled_reason,omitempty" yaml:"disabled_reason,omitempty"`
	CurrentWorkload    int      `json:"current_workload" yaml:"current_workload"`
	RunningVMs         int      `json:"running_vms" yaml:"running_vms"`
	VCPUs              int      `json:"vcpus,omitempty" yaml:"vcpus,omitempty"`
	VCPUsUsed          int      `json:"vcpus_used" yaml:"vcpus_used"`
	MemoryMB           int      `json:"memory_mb,omitempty" yaml:"memory_mb,omitempty"`
	MemoryMBUsed       int      `json:"memory_mb_used" yaml:"memory_mb_used"`
	LocalGB            int      `json:"local_gb,omitempty" yaml:"local_gb,omitempty"`
	LocalGBUsed        int      `json:"local_gb_used" yaml:"local_gb_used"`
	DiskAvailableLeast int      `json:"disk_available_least,omitempty" yaml:"disk_available_least,omitempty"`
	CPUArch            string   `json:"cpu_arch,omitempty" yaml:"cpu_arch,omitempty"`
	CPUModel           string   `json:"cpu_model,omitempty" yaml:"cpu_model,omitempty"`
	CPUVendor          string   `json:"cpu_vendor,omitempty" yaml:"cpu_vendor,omitempty"`
	CPUFeatures        []string `json:"cpu_features,omitempty" yaml:"cpu_features,omitempty"`
	CPUCells           int      `json:"cpu_cells,omitempty" yaml:"cpu_cells,omitempty"`
	CPUSockets         int      `json:"cpu_sockets,omitempty" yaml:"cpu_sockets,omitempty"`
	CPUCores           int      `json:"cpu_cores,omitempty" yaml:"cpu_cores,omitempty"`
	CPUThreads         int      `json:"cpu_threads,omitempty" yaml:"cpu_threads,omitempty"`
}

// ListInstances lists Nova servers visible in the active authentication scope.
// It deliberately does not add all_tenants: Nova applies the token's scope, and
// deployments may protect that explicit override with a separate policy.
func (c *Clients) ListInstances(ctx context.Context) ([]Instance, error) {
	sc, err := c.activeClients()
	if err != nil {
		return nil, err
	}
	if sc.compute == nil {
		return nil, fmt.Errorf("nova: %w", ErrUnavailable)
	}

	c.mu.Lock()
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()
	pages, err := servers.List(sc.compute, servers.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := servers.ExtractServers(pages)
	if err != nil {
		return nil, err
	}

	projectNames := c.projectNameMap(ctx)
	out := make([]Instance, 0, len(items))
	for _, item := range items {
		flavorID, flavorName := resourceRef(item.Flavor)
		imageID, imageName := resourceRef(item.Image)
		addresses, primary := instanceAddresses(item.Addresses)
		projectName := projectNames[item.TenantID]
		if projectName == "" && scope.Kind == ScopeProject && item.TenantID == scope.ID {
			projectName = scope.Name
		}
		out = append(out, Instance{
			ID: item.ID, Name: item.Name, Status: item.Status,
			ProjectID: item.TenantID, ProjectName: projectName, UserID: item.UserID,
			FlavorID: flavorID, FlavorName: flavorName,
			ImageID: imageID, ImageName: imageName,
			Addresses: addresses, PrimaryAddress: primary,
			AvailabilityZone: item.AvailabilityZone, Host: item.Host, HypervisorHostname: item.HypervisorHostname,
			InstanceName: item.InstanceName, KeyName: item.KeyName,
			Created: item.Created, Updated: item.Updated, Metadata: item.Metadata,
		})
	}
	return out, nil
}

// ListHypervisors lists Nova compute hosts visible to the active token.
// Hypervisor inventory is normally admin-only, so a 403 is translated to the
// shared RBAC sentinel for a friendly explanatory empty view. A private copy
// of the compute client uses microversion 2.53 because that is where Nova
// changed hypervisor IDs from database integers to stable UUIDs. Pinning this
// request also retains the detailed capacity fields removed in later versions,
// without changing the microversion used by other Nova operations.
func (c *Clients) ListHypervisors(ctx context.Context) ([]Hypervisor, error) {
	sc, err := c.activeClients()
	if err != nil {
		return nil, err
	}
	if sc.compute == nil {
		return nil, fmt.Errorf("nova: %w", ErrUnavailable)
	}
	compute := *sc.compute
	compute.Microversion = "2.53"
	pages, err := hypervisors.List(&compute, nil).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := hypervisors.ExtractHypervisors(pages)
	if err != nil {
		return nil, err
	}
	out := make([]Hypervisor, 0, len(items))
	for _, item := range items {
		out = append(out, Hypervisor{
			ID: item.ID, Hostname: item.HypervisorHostname,
			Type: item.HypervisorType, Version: item.HypervisorVersion,
			State: item.State, Status: item.Status, HostIP: item.HostIP,
			ServiceHost: item.Service.Host, ServiceID: item.Service.ID, DisabledReason: item.Service.DisabledReason,
			CurrentWorkload: item.CurrentWorkload, RunningVMs: item.RunningVMs,
			VCPUs: item.VCPUs, VCPUsUsed: item.VCPUsUsed,
			MemoryMB: item.MemoryMB, MemoryMBUsed: item.MemoryMBUsed,
			LocalGB: item.LocalGB, LocalGBUsed: item.LocalGBUsed,
			DiskAvailableLeast: item.DiskAvailableLeast,
			CPUArch:            item.CPUInfo.Arch, CPUModel: item.CPUInfo.Model, CPUVendor: item.CPUInfo.Vendor,
			CPUFeatures: item.CPUInfo.Features,
			CPUCells:    item.CPUInfo.Topology.Cells, CPUSockets: item.CPUInfo.Topology.Sockets,
			CPUCores: item.CPUInfo.Topology.Cores, CPUThreads: item.CPUInfo.Topology.Threads,
		})
	}
	return out, nil
}

// resourceRef extracts the stable ID and the best human-readable name from
// Nova's image/flavor reference maps. "original_name" is available for flavors
// on newer microversions; older clouds generally return only an ID.
func resourceRef(ref map[string]any) (id, name string) {
	if ref == nil {
		return "", ""
	}
	if value, ok := ref["id"].(string); ok {
		id = value
	}
	for _, key := range []string{"original_name", "name"} {
		if value, ok := ref[key].(string); ok && value != "" {
			name = value
			break
		}
	}
	return id, name
}

// instanceAddresses flattens Nova's network-keyed address collection into
// stable "network=address" values while retaining the first raw IP for sorting.
func instanceAddresses(networks map[string]any) ([]string, string) {
	names := make([]string, 0, len(networks))
	for name := range networks {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	primary := ""
	for _, network := range names {
		values, ok := networks[network].([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			address, ok := value.(map[string]any)
			if !ok {
				continue
			}
			ip, _ := address["addr"].(string)
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			if primary == "" {
				primary = ip
			}
			if network == "" {
				out = append(out, ip)
			} else {
				out = append(out, network+"="+ip)
			}
		}
	}
	return out, primary
}
