package osclient

import (
	"context"
	"fmt"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/containerinfra/v1/clusters"
)

// COECluster is the Magnum cluster-list data used to associate Kubernetes
// API-server and Service load balancers with their owning cluster.
type COECluster struct {
	UUID              string            `json:"uuid" yaml:"uuid"`
	Name              string            `json:"name" yaml:"name"`
	ProjectID         string            `json:"project_id,omitempty" yaml:"project_id,omitempty"`
	StackID           string            `json:"stack_id" yaml:"stack_id"`
	ClusterTemplateID string            `json:"cluster_template_id" yaml:"cluster_template_id"`
	KeyPair           string            `json:"keypair" yaml:"keypair"`
	NodeCount         int               `json:"node_count" yaml:"node_count"`
	MasterCount       int               `json:"master_count" yaml:"master_count"`
	FlavorID          string            `json:"flavor_id" yaml:"flavor_id"`
	MasterFlavorID    string            `json:"master_flavor_id" yaml:"master_flavor_id"`
	Status            string            `json:"status" yaml:"status"`
	HealthStatus      string            `json:"health_status" yaml:"health_status"`
	Labels            map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ListCOEClusters lists Magnum clusters once for the active credential scope.
// Magnum is optional so clouds without it still retain the complete Octavia UI.
func (c *Clients) ListCOEClusters(ctx context.Context) ([]COECluster, error) {
	sc, err := c.clientsForLB(ctx, "")
	if err != nil {
		return nil, err
	}
	if sc.container == nil {
		return nil, fmt.Errorf("magnum: %w", ErrUnavailable)
	}

	pages, err := clusters.List(sc.container, nil).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	items, err := clusters.ExtractClusters(pages)
	if err != nil {
		return nil, err
	}
	out := make([]COECluster, 0, len(items))
	for _, item := range items {
		out = append(out, COECluster{
			UUID: item.UUID, Name: item.Name, ProjectID: item.ProjectID,
			StackID: item.StackID, ClusterTemplateID: item.ClusterTemplateID,
			KeyPair: item.KeyPair, NodeCount: item.NodeCount, MasterCount: item.MasterCount,
			FlavorID: item.FlavorID, MasterFlavorID: item.MasterFlavorID,
			Status: item.Status, HealthStatus: item.HealthStatus, Labels: item.Labels,
		})
	}
	return out, nil
}

// COEClusterDetail holds the extra Magnum cluster fields that the brief cluster
// list omits and only the per-cluster detail call populates — notably the
// Kubernetes API endpoint (served by the cluster's master load balancer), the
// running version, per-node health, and networking flags. It is fetched lazily
// because that endpoint is slow.
type COEClusterDetail struct {
	APIAddress         string            `json:"api_address" yaml:"api_address"`
	COEVersion         string            `json:"coe_version" yaml:"coe_version"`
	StatusReason       string            `json:"status_reason,omitempty" yaml:"status_reason,omitempty"`
	HealthStatusReason map[string]string `json:"health_status_reason,omitempty" yaml:"health_status_reason,omitempty"`
	MasterAddresses    []string          `json:"master_addresses,omitempty" yaml:"master_addresses,omitempty"`
	NodeAddresses      []string          `json:"node_addresses,omitempty" yaml:"node_addresses,omitempty"`
	FixedNetwork       string            `json:"fixed_network,omitempty" yaml:"fixed_network,omitempty"`
	FixedSubnet        string            `json:"fixed_subnet,omitempty" yaml:"fixed_subnet,omitempty"`
	FloatingIPEnabled  bool              `json:"floating_ip_enabled" yaml:"floating_ip_enabled"`
	MasterLBEnabled    bool              `json:"master_lb_enabled" yaml:"master_lb_enabled"`
	CreatedAt          string            `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	UpdatedAt          string            `json:"updated_at,omitempty" yaml:"updated_at,omitempty"`
}

// GetCOECluster fetches one Magnum cluster's full detail (the slow per-cluster
// endpoint the list call does not populate). Callers fetch it lazily and cache
// it by UUID.
func (c *Clients) GetCOECluster(ctx context.Context, id string) (COEClusterDetail, error) {
	sc, err := c.clientsForLB(ctx, "")
	if err != nil {
		return COEClusterDetail{}, err
	}
	if sc.container == nil {
		return COEClusterDetail{}, fmt.Errorf("magnum: %w", ErrUnavailable)
	}
	cluster, err := clusters.Get(ctx, sc.container, id).Extract()
	if err != nil {
		return COEClusterDetail{}, err
	}
	var health map[string]string
	if len(cluster.HealthStatusReason) > 0 {
		health = make(map[string]string, len(cluster.HealthStatusReason))
		for k, v := range cluster.HealthStatusReason {
			health[k] = fmt.Sprintf("%v", v)
		}
	}
	return COEClusterDetail{
		APIAddress: cluster.APIAddress, COEVersion: cluster.COEVersion,
		StatusReason: cluster.StatusReason, HealthStatusReason: health,
		MasterAddresses: cluster.MasterAddresses, NodeAddresses: cluster.NodeAddresses,
		FixedNetwork: cluster.FixedNetwork, FixedSubnet: cluster.FixedSubnet,
		FloatingIPEnabled: cluster.FloatingIPEnabled, MasterLBEnabled: cluster.MasterLBEnabled,
		CreatedAt: formatClusterTime(cluster.CreatedAt), UpdatedAt: formatClusterTime(cluster.UpdatedAt),
	}, nil
}

func formatClusterTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
