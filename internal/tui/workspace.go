package tui

import "github.com/krisiasty/olb/internal/model"

// workspaceState is the independently remembered navigation surface behind one
// of the 1-5 top-level views. API data and graph caches remain shared globally;
// only browser-like navigation and presentation state belong to the workspace.
type workspaceState struct {
	hist *history
	loc  location

	allEntries []entry
	entries    []entry
	cursor     int
	top        int

	filterValue string
	status      statusFilter
	rawContent  string
	rawFormat   string
}

type workspacePosition struct {
	valid       bool
	at          model.Identity
	selection   entrySelection
	selectionOK bool
	cursor      int
	top         int
}

func newWorkspaceState(kind listKind, historyCap int) workspaceState {
	hist := newHistory(historyCap)
	root := kind.identity()
	hist.root = root
	hist.navigate(histEntry{id: root})
	return workspaceState{hist: hist, loc: location{id: root}}
}

// resetWorkspaces initializes every scope-dependent navigation stack at
// startup, where the load-balancer workspace is the default.
func (m *Model) resetWorkspaces() {
	m.resetWorkspacesAt(kindLB)
}

// resetWorkspacesAt invalidates every scope-dependent navigation stack while
// keeping the selected top-level surface active. Project changes use this so a
// user switching scope from listeners returns to the listener list, rather than
// being unexpectedly sent to load balancers.
func (m *Model) resetWorkspacesAt(active listKind) {
	for _, kind := range topLevelKinds {
		m.workspaces[kind] = newWorkspaceState(kind, m.cfg.HistoryCap)
	}
	m.workspaceResume = workspacePosition{}
	m.restoreWorkspaceState(active)
}

func (m *Model) saveWorkspaceState() {
	state := &m.workspaces[m.activeWorkspace]
	state.hist = m.hist
	state.loc = m.loc
	state.allEntries = m.allEntries
	state.entries = m.entries
	state.cursor = m.cursor
	state.top = m.top
	state.filterValue = m.filter.Value()
	state.status = m.status
	state.rawContent = m.rawContent
	state.rawFormat = m.rawFormat
}

func (m *Model) restoreWorkspaceState(kind listKind) {
	state := &m.workspaces[kind]
	m.activeWorkspace = kind
	m.hist = state.hist
	m.loc = state.loc
	m.allEntries = state.allEntries
	m.entries = state.entries
	m.cursor = state.cursor
	m.top = state.top
	m.filter.SetValue(state.filterValue)
	m.filter.Blur()
	m.filtering = false
	m.status = state.status
	m.rawContent = state.rawContent
	m.rawFormat = state.rawFormat
	m.rawTitle = ""
}

// prepareWorkspacePosition captures the visible selection before render
// re-resolves the restored history entry against shared live/cache data.
func (m *Model) prepareWorkspacePosition() {
	m.workspaceResume = workspacePosition{}
	current, ok := m.hist.current()
	if !ok || !m.loc.id.Equal(current.id) {
		return
	}
	position := workspacePosition{valid: true, at: current.id, cursor: m.cursor, top: m.top}
	if m.cursor >= 0 && m.cursor < len(m.entries) && m.entries[m.cursor].selectable() {
		position.selection = m.entries[m.cursor].selection()
		position.selectionOK = true
	}
	m.workspaceResume = position
}

// saveHistoryPosition snapshots the current list selection before leaving a
// history location. The selection identity survives row reordering; cursor is
// retained as a fallback when the selected object no longer exists.
func (m *Model) saveHistoryPosition() {
	current, ok := m.hist.current()
	if !ok || !m.loc.id.Equal(current.id) {
		return
	}
	position := workspacePosition{valid: true, at: current.id, cursor: m.cursor, top: m.top}
	if m.cursor >= 0 && m.cursor < len(m.entries) && m.entries[m.cursor].selectable() {
		position.selection = m.entries[m.cursor].selection()
		position.selectionOK = true
	}
	m.hist.saveCurrentPosition(position)
}

func (m *Model) prepareHistoryPosition(entry histEntry) {
	m.workspaceResume = workspacePosition{}
	if !entry.positionSet {
		return
	}
	position := entry.position
	position.valid = true
	position.at = entry.id
	m.workspaceResume = position
}

func (m *Model) restoreWorkspacePosition() {
	position := m.workspaceResume
	if !position.valid || !m.loc.id.Equal(position.at) {
		return
	}

	selected := -1
	if position.selectionOK {
		for i := range m.entries {
			if m.entries[i].selectable() && m.entries[i].selection().equal(position.selection) {
				selected = i
				break
			}
		}
	}
	if selected < 0 && len(m.entries) > 0 {
		selected = nearestSelectableIndex(m.entries, position.cursor)
	}
	if selected >= 0 {
		m.cursor = selected
	} else {
		m.cursor = 0
	}
	m.top = position.top
	m.ensureVisible()
	m.workspaceResume = workspacePosition{}
}
