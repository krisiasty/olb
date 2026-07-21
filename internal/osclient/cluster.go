package osclient

import (
	"context"
	"fmt"

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
