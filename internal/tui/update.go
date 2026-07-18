package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/cache"
	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// Update is the central event loop. All shared-graph mutation happens here on
// the single UI goroutine; commands only fetch and return data.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = msg.Width - 4
		m.search.Width = msg.Width - 12
		m.vp.Width = msg.Width
		m.vp.Height = m.bodyHeight()
		m.ensureVisible()
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case lbsMsg:
		return m.onLBs(msg)
	case treeMsg:
		return m.onTree(msg)
	case detailMsg:
		return m.onDetail(msg)
	case statsMsg:
		return m.onStats(msg)
	case refResolveMsg:
		return m.onRefResolve(msg)
	case amphoraeMsg:
		return m.onAmphorae(msg)
	case projectsMsg:
		return m.onProjects(msg)
	case switchedMsg:
		return m.onSwitched(msg)
	case flashClearMsg:
		if msg.token == m.flashToken {
			m.flash, m.flashErr = "", false
		}
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// --- message handlers -----------------------------------------------------

func (m Model) onLBs(msg lbsMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		return m, m.setFlash("list load balancers: "+msg.err.Error(), true)
	}
	m.lbs = msg.lbs
	m.lbsLoaded = true
	if cur, ok := m.hist.current(); ok && cur.id.IsLBList() {
		m.setLBLocation()
	}
	return m, nil
}

func (m Model) onTree(msg treeMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	cur, ok := m.hist.current()
	if msg.err != nil {
		if osclient.IsNotFound(msg.err) && ok && cur.id.OwningLBID == msg.lbID {
			// The whole LB is gone: mark this history entry dead and show it.
			m.hist.markDead()
			m.loc = location{id: cur.id, dead: true}
			m.allEntries = nil
			m.applyFilters()
			return m, m.setFlash("this object was deleted since you last viewed it", true)
		}
		return m, m.setFlash("load tree: "+msg.err.Error(), true)
	}
	m.cache.Put(msg.lbID, msg.tree)
	if ok && cur.id.OwningLBID == msg.lbID {
		m.buildNodeLocation(cur.id, msg.tree)
	}
	return m, nil
}

func (m Model) onDetail(msg detailMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		return m, m.setFlash("load detail: "+msg.err.Error(), true)
	}
	node := m.treeNode(msg.nodeID)
	if node == nil {
		return m, nil
	}
	// Apply the fetched detail on the UI goroutine.
	node.Raw = msg.res.Raw
	node.DetailLoaded = true
	for k, v := range msg.res.Attrs {
		node.SetAttr(k, v)
	}
	if m.loc.tree != nil {
		if msg.res.IsListener {
			m.loc.tree.ResolveListenerDefaultPool(node.ID, msg.res.ListenerDefaultPoolID)
		}
		if msg.res.IsL7Policy {
			m.loc.tree.ResolveL7PolicyRedirect(node.ID, msg.res.L7Action, msg.res.L7RedirectPoolID)
		}
	}
	node.RefsResolved = true
	// Newly-resolved reference edges can add rows to the current view.
	if m.loc.node == node {
		m.allEntries = nodeEntries(node)
		m.applyFilters()
	}
	return m, m.openInspect(node, msg.intent)
}

func (m Model) onStats(msg statsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil // stats are a nicety; failure is silent
	}
	m.lbStats[msg.lbID] = msg.stats
	if m.overlay == overlayDetail && m.loc.node != nil && m.loc.node.ID == msg.lbID {
		m.refreshDetailOverlay()
	}
	return m, nil
}

func (m Model) onRefResolve(msg refResolveMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		if errors.Is(msg.err, osclient.ErrUnavailable) {
			return m, m.setFlash(msg.label+" lookup is unavailable in this cloud/scope", true)
		}
		return m, m.setFlash("resolve "+msg.label+": "+msg.err.Error(), true)
	}
	if m.loc.tree == nil {
		return m, nil
	}
	src := m.loc.tree.Node(msg.sourceID)
	if src == nil {
		src = m.loc.node
	}
	if msg.node == nil {
		// A genuine "no such boundary object" (e.g. an internal LB with no
		// floating IP) — mark the edge missing so it stops inviting a lookup.
		if src != nil {
			src.ResolveEdge(msg.label, nil)
			if m.loc.node == src {
				m.allEntries = nodeEntries(src)
				m.applyFilters()
			}
		}
		return m, m.setFlash("no "+msg.label+" associated with this object", false)
	}
	if src != nil {
		msg.node.OwningLBID = src.OwningLBID
	}
	m.loc.tree.Attach(msg.node)
	if src != nil {
		src.ResolveEdge(msg.label, msg.node)
	}
	m.hist.navigate(histEntry{id: msg.node.Identity(), viaRef: true})
	m.clearFilter()
	return m, m.render()
}

