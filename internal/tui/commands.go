package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// requestTimeout bounds every backend round trip so a hung API can't wedge the
// UI (the command goroutine returns an error msg instead).
const requestTimeout = 30 * time.Second

func ctxTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), requestTimeout)
}

// --- messages -------------------------------------------------------------

type lbsMsg struct {
	lbs []osclient.LB
	err error
}

type treeMsg struct {
	lbID       string
	tree       *model.Tree
	err        error
	forID      model.Identity // identity to render once the tree is in
	background bool           // stale-refresh: don't disturb the view on error
}

// detailIntent is what to do once a node's full configuration has loaded.
type detailIntent int

const (
	intentOverview detailIntent = iota
	intentYAML
	intentJSON
)

type detailMsg struct {
	nodeID    string
	lbID      string
	res       osclient.DetailResult
	intent    detailIntent
	refresh   bool
	workspace listKind
	err       error
}

type refResolveMsg struct {
	sourceID  string // node whose unresolved edge we followed
	lbID      string
	workspace listKind
	label     string // edge label (e.g. "floating IP", "instance")
	node      *model.Node
	err       error
}

type amphoraeMsg struct {
	lbID    string
	nodes   []*model.Node
	refresh bool
	err     error
}

// Top-level resource-list load results (keys 3/4/5). Each carries whether it was
// a background refresh so a failure can be reported without wiping the view.
type listenersMsg struct {
	rows    []osclient.ListenerRow
	refresh bool
	err     error
}

type poolsMsg struct {
	rows    []osclient.PoolRow
	refresh bool
	err     error
}

type amphoraeListMsg struct {
	nodes   []*model.Node
	refresh bool
	err     error
}

type projectsMsg struct {
	projects []osclient.ProjectInfo
	err      error
}

type switchedMsg struct {
	project osclient.ProjectInfo
	all     bool
	err     error
}

type statsMsg struct {
	lbID      string
	stats     map[string]any
	sampledAt time.Time
	refresh   bool
	automatic bool
	err       error
}

type lbFloatingIPMsg struct {
	lbID    string
	nodes   map[string]*model.Node // keyed by fixed VIP address
	refresh bool
	err     error
}

type listenerSummariesMsg struct {
	lbID    string
	items   map[string]osclient.ListenerSummary
	refresh bool
	err     error
}

type poolSummariesMsg struct {
	lbID    string
	items   map[string]osclient.PoolSummary
	refresh bool
	err     error
}

type flashClearMsg struct{ token int }

// --- commands -------------------------------------------------------------

func (m Model) loadLBsCmd() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		lbs, err := b.ListLoadBalancers(ctx)
		return lbsMsg{lbs: lbs, err: err}
	}
}

func (m Model) loadListenersCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		rows, err := b.ListListeners(ctx)
		return listenersMsg{rows: rows, refresh: refresh, err: err}
	}
}

func (m Model) loadPoolsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		rows, err := b.ListPools(ctx)
		return poolsMsg{rows: rows, refresh: refresh, err: err}
	}
}

func (m Model) loadAmphoraeListCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ListAllAmphorae(ctx)
		return amphoraeListMsg{nodes: nodes, refresh: refresh, err: err}
	}
}

func (m Model) getTreeCmd(lbID string, forID model.Identity, background bool) tea.Cmd {
	b := m.backend
	var hint *model.LBMeta
	for _, lb := range m.lbs {
		if lb.ID == lbID {
			h := lb.Meta()
			hint = &h
			break
		}
	}
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		tree, err := b.GetTree(ctx, lbID, hint)
		return treeMsg{lbID: lbID, tree: tree, err: err, forID: forID, background: background}
	}
}

func (m Model) fetchDetailCmd(n *model.Node, intent detailIntent) tea.Cmd {
	return m.detailCmd(n, intent, false)
}

func (m Model) refreshDetailCmd(n *model.Node) tea.Cmd {
	return m.detailCmd(n, intentOverview, true)
}

func (m Model) detailCmd(n *model.Node, intent detailIntent, refresh bool) tea.Cmd {
	b := m.backend
	id := n.ID
	lbID := n.OwningLBID
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		res, err := b.FetchDetail(ctx, n)
		return detailMsg{nodeID: id, lbID: lbID, res: res, intent: intent, refresh: refresh, workspace: workspace, err: err}
	}
}

func (m Model) resolveFloatingIPCmd(source *model.Node, portID string) tea.Cmd {
	b := m.backend
	sid := source.ID
	lbID := source.OwningLBID
	fixedIP := source.Attrs["address"]
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ResolveFloatingIPs(ctx, lbID, portID)
		node := nodes[fixedIP]
		return refResolveMsg{sourceID: sid, lbID: lbID, workspace: workspace, label: "floating IP", node: node, err: err}
	}
}

func (m Model) lbFloatingIPCmd(lbID, portID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ResolveFloatingIPs(ctx, lbID, portID)
		return lbFloatingIPMsg{lbID: lbID, nodes: nodes, refresh: refresh, err: err}
	}
}

func (m Model) resolveInstanceCmd(source *model.Node, address string) tea.Cmd {
	b := m.backend
	sid := source.ID
	lbID := source.OwningLBID
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		node, err := b.ResolveInstance(ctx, lbID, address)
		return refResolveMsg{sourceID: sid, lbID: lbID, workspace: workspace, label: "instance", node: node, err: err}
	}
}

func (m Model) loadAmphoraeCmd(lbID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ListAmphorae(ctx, lbID)
		return amphoraeMsg{lbID: lbID, nodes: nodes, refresh: refresh, err: err}
	}
}

func (m Model) loadProjectsCmd() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		ps, err := b.ListProjects(ctx)
		return projectsMsg{projects: ps, err: err}
	}
}

func (m Model) switchProjectCmd(target osclient.ProjectInfo) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		err := b.SwitchProject(ctx, target)
		return switchedMsg{project: b.CurrentProject(), all: b.AllProjects(), err: err}
	}
}

func (m Model) enterAllProjectsCmd() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		err := b.EnterAllProjects(ctx)
		return switchedMsg{project: b.CurrentProject(), all: b.AllProjects(), err: err}
	}
}

func (m Model) lbStatsCmd(lbID string) tea.Cmd {
	return m.statsCmd(lbID, false, false)
}

func (m Model) autoStatsCmd(lbID string) tea.Cmd {
	return m.statsCmd(lbID, false, true)
}

func (m Model) listenerSummariesCmd(lbID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		items, err := b.ListListenerSummaries(ctx, lbID)
		return listenerSummariesMsg{lbID: lbID, items: items, refresh: refresh, err: err}
	}
}

func (m Model) poolSummariesCmd(lbID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		items, err := b.ListPoolSummaries(ctx, lbID)
		return poolSummariesMsg{lbID: lbID, items: items, refresh: refresh, err: err}
	}
}

func (m Model) refreshStatsCmd(lbID string) tea.Cmd {
	return m.statsCmd(lbID, true, false)
}

func (m Model) statsCmd(lbID string, refresh, automatic bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		stats, err := b.LBStats(ctx, lbID)
		return statsMsg{lbID: lbID, stats: stats, sampledAt: m.clock(), refresh: refresh, automatic: automatic, err: err}
	}
}

// flashCmd clears the status flash after a short delay. The token guards against
// a stale timer clearing a newer flash.
func flashCmd(token int) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return flashClearMsg{token: token}
	})
}
