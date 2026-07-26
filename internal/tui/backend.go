package tui

import (
	"context"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
	"github.com/krisiasty/olb/internal/telemetry"
)

// Backend is the set of OpenStack operations the TUI drives asynchronously. It
// is an interface so the UI can be exercised against a fake, and so the async
// tea.Cmd layer has a single seam. *osclient.Clients satisfies it.
type Backend interface {
	ListLoadBalancers(ctx context.Context) ([]osclient.LB, error)
	GetTree(ctx context.Context, lbID string, hint *model.LBMeta) (*model.Tree, error)
	FetchDetail(ctx context.Context, n *model.Node) (osclient.DetailResult, error)
	LBStats(ctx context.Context, lbID string) (map[string]any, error)
	ListenerStats(ctx context.Context, lbID, listenerID string) (map[string]any, error)
	ListListenerSummaries(ctx context.Context, lbID string) (map[string]osclient.ListenerSummary, error)
	ListPoolSummaries(ctx context.Context, lbID string) (map[string]osclient.PoolSummary, error)
	ListListeners(ctx context.Context) ([]osclient.ListenerRow, error)
	ListPools(ctx context.Context) ([]osclient.PoolRow, error)
	ListAllAmphorae(ctx context.Context) ([]*model.Node, error)
	ListFloatingIPMappings(ctx context.Context) ([]osclient.FloatingIPMapping, error)
	ListCOEClusters(ctx context.Context) ([]osclient.COECluster, error)
	GetCOECluster(ctx context.Context, id string) (osclient.COEClusterDetail, error)
	ResolveFloatingIPs(ctx context.Context, lbID, portID string) (map[string]*model.Node, error)
	ResolveInstance(ctx context.Context, lbID, address string) (*model.Node, error)
	ListAmphorae(ctx context.Context, lbID string) ([]*model.Node, error)
	ListUsers(ctx context.Context) (osclient.IdentityList[osclient.User], error)
	ListDomains(ctx context.Context) (osclient.IdentityList[osclient.Domain], error)
	ListGroups(ctx context.Context) (osclient.IdentityList[osclient.Group], error)
	ListGroupMembers(ctx context.Context, groupID string) ([]osclient.User, error)
	ListUserGroups(ctx context.Context, userID string) ([]osclient.Group, error)
	ListUsersInDomain(ctx context.Context, domainID string) ([]osclient.User, error)
	ListGroupsInDomain(ctx context.Context, domainID string) ([]osclient.Group, error)
	ListProjectsInDomain(ctx context.Context, domainID string) ([]osclient.Project, error)
	ListProjectsDetailed(ctx context.Context) ([]osclient.Project, error)
	ListRoles(ctx context.Context) (osclient.IdentityList[osclient.Role], error)
	ListRoleAssignments(ctx context.Context, roleID string) ([]osclient.RoleAssignment, error)
	ListUserAssignments(ctx context.Context, userID string) ([]osclient.RoleAssignment, error)
	ListGroupAssignments(ctx context.Context, groupID string) ([]osclient.RoleAssignment, error)
	ListProjectAssignments(ctx context.Context, projectID string) ([]osclient.RoleAssignment, error)
	ListDomainAssignments(ctx context.Context, domainID string) ([]osclient.RoleAssignment, error)
	ListImpliedRoles(ctx context.Context, roleID string) ([]osclient.Role, error)
	ListServices(ctx context.Context) ([]osclient.Service, error)
	ListEndpoints(ctx context.Context) ([]osclient.Endpoint, error)
	ListRegions(ctx context.Context) ([]osclient.Region, error)
	CurrentToken() osclient.TokenInfo
	ListScopes(ctx context.Context) ([]osclient.ScopeInfo, error)
	SwitchScope(ctx context.Context, target osclient.ScopeInfo) error
	CurrentScope() osclient.ScopeInfo
}

// TelemetryBackend is optional so alternate/testing backends can run without
// HTTP instrumentation. The real OpenStack client implements it.
type TelemetryBackend interface {
	TelemetrySnapshot() telemetry.Snapshot
	ResetTelemetry()
}
