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

// detailIntent is what to do once a node's detail has loaded.
type detailIntent int

const (
	intentDetail detailIntent = iota
	intentYAML
	intentJSON
)

type detailMsg struct {
	nodeID string
	res    osclient.DetailResult
	intent detailIntent
	err    error
}

type refResolveMsg struct {
	sourceID string // node whose unresolved edge we followed
	label    string // edge label (e.g. "floating IP", "instance")
	node     *model.Node
	err      error
}

type amphoraeMsg struct {
	placeholderID string
	lbID          string
	nodes         []*model.Node
	err           error
}

type projectsMsg struct {
	projects []osclient.ProjectInfo
	err      error
}

type switchedMsg struct {
	project osclient.ProjectInfo
	err     error
}

type statsMsg struct {
	lbID  string
	stats map[string]any
	err   error
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
	b := m.backend
	id := n.ID
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		res, err := b.FetchDetail(ctx, n)
		return detailMsg{nodeID: id, res: res, intent: intent, err: err}
	}
}

func (m Model) resolveFloatingIPCmd(source *model.Node, portID string) tea.Cmd {
	b := m.backend
	sid := source.ID
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		node, err := b.ResolveFloatingIP(ctx, portID)
		return refResolveMsg{sourceID: sid, label: "floating IP", node: node, err: err}
	}
}

func (m Model) resolveInstanceCmd(source *model.Node, address string) tea.Cmd {
	b := m.backend
	sid := source.ID
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		node, err := b.ResolveInstance(ctx, address)
		return refResolveMsg{sourceID: sid, label: "instance", node: node, err: err}
	}
}

func (m Model) loadAmphoraeCmd(placeholder *model.Node, lbID string) tea.Cmd {
	b := m.backend
	pid := placeholder.ID
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ListAmphorae(ctx, lbID)
		return amphoraeMsg{placeholderID: pid, lbID: lbID, nodes: nodes, err: err}
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
		return switchedMsg{project: b.CurrentProject(), err: err}
	}
}

func (m Model) lbStatsCmd(lbID string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		stats, err := b.LBStats(ctx, lbID)
		return statsMsg{lbID: lbID, stats: stats, err: err}
	}
}

// flashCmd clears the status flash after a short delay. The token guards against
// a stale timer clearing a newer flash.
func flashCmd(token int) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return flashClearMsg{token: token}
	})
}
