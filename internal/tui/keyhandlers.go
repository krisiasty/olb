package tui

import (
	"os"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/clipboard"
	"github.com/krisiasty/olb/internal/model"
)

// onKey routes a key press by context: filter input, overlay, or the main list.
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Force) {
		m.quitting = true
		return m, tea.Quit
	}
	if m.filtering {
		return m.onFilterKey(msg)
	}
	if m.overlay != overlayNone {
		return m.onOverlayKey(msg)
	}
	return m.onListKey(msg)
}

func (m Model) onFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel): // esc clears the filter
		m.clearFilter()
		m.applyFilters()
		return m, nil
	case key.Matches(msg, m.keys.Accept): // enter keeps the filter, leaves the input
		m.filtering = false
		m.filter.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.cursor = 0
	m.applyFilters()
	return m, cmd
}

func (m Model) onListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.moveCursor(-1)
	case key.Matches(msg, m.keys.Down):
		m.moveCursor(1)
	case key.Matches(msg, m.keys.PageUp):
		m.moveCursor(-m.visibleRows())
	case key.Matches(msg, m.keys.PageDown):
		m.moveCursor(m.visibleRows())
	case key.Matches(msg, m.keys.Home):
		if first := firstSelectableIndex(m.entries); first >= 0 {
			m.cursor = first
		} else {
			m.cursor = 0
		}
		m.ensureVisible()
	case key.Matches(msg, m.keys.End):
		if last := lastSelectableIndex(m.entries); last >= 0 {
			m.cursor = last
		} else {
			m.cursor = 0
		}
		m.ensureVisible()

	case key.Matches(msg, m.keys.Open):
		cmd := m.openSelected()
		return m, cmd

	case msg.String() == "esc":
		// esc is contextual: clear an active filter, otherwise go back.
		if m.filter.Value() != "" {
			m.clearFilter()
			m.applyFilters()
			return m, nil
		}
		return m.goBack()
	case key.Matches(msg, m.keys.Forward):
		return m.goForward()
	case key.Matches(msg, m.keys.Back): // left / backspace
		return m.goBack()
	case key.Matches(msg, m.keys.LBList):
		return m.goLBList()
	case key.Matches(msg, m.keys.Picker):
		return m.openPicker()

	case key.Matches(msg, m.keys.YAML):
		cmd := m.inspect(intentYAML)
		return m, cmd
	case key.Matches(msg, m.keys.JSON):
		cmd := m.inspect(intentJSON)
		return m, cmd
	case key.Matches(msg, m.keys.CopyID):
		cmd := m.copyID()
		return m, cmd
	case key.Matches(msg, m.keys.CopyNm):
		cmd := m.copyName()
		return m, cmd
	case key.Matches(msg, m.keys.CopyRaw):
		cmd := m.copyRaw()
		return m, cmd

	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case key.Matches(msg, m.keys.Status):
		m.status = m.status.next()
		m.cursor = 0
		m.applyFilters()
		cmd := m.setFlash("status filter: "+m.status.String(), false)
		return m, cmd

	case key.Matches(msg, m.keys.Project):
		return m.openProject()
	case key.Matches(msg, m.keys.Refresh):
		return m.refresh()
	case key.Matches(msg, m.keys.AutoRefresh):
		return m.toggleAutoRefresh()
	case key.Matches(msg, m.keys.IntervalUp):
		return m.changeAutoRefreshInterval(1)
	case key.Matches(msg, m.keys.IntervalDown):
		return m.changeAutoRefreshInterval(-1)
	case key.Matches(msg, m.keys.Help):
		m.overlay = overlayHelp
		m.setupHelpViewport()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m.quitOrBack()
	}
	return m, nil
}

// --- navigation actions ---------------------------------------------------

func (m Model) goBack() (tea.Model, tea.Cmd) {
	if e, ok := m.hist.back(); ok {
		m.clearFilter()
		cmd := m.showIdentity(e.id)
		return m, cmd
	}
	return m, nil
}

func (m Model) goForward() (tea.Model, tea.Cmd) {
	if e, ok := m.hist.forward(); ok {
		m.clearFilter()
		cmd := m.showIdentity(e.id)
		return m, cmd
	}
	return m, nil
}

func (m Model) goLBList() (tea.Model, tea.Cmd) {
	m.hist.navigate(histEntry{id: model.LBListIdentity})
	m.clearFilter()
	cmd := m.render()
	return m, cmd
}

