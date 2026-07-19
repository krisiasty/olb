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
	ListListenerSummaries(ctx context.Context, lbID string) (map[string]osclient.ListenerSummary, error)
	ListPoolSummaries(ctx context.Context, lbID string) (map[string]osclient.PoolSummary, error)
	ListListeners(ctx context.Context) ([]osclient.ListenerRow, error)
	ListPools(ctx context.Context) ([]osclient.PoolRow, error)
	ListAllAmphorae(ctx context.Context) ([]*model.Node, error)
	ResolveFloatingIPs(ctx context.Context, lbID, portID string) (map[string]*model.Node, error)
	ResolveInstance(ctx context.Context, lbID, address string) (*model.Node, error)
	ListAmphorae(ctx context.Context, lbID string) ([]*model.Node, error)
	ListProjects(ctx context.Context) ([]osclient.ProjectInfo, error)
	SwitchProject(ctx context.Context, target osclient.ProjectInfo) error
	EnterAllProjects(ctx context.Context) error
	CurrentProject() osclient.ProjectInfo
	AllProjects() bool
	SwitchCapability() osclient.SwitchCapability
}

// TelemetryBackend is optional so alternate/testing backends can run without
// HTTP instrumentation. The real OpenStack client implements it.
type TelemetryBackend interface {
	TelemetrySnapshot() telemetry.Snapshot
	ResetTelemetry()
}
