package osclient

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

// Instance is the Nova server summary used by the compute area's instance list
// and detail view. Project, flavor, and image names are best-effort because
// Nova may expose only their IDs to the active token.
type Instance struct {
	ID               string            `json:"id" yaml:"id"`
	Name             string            `json:"name" yaml:"name"`
	Status           string            `json:"status" yaml:"status"`
	ProjectID        string            `json:"project_id,omitempty" yaml:"project_id,omitempty"`
	ProjectName      string            `json:"project_name,omitempty" yaml:"project_name,omitempty"`
	UserID           string            `json:"user_id,omitempty" yaml:"user_id,omitempty"`
	FlavorID         string            `json:"flavor_id,omitempty" yaml:"flavor_id,omitempty"`
	FlavorName       string            `json:"flavor_name,omitempty" yaml:"flavor_name,omitempty"`
	ImageID          string            `json:"image_id,omitempty" yaml:"image_id,omitempty"`
	ImageName        string            `json:"image_name,omitempty" yaml:"image_name,omitempty"`
	Addresses        []string          `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	PrimaryAddress   string            `json:"-" yaml:"-"`
	AvailabilityZone string            `json:"availability_zone,omitempty" yaml:"availability_zone,omitempty"`
	Host             string            `json:"host,omitempty" yaml:"host,omitempty"`
	InstanceName     string            `json:"instance_name,omitempty" yaml:"instance_name,omitempty"`
	KeyName          string            `json:"key_name,omitempty" yaml:"key_name,omitempty"`
	Created          time.Time         `json:"created,omitempty" yaml:"created,omitempty"`
	Updated          time.Time         `json:"updated,omitempty" yaml:"updated,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
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
			AvailabilityZone: item.AvailabilityZone, Host: item.Host, InstanceName: item.InstanceName, KeyName: item.KeyName,
			Created: item.Created, Updated: item.Updated, Metadata: item.Metadata,
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