func (m Model) onAmphorae(msg amphoraeMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		if errors.Is(msg.err, osclient.ErrAdminRequired) {
			return m, m.setFlash("amphora enumeration requires admin RBAC — unavailable with tenant scope", false)
		}
		return m, m.setFlash("list amphorae: "+msg.err.Error(), true)
	}
	if m.loc.tree == nil {
		return m, nil
	}
	placeholder := m.loc.tree.Node(msg.placeholderID)
	if placeholder == nil {
		return m, nil
	}
	if len(placeholder.Children) == 0 {
		for _, a := range msg.nodes {
			a.Parent = placeholder
			placeholder.Children = append(placeholder.Children, a)
			m.loc.tree.Attach(a)
		}
	}
	m.hist.navigate(histEntry{id: placeholder.Identity()})
	m.clearFilter()
	return m, m.render()
}

func (m Model) onProjects(msg projectsMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.overlay = overlayNone
		return m, m.setFlash(msg.err.Error(), true)
	}
	m.projects = msg.projects
	m.projCursor = 0
	m.overlay = overlayProject
	m.search.SetValue("")
	m.search.Focus()
	return m, textinput.Blink
}

func (m Model) onSwitched(msg switchedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		return m, m.setFlash(msg.err.Error(), true)
	}
	// A new scope means a different object set: drop caches and history.
	m.project = msg.project
	m.cache = cache.New(m.cfg.CacheSize, m.cfg.CacheTTL)
	m.lbStats = map[string]map[string]any{}
	m.lbs, m.lbsLoaded = nil, false
	m.hist = newHistory(m.cfg.HistoryCap)
	m.hist.navigate(histEntry{id: model.LBListIdentity})
	m.loc = location{id: model.LBListIdentity}
	m.overlay = overlayNone
	m.clearFilter()
	m.loading = true
	m.loadingWhat = "load balancers"
	return m, tea.Batch(m.loadLBsCmd(), m.setFlash("switched to project "+projectLabel(msg.project), false))
}

// --- navigation & rendering ----------------------------------------------

// render resolves the current history entry into a location, fetching if needed.
func (m *Model) render() tea.Cmd {
	cur, ok := m.hist.current()
	if !ok {
		return nil
	}
	return m.showIdentity(cur.id)
}

func (m *Model) showIdentity(id model.Identity) tea.Cmd {
	if id.IsLBList() {
		m.loc = location{id: id}
		if m.lbsLoaded {
			m.setLBLocation()
			return nil
		}
		m.loading, m.loadingWhat = true, "load balancers"
		return m.loadLBsCmd()
	}
	entry, fresh := m.cache.Get(id.OwningLBID)
	if entry.Tree != nil && fresh {
		m.buildNodeLocation(id, entry.Tree)
		return nil
	}
	m.loading, m.loadingWhat = true, "tree"
	return m.getTreeCmd(id.OwningLBID, id, false)
}

func (m *Model) setLBLocation() {
	m.loc = location{id: model.LBListIdentity}
	m.allEntries = lbEntries(m.lbs)
	m.cursor, m.top = 0, 0
	m.applyFilters()
}

func (m *Model) buildNodeLocation(id model.Identity, tree *model.Tree) {
	node := tree.Node(id.ID)
	if node == nil {
		m.loc = location{id: id, tree: tree, dead: true}
		m.allEntries = nil
		m.hist.markDead()
		m.applyFilters()
		return
	}
	m.loc = location{id: id, node: node, tree: tree}
	m.allEntries = nodeEntries(node)
	m.rawContent, m.rawFormat = "", ""
	m.cursor, m.top = 0, 0
	m.applyFilters()
}

// applyFilters recomputes the visible rows from the substring filter and the
// status filter, then clamps the cursor.
func (m *Model) applyFilters() {
	f := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var res []entry
	for _, e := range m.allEntries {
		if !m.status.match(e.oper, e.prov) {
			continue
		}
		if f != "" && !strings.Contains(e.filterText(), f) {
			continue
		}
		res = append(res, e)
	}
	m.entries = res
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureVisible()
}

func (m *Model) ensureVisible() {
	h := m.bodyHeight()
	if h < 1 {
		h = 1
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

// treeNode finds a node by ID in the current location's tree.
func (m *Model) treeNode(id string) *model.Node {
	if m.loc.tree == nil {
		return nil
	}
	return m.loc.tree.Node(id)
}

// setFlash sets the transient status line and schedules its clearing.
func (m *Model) setFlash(text string, isErr bool) tea.Cmd {
	m.flash, m.flashErr = text, isErr
	m.flashToken++
	return flashCmd(m.flashToken)
}

func (m *Model) clearFilter() {
	m.filter.SetValue("")
	m.filter.Blur()
	m.filtering = false
}

func projectLabel(p osclient.ProjectInfo) string {
	if p.Name != "" {
		return p.Name
	}
	if p.ID != "" {
		return p.ID
	}
	return "(unknown)"
}