func (m Model) quitOrBack() (tea.Model, tea.Cmd) {
	if m.hist.canBack() {
		return m.goBack()
	}
	m.quitting = true
	return m, tea.Quit
}

func (m Model) refresh() (tea.Model, tea.Cmd) {
	next, cmd := m.beginRefresh(false)
	return next, cmd
}

func (m Model) beginRefresh(automatic bool) (Model, tea.Cmd) {
	if m.refreshing {
		return m, nil
	}
	m.hist.pruneDead()
	m.refreshing = true
	m.refreshAutomatic = automatic
	m.loading, m.loadingWhat = true, "refreshing…"
	m.captureRefreshSelection()
	if m.loc.isList() {
		m.refreshLBID = ""
		return m, m.loadLBsCmd()
	}
	lbID := m.loc.id.OwningLBID
	if lbID == "" {
		m.endRefresh()
		return m, nil
	}
	m.refreshLBID = lbID
	m.refreshDetail = nil
	m.refreshStats = nil
	return m, m.getTreeCmd(lbID, m.loc.id, false)
}

func (m *Model) moveCursor(delta int) {
	if len(m.entries) == 0 || delta == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 || m.cursor >= len(m.entries) || !m.entries[m.cursor].selectable() {
		m.cursor = nearestSelectableIndex(m.entries, m.cursor)
	}
	if m.cursor < 0 {
		m.cursor = 0
		return
	}
	target := m.cursor + delta
	if target < 0 {
		target = 0
	}
	if target >= len(m.entries) {
		target = len(m.entries) - 1
	}
	direction := 1
	if delta < 0 {
		direction = -1
	}
	for i := target; i >= 0 && i < len(m.entries); i += direction {
		if m.entries[i].selectable() {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
	for i := target - direction; i >= 0 && i < len(m.entries); i -= direction {
		if m.entries[i].selectable() {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

// openSelected acts on the highlighted row — new navigation into a containment
// child or along a reference edge, resolving lazily where needed.
func (m *Model) openSelected() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.entries) || !m.entries[m.cursor].selectable() {
		return nil
	}
	e := m.entries[m.cursor]

	if e.kind == entRef && e.edge != nil && e.edge.Unresolved {
		return m.followUnresolved(e)
	}
	if (e.kind == entRef || e.kind == entBackRef) && e.edge != nil && e.edge.Missing {
		return m.setFlash(e.relationship+" is unavailable", false)
	}

	id, viaRef, unresolved := e.identity()
	if unresolved || (id.ID == "" && !id.IsLBList()) {
		return m.setFlash("nothing to open here", false)
	}
	m.hist.navigate(histEntry{id: id, viaRef: viaRef})
	m.clearFilter()
	return m.render()
}

func (m *Model) followUnresolved(e entry) tea.Cmd {
	src := m.loc.node
	if src == nil || e.edge == nil {
		return nil
	}
	switch e.edge.Label {
	case "floating IP":
		m.loading, m.loadingWhat = true, "floating IP"
		return m.resolveFloatingIPCmd(src, e.edge.TargetID)
	case "instance":
		m.loading, m.loadingWhat = true, "instance"
		return m.resolveInstanceCmd(src, e.edge.TargetID)
	}
	return nil
}

// --- inspect & copy -------------------------------------------------------

func (m *Model) inspect(intent detailIntent) tea.Cmd {
	node := m.loc.node
	if node == nil {
		return m.setFlash("open a load balancer to inspect it", false)
	}
	if node.DetailLoaded {
		return m.openInspect(node, intent)
	}
	if node.Type == model.TypeLoadBalancer && m.lbDetailLoading[node.ID] {
		return m.setFlash("full configuration is still loading", false)
	}
	m.loading, m.loadingWhat = true, "detail"
	return m.fetchDetailCmd(node, intent)
}

// openInspect opens the raw YAML/JSON overlay for an already-loaded node.
func (m *Model) openInspect(node *model.Node, intent detailIntent) tea.Cmd {
	switch intent {
	case intentYAML:
		m.rawContent, m.rawFormat = marshalRaw(node.Raw, "yaml"), "yaml"
		m.overlay = overlayRaw
		m.setupRawViewport()
	case intentJSON:
		m.rawContent, m.rawFormat = marshalRaw(node.Raw, "json"), "json"
		m.overlay = overlayRaw
		m.setupRawViewport()
	}
	return nil
}

func (m Model) currentIDName() (id, name string) {
	if m.loc.node != nil {
		return m.loc.node.ID, m.loc.node.Name
	}
	if m.loc.isList() && len(m.entries) > 0 {
		if e := m.entries[m.cursor]; e.kind == entLB {
			return e.lb.ID, e.lb.Name
		}
	}
	return "", ""
}

func (m *Model) copyID() tea.Cmd {
	id, _ := m.currentIDName()
	if id == "" {
		return m.setFlash("no object id to copy here", false)
	}
	return m.copyValue("id", id)
}

func (m *Model) copyName() tea.Cmd {
	_, name := m.currentIDName()
	if name == "" {
		return m.setFlash("this object has no name", false)
	}
	return m.copyValue("name", name)
}

// copyRaw copies the displayed raw object; a no-op with a hint when neither y
// nor j has been pressed for the current object.
func (m *Model) copyRaw() tea.Cmd {
	if m.rawContent == "" || m.rawFormat == "" {
		return m.setFlash("no raw object shown — press y or j first", false)
	}
	return m.copyValue("raw "+m.rawFormat, m.rawContent)
}

func (m *Model) copyValue(what, value string) tea.Cmd {
	if m.cfg.PrintMode {
		// Escape hatch for terminals without OSC 52: show the value so the user
		// can select/copy it manually.
		m.rawContent = value
		m.overlay = overlayRaw
		m.setupRawViewportTitle("copy " + what + " — select to copy (print mode)")
		return nil
	}
	out := m.cfg.Stdout
	if out == nil {
		out = os.Stdout
	}
	if err := clipboard.Emit(out, value); err != nil {
		return m.setFlash("clipboard: "+err.Error(), true)
	}
	msg := "copied " + what + " to clipboard (OSC 52)"
	if clipboard.LargePayload(value) {
		msg += " — large payload, may be truncated by some terminals"
	}
	return m.setFlash(msg, false)
}

// --- overlays -------------------------------------------------------------

func (m Model) onOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayHelp:
		switch {
		case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Help), key.Matches(msg, m.keys.Quit):
			m.overlay = overlayNone
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case overlayRaw:
		switch {
		case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Quit):
			m.overlay = overlayNone
			return m, nil
		case key.Matches(msg, m.keys.CopyRaw):
			cmd := m.copyRaw()
			return m, cmd
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case overlayProject:
		return m.onProjectKey(msg)
	case overlayPicker:
		return m.onPickerKey(msg)
	}
	return m, nil
}

func (m Model) openProject() (tea.Model, tea.Cmd) {
	m.overlay = overlayProject
	if !m.backend.SwitchCapability().CanSwitch {
		m.projects = nil
		return m, nil
	}
	m.loading, m.loadingWhat = true, "projects"
	return m, m.loadProjectsCmd()
}

func (m Model) onProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.backend.SwitchCapability().CanSwitch {
		if key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Quit) {
			m.overlay = overlayNone
		}
		return m, nil
	}
	fp := m.filteredProjects()
	// Row 0 is the synthetic "all projects" option; rows 1..N are the projects.
	total := len(fp) + 1
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if m.projCursor > 0 {
			m.projCursor--
		}
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.projCursor < total-1 {
			m.projCursor++
		}
		return m, nil
	case key.Matches(msg, m.keys.Accept):
		m.overlay = overlayNone
		m.search.Blur()
		if m.projCursor == 0 {
			m.loading, m.loadingWhat = true, "all projects"
			return m, m.enterAllProjectsCmd()
		}
		idx := m.projCursor - 1
		if idx < 0 || idx >= len(fp) {
			return m, nil
		}
		m.loading, m.loadingWhat = true, "switching project"
		return m, m.switchProjectCmd(fp[idx])
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.projCursor = 0
	return m, cmd
}

func (m Model) openPicker() (tea.Model, tea.Cmd) {
	if m.hist.empty() {
		return m, nil
	}
	m.overlay = overlayPicker
	m.pickCursor = 0
	m.search.SetValue("")
	m.search.Focus()
	return m, textinput.Blink
}

func (m Model) onPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.pickerItems()
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if m.pickCursor > 0 {
			m.pickCursor--
		}
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.pickCursor < len(items)-1 {
			m.pickCursor++
		}
		return m, nil
	case key.Matches(msg, m.keys.Accept):
		if len(items) == 0 {
			return m, nil
		}
		idx := items[m.pickCursor].index
		m.overlay = overlayNone
		m.search.Blur()
		if e, ok := m.hist.moveTo(idx); ok {
			m.clearFilter()
			cmd := m.showIdentity(e.id)
			return m, cmd
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.pickCursor = 0
	return m, cmd
}
