package osclient

import (
	"context"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/amphorae"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/listeners"
	"github.com/gophercloud/gophercloud/v2/openstack/loadbalancer/v2/pools"
	"github.com/krisiasty/olb/internal/model"
)

// ListenerRow is a listener summary for the top-level listeners list view. It
// carries its owning load balancer's ID so the UI can label the row and drill in;
// the owning name is resolved by the UI from the load-balancer list.
type ListenerRow struct {
	ID                 string
	Name               string
	Protocol           string
	ProtocolPort       int
	LBID               string
	ProjectID          string
	ProvisioningStatus string
	OperatingStatus    string
}

// PoolRow is a pool summary for the top-level pools list view.
type PoolRow struct {
	ID                 string
	Name               string
	Protocol           string
	LBMethod           string
	MemberCount        int
	LBID               string
	ProjectID          string
	ProvisioningStatus string
	OperatingStatus    string
}

// ListListeners lists every listener visible to the original token, applying the
// same local project presentation filter as ListLoadBalancers: an empty project
// selection (all-projects mode) returns Octavia's unfiltered view; a concrete
// selection keeps only that project's listeners. The authenticated scope is never
// narrowed.
func (c *Clients) ListListeners(ctx context.Context) ([]ListenerRow, error) {
	c.mu.Lock()
	allMode := c.allMode
	selected := c.selected
	sc := c.services
	c.mu.Unlock()

	pages, err := listeners.List(sc.lb, listeners.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	items, err := listeners.ExtractListeners(pages)
	if err != nil {
		return nil, err
	}
	out := make([]ListenerRow, 0, len(items))
	for _, l := range items {
		if !allMode && selected.ID != "" && l.ProjectID != selected.ID {
			continue
		}
		out = append(out, ListenerRow{
			ID: l.ID, Name: l.Name, Protocol: l.Protocol, ProtocolPort: l.ProtocolPort,
			LBID: firstLBID(loadBalancerIDs(l.Loadbalancers)), ProjectID: l.ProjectID,
			ProvisioningStatus: l.ProvisioningStatus, OperatingStatus: l.OperatingStatus,
		})
	}
	return out, nil
}

// ListPools lists every pool visible to the original token, with the same local
// project filter as ListListeners.
func (c *Clients) ListPools(ctx context.Context) ([]PoolRow, error) {
	c.mu.Lock()
	allMode := c.allMode
	selected := c.selected
	sc := c.services
	c.mu.Unlock()

	pages, err := pools.List(sc.lb, pools.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	items, err := pools.ExtractPools(pages)
	if err != nil {
		return nil, err
	}
	out := make([]PoolRow, 0, len(items))
	for _, p := range items {
		if !allMode && selected.ID != "" && p.ProjectID != selected.ID {
			continue
		}
		lbID := ""
		if len(p.Loadbalancers) > 0 {
			lbID = p.Loadbalancers[0].ID
		}
		out = append(out, PoolRow{
			ID: p.ID, Name: p.Name, Protocol: p.Protocol, LBMethod: p.LBMethod,
			MemberCount: len(p.Members), LBID: lbID, ProjectID: p.ProjectID,
			ProvisioningStatus: p.ProvisioningStatus, OperatingStatus: p.OperatingStatus,
		})
	}
	return out, nil
}

// ListAllAmphorae returns every amphora VM in the cluster (no load-balancer
// filter). Admin-only, like the per-LB variant: a 403 becomes ErrAdminRequired so
// the caller can degrade gracefully. Amphorae carry no project attribute, so the
// project filter does not apply — the owning LB gives their scope in the UI.
func (c *Clients) ListAllAmphorae(ctx context.Context) ([]*model.Node, error) {
	c.mu.Lock()
	sc := c.services
	c.mu.Unlock()

	pages, err := amphorae.List(sc.lb, amphorae.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	as, err := amphorae.ExtractAmphorae(pages)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Node, 0, len(as))
	for _, a := range as {
		out = append(out, amphoraNode(a, a.LoadbalancerID))
	}
	return out, nil
}

func loadBalancerIDs(refs []listeners.LoadBalancerID) []string {
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}
	return ids
}

func firstLBID(ids []string) string {
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}
