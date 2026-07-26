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
	if m.home {
		return m.onHomeKey(msg)
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
		m.moveCursor(-m.listContentRows())
	case key.Matches(msg, m.keys.PageDown):
		m.moveCursor(m.listContentRows())
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
		return m.goWorkspaceRoot()
	case key.Matches(msg, m.keys.TopLevel):
		return m.goTopLevel(msg.String())
	case key.Matches(msg, m.keys.Area):
		return m.goArea(msg.String())
	case key.Matches(msg, m.keys.Switcher):
		return m.openSwitcher()
	case key.Matches(msg, m.keys.HomeView):
		m.home = true
		return m, nil
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

	case key.Matches(msg, m.keys.Filter):
		if !hasFilterableEntries(m.allEntries) {
			return m, nil
		}
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case key.Matches(msg, m.keys.Status):
		if !hasStatusEntries(m.allEntries) {
			return m, nil
		}
		m.status = m.status.next()
		m.cursor = 0
		m.applyFilters()
		cmd := m.setFlash("status filter: "+m.status.String(), false)
		return m, cmd
	case key.Matches(msg, m.keys.ShowIDs):
		if !m.loc.isTopLevelList() {
			return m, nil
		}
		return m.toggleIDs()
	case key.Matches(msg, m.keys.Sort):
		return m.openSort()

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
	case key.Matches(msg, m.keys.Telemetry):
		return m.openTelemetry()
	case key.Matches(msg, m.keys.Token):
		return m.openToken()
	case key.Matches(msg, m.keys.Help):
		m.overlay = overlayHelp
		m.setupHelpViewport()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m.quitOrBack()
	}
	return m, nil
}

// toggleIDs flips top-level tables between name and ID columns. It is a
// pure presentation switch — the underlying entries, cursor, and filters are
// untouched — so it takes effect the moment the list is shown.
func (m Model) toggleIDs() (tea.Model, tea.Cmd) {
	m.showIDs = !m.showIDs
	mode := "names"
	if m.showIDs {
		mode = "IDs"
	}
	return m, m.setFlash("list: showing "+mode, false)
}

// --- navigation actions ---------------------------------------------------

func (m Model) goBack() (tea.Model, tea.Cmd) {
	m.saveHistoryPosition()
	if e, ok := m.hist.back(); ok {
		m.prepareHistoryPosition(e)
		m.clearFilter()
		cmd := m.showIdentity(e.id)
		return m, cmd
	}
	return m, nil
}

func (m Model) goForward() (tea.Model, tea.Cmd) {
	m.saveHistoryPosition()
	if e, ok := m.hist.forward(); ok {
		m.prepareHistoryPosition(e)
		m.clearFilter()
		cmd := m.showIdentity(e.id)
		return m, cmd
	}
	return m, nil
}

func (m Model) goWorkspaceRoot() (tea.Model, tea.Cmd) {
	m.saveHistoryPosition()
	e, ok := m.hist.toRoot()
	if !ok {
		return m, nil
	}
	m.prepareHistoryPosition(e)
	m.clearFilter()
	cmd := m.render()
	return m, cmd
}

// goTopLevel switches persistent workspaces within the active area without
// adding either root to a navigation stack. The digit indexes the active area's
// views (1-based), so the same number selects a different view per area. Pressing
// the active workspace's key is a no-op; ctrl+home returns to its root.
func (m Model) goTopLevel(digit string) (tea.Model, tea.Cmd) {
	if len(digit) != 1 || digit[0] < '1' || digit[0] > '9' {
		return m, nil
	}
	views := viewsInArea(areaOf(m.activeWorkspace))
	idx := int(digit[0] - '1')
	if idx >= len(views) {
		return m, nil
	}
	return m.switchWorkspace(views[idx])
}

// goArea switches to another area via its uppercase accelerator, restoring the
// view last active there (or the area's first view on first entry).
func (m Model) goArea(key string) (tea.Model, tea.Cmd) {
	if len(key) != 1 {
		return m, nil
	}
	area, ok := areaByKey(rune(key[0]))
	if !ok || len(area.views) == 0 {
		return m, nil
	}
	target := area.views[0]
	if last, ok := m.areaLastView[area.kind]; ok {
		target = last
	}
	return m.switchWorkspace(target)
}

// switchWorkspace projects the target workspace onto the flat Model fields. A
// no-op when it is already active.
func (m Model) switchWorkspace(target listKind) (tea.Model, tea.Cmd) {
	m.home = false // any deliberate view switch leaves the overview landing
	if target == m.activeWorkspace {
		return m, nil
	}
	m.saveWorkspaceState()
	m.restoreWorkspaceState(target)
	m.prepareWorkspacePosition()
	m.loading = false
	m.loadingWhat = ""
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

// refreshAssignmentsCmd drops an identity object's cached role assignments and
// returns the command to re-fetch them, used by a manual refresh on a user,
// group, project, or domain detail.
func (m *Model) refreshAssignmentsCmd(owner ownerKind, id string) tea.Cmd {
	key := assignmentKey{owner: owner, id: id}
	delete(m.assignments, key)
	m.assignmentsLoaded[key] = false
	m.assignmentsLoading[key] = true
	return m.loadAssignmentsCmd(key)
}

func (m Model) beginRefresh(automatic bool) (Model, tea.Cmd) {
	if m.refreshing {
		return m, nil
	}
	if automatic && (m.isCOEClusterOverview() || m.isKubernetesServiceOverview()) {
		return m, m.ensureCOEClustersCmd(false)
	}
	// An identity detail's only refreshable data is its related list (a group's
	// members, a user's groups); reload it directly rather than through the
	// load-balancer refresh transaction. These don't auto-refresh — that would
	// repeatedly re-list on every tick.
	if m.isGroupOverview() && m.loc.node != nil {
		if automatic {
			return m, nil
		}
		gid := m.loc.node.ID
		delete(m.groupMembers, gid)
		m.groupMembersLoaded[gid] = false
		m.groupMembersLoading[gid] = true
		return m, tea.Batch(m.loadGroupMembersCmd(gid), m.refreshAssignmentsCmd(ownerGroup, gid))
	}
	if m.isUserOverview() && m.loc.node != nil {
		if automatic {
			return m, nil
		}
		uid := m.loc.node.ID
		delete(m.userGroups, uid)
		m.userGroupsLoaded[uid] = false
		m.userGroupsLoading[uid] = true
		return m, tea.Batch(m.loadUserGroupsCmd(uid), m.refreshAssignmentsCmd(ownerUser, uid))
	}
	if m.isProjectOverview() && m.loc.node != nil {
		if automatic {
			return m, nil
		}
		return m, m.refreshAssignmentsCmd(ownerProject, m.loc.node.ID)
	}
	if m.isDomainOverview() && m.loc.node != nil {
		if automatic {
			return m, nil
		}
		did := m.loc.node.ID
		delete(m.domainContents, did)
		m.domainContentsLoaded[did] = false
		m.domainContentsLoading[did] = true
		return m, tea.Batch(m.loadDomainContentsCmd(did), m.refreshAssignmentsCmd(ownerDomain, did))
	}
	if m.isRoleOverview() && m.loc.node != nil {
		if automatic {
			return m, nil
		}
		rid := m.loc.node.ID
		delete(m.roleRelations, rid)
		m.roleRelationsLoaded[rid] = false
		m.roleRelationsLoading[rid] = true
		return m, m.loadRoleRelationsCmd(rid)
	}
	if (m.isServiceOverview() || m.isRegionOverview()) && m.loc.node != nil {
		if automatic {
			return m, nil
		}
		m.endpoints, m.endpointsLoaded, m.endpointsLoading = nil, false, true
		return m, m.loadEndpointsCmd(false)
	}
	m.hist.pruneDead()
	m.refreshing = true
	m.refreshAutomatic = automatic
	m.loading, m.loadingWhat = true, "refreshing…"
	m.captureRefreshSelection()
	if m.loc.isTopLevelList() {
		m.refreshLBID = ""
		switch m.loc.listKind() {
		case kindVIP:
			m.refreshVIPLBs = nil
			m.refreshVIPFloatingIPs = nil
			m.vipFloatingIPsLoading = true
			return m, tea.Batch(m.loadLBsCmd(), m.loadVIPFloatingIPsCmd(true))
		case kindListener:
			return m, m.loadListenersCmd(true)
		case kindPool:
			return m, m.loadPoolsCmd(true)
		case kindAmphora:
			return m, m.loadAmphoraeListCmd(true)
		case kindUser:
			return m, m.loadUsersCmd(true)
		case kindDomain:
			return m, m.loadDomainsCmd(true)
		case kindGroup:
			return m, m.loadGroupsCmd(true)
		case kindProject:
			return m, m.loadProjectListCmd(true)
		case kindRole:
			return m, m.loadRolesCmd(true)
		case kindService:
			return m, m.loadServicesCmd(true)
		case kindEndpoint:
			m.endpointsLoading = true
			return m, m.loadEndpointsCmd(true)
		case kindRegion:
			return m, m.loadRegionsCmd(true)
		default:
			return m, m.loadLBsCmd()
		}
	}
	lbID := m.loc.id.OwningLBID
	if lbID == "" {
		m.endRefresh()
		return m, nil
	}
	m.refreshLBID = lbID
	m.refreshDetail = nil
	m.refreshHealthMonitor = nil
	m.refreshMonitorExpected = false
	m.refreshStats = nil
	m.refreshListenerStats = nil
	if m.loc.node != nil && m.loc.node.Type == model.TypeAmphora {
		m.lbDetailLoading[m.loc.node.ID] = true
		return m, m.refreshDetailCmd(m.loc.node)
	}
	return m, m.refreshTreeCmd(lbID, m.loc.id)
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
	if e.kind == entAmphora && e.node != nil {
		m.saveHistoryPosition()
		m.hist.navigate(histEntry{id: e.node.Identity()})
		m.clearFilter()
		return m.showAmphora(e.node)
	}

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
	m.saveHistoryPosition()
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
		if m.loc.node.Type == model.TypeCOECluster && m.loc.node.Attrs["uuid"] != "" {
			return m.loc.node.Attrs["uuid"], m.loc.node.Name
		}
		return m.loc.node.ID, m.loc.node.Name
	}
	if m.loc.isTopLevelList() && m.cursor >= 0 && m.cursor < len(m.entries) {
		switch e := m.entries[m.cursor]; e.kind {
		case entLB:
			return e.lb.ID, e.lb.Name
		case entListener:
			return e.listener.ID, e.listener.Name
		case entPool:
			return e.pool.ID, e.pool.Name
		case entVIP:
			return e.vip.portID, e.vip.address
		case entAmphora:
			return e.node.ID, ""
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

// copyRaw copies the object displayed in the raw YAML/JSON overlay.
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
			return m.resumeAutoRefreshAfterPause()
		case key.Matches(msg, m.keys.CopyRaw):
			cmd := m.copyRaw()
			return m, cmd
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd

	case overlayProject:
		return m.onProjectKey(msg)
	case overlaySwitcher:
		return m.onSwitcherKey(msg)
	case overlayPicker:
		return m.onPickerKey(msg)
	case overlaySort:
		return m.onSortKey(msg)
	case overlayTelemetry:
		return m.onTelemetryKey(msg)

	case overlayToken:
		switch {
		case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Token), key.Matches(msg, m.keys.Quit):
			m.overlay = overlayNone
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

// openSort opens the sort-column picker for the active top-level list, with the
// current sort column pre-selected. Other views have no sortable columns, so it
// flashes a hint instead of opening an empty overlay.
func (m Model) openSort() (tea.Model, tea.Cmd) {
	cols := m.sortColumns()
	if len(cols) == 0 {
		return m, m.setFlash("sorting is available in the list views", false)
	}
	m.overlay = overlaySort
	m.sortCursor = 0
	for i, c := range cols {
		if c.key == m.sortKey {
			m.sortCursor = i
			break
		}
	}
	return m, nil
}

// onSortKey drives the sort-column picker: arrows/page/home/end move, enter
// applies the highlighted column (always ascending) and re-sorts in place, esc
// cancels without changing the current sort.
func (m Model) onSortKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cols := m.sortColumns()
	last := len(cols) - 1
	switch {
	case key.Matches(msg, m.keys.Cancel):
		m.overlay = overlayNone
		return m, nil
	case key.Matches(msg, m.keys.Up):
		if m.sortCursor > 0 {
			m.sortCursor--
		}
	case key.Matches(msg, m.keys.Down):
		if m.sortCursor < last {
			m.sortCursor++
		}
	case key.Matches(msg, m.keys.PageUp):
		if m.sortCursor -= m.pickerPageSize(); m.sortCursor < 0 {
			m.sortCursor = 0
		}
	case key.Matches(msg, m.keys.PageDown):
		if m.sortCursor += m.pickerPageSize(); m.sortCursor > last {
			m.sortCursor = last
		}
	case key.Matches(msg, m.keys.Home):
		m.sortCursor = 0
	case key.Matches(msg, m.keys.End):
		m.sortCursor = last
	case key.Matches(msg, m.keys.Accept):
		if m.sortCursor >= 0 && m.sortCursor <= last {
			m.sortKey = cols[m.sortCursor].key
			m.applyFilters() // re-sort; selection follows the object by identity
		}
		m.overlay = overlayNone
		return m, nil
	}
	return m, nil
}

// openSwitcher opens the area/view switcher overlay, pre-selecting the current
// view. Rows come from the areas table, so it needs no backend call.
func (m Model) openSwitcher() (tea.Model, tea.Cmd) {
	m.overlay = overlaySwitcher
	m.search.Prompt = "filter: "
	m.search.PromptStyle = m.st.filterPrompt
	m.search.SetValue("")
	m.search.Blur()
	m.switchCursor = 0
	for i, r := range m.filteredSwitcherRows() {
		if r.view == m.activeWorkspace {
			m.switchCursor = i
			break
		}
	}
	return m, nil
}

// onSwitcherKey drives the area/view switcher: it mirrors the project switcher's
// modal filtering (/ edits, enter applies, esc clears then closes) and reuses the
// list navigation keys. Enter jumps to the highlighted area+view.
func (m Model) onSwitcherKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.search.Focused() {
		switch {
		case key.Matches(msg, m.keys.Cancel):
			m.search.SetValue("")
			m.search.Blur()
			m.switchCursor = 0
			return m, nil
		case key.Matches(msg, m.keys.Accept):
			m.search.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.switchCursor = 0
		return m, cmd
	}

	rows := m.filteredSwitcherRows()
	last := len(rows) - 1
	switch {
	case key.Matches(msg, m.keys.Cancel):
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.switchCursor = 0
			return m, nil
		}
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Filter):
		m.search.Focus()
		return m, textinput.Blink
	case key.Matches(msg, m.keys.Area):
		// Uppercase accelerators jump straight to their area, exactly as they do
		// from the main list — the switcher is just another place they work.
		m.overlay = overlayNone
		m.search.Blur()
		return m.goArea(msg.String())
	case key.Matches(msg, m.keys.Up):
		if m.switchCursor > 0 {
			m.switchCursor--
		}
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.switchCursor < last {
			m.switchCursor++
		}
		return m, nil
	case key.Matches(msg, m.keys.PageUp):
		if m.switchCursor -= m.pickerPageSize(); m.switchCursor < 0 {
			m.switchCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		if m.switchCursor += m.pickerPageSize(); m.switchCursor > last {
			m.switchCursor = last
		}
		if m.switchCursor < 0 {
			m.switchCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.switchCursor = 0
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.switchCursor = last
		if m.switchCursor < 0 {
			m.switchCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Accept):
		if m.switchCursor < 0 || m.switchCursor > last {
			return m, nil
		}
		target := rows[m.switchCursor].view
		m.overlay = overlayNone
		m.search.Blur()
		return m.switchWorkspace(target)
	}
	return m, nil
}

func (m Model) openProject() (tea.Model, tea.Cmd) {
	m.overlay = overlayProject
	m.search.Prompt = "filter: "
	m.search.PromptStyle = m.st.filterPrompt
	m.search.SetValue("")
	m.search.Blur()
	if !m.backend.SwitchCapability().CanSwitch {
		m.projects = nil
		return m, nil
	}
	m.loading, m.loadingWhat = true, "projects"
	return m, m.loadProjectsCmd()
}

func (m Model) selectAllProjects() (tea.Model, tea.Cmd) {
	if !m.allProjectsSelectable() {
		return m, nil
	}
	m.overlay = overlayNone
	m.search.Blur()
	m.loading, m.loadingWhat = true, "all projects"
	return m, m.enterAllProjectsCmd()
}

func (m Model) onProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.backend.SwitchCapability().CanSwitch {
		if key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Quit) {
			m.overlay = overlayNone
		}
		return m, nil
	}
	// Project filtering follows the same modal behavior as the main list: slash
	// starts editing, Enter keeps the query, and Esc clears it. Navigation keys
	// continue to operate on the project list whenever the input is inactive.
	if m.search.Focused() {
		switch {
		case key.Matches(msg, m.keys.Cancel):
			m.search.SetValue("")
			m.search.Blur()
			m.projCursor = m.firstProjectCursor()
			return m, nil
		case key.Matches(msg, m.keys.Accept):
			m.search.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.projCursor = m.firstProjectCursor()
		return m, cmd
	}

	fp := m.filteredProjects()
	hasAllProjects := m.hasAllProjectsRow()
	total := len(fp)
	if hasAllProjects {
		total++
	}
	switch {
	case key.Matches(msg, m.keys.Cancel):
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.projCursor = m.firstProjectCursor()
			return m, nil
		}
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Filter):
		m.search.Focus()
		return m, textinput.Blink
	case key.Matches(msg, m.keys.ProjectAll):
		return m.selectAllProjects()
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
	case key.Matches(msg, m.keys.PageUp):
		m.projCursor -= m.projectPageSize()
		if m.projCursor < 0 {
			m.projCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		m.projCursor += m.projectPageSize()
		if m.projCursor >= total {
			m.projCursor = total - 1
		}
		if m.projCursor < 0 {
			m.projCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.projCursor = 0
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.projCursor = total - 1
		if m.projCursor < 0 {
			m.projCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Accept):
		idx := m.projCursor
		if hasAllProjects {
			if m.projCursor == 0 {
				return m.selectAllProjects()
			}
			idx--
		}
		if idx < 0 || idx >= len(fp) {
			return m, nil
		}
		m.overlay = overlayNone
		m.search.Blur()
		m.loading, m.loadingWhat = true, "switching project"
		return m, m.switchProjectCmd(fp[idx])
	}
	return m, nil
}

func (m Model) openPicker() (tea.Model, tea.Cmd) {
	if m.hist.empty() {
		return m, nil
	}
	m.overlay = overlayPicker
	m.search.Prompt = "search: "
	m.search.PromptStyle = m.st.normal
	m.search.SetValue("")
	m.pickCursor = 0
	for i, item := range m.pickerItems() {
		if item.current {
			m.pickCursor = i
			break
		}
	}
	m.search.Blur()
	return m, nil
}

func (m Model) onPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// History search follows the same modal behavior as the main list filter:
	// slash starts editing, Enter keeps the query, and Esc clears it. While the
	// input is inactive, ordinary key presses must not silently change it or
	// reset the history cursor.
	if m.search.Focused() {
		switch {
		case key.Matches(msg, m.keys.Cancel):
			m.search.SetValue("")
			m.search.Blur()
			m.pickCursor = 0
			return m, nil
		case key.Matches(msg, m.keys.Accept):
			m.search.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.pickCursor = 0
		return m, cmd
	}

	items := m.pickerItems()
	switch {
	case key.Matches(msg, m.keys.Cancel):
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.pickCursor = 0
			return m, nil
		}
		m.overlay = overlayNone
		m.search.Blur()
		return m, nil
	case key.Matches(msg, m.keys.Filter):
		m.search.Focus()
		return m, textinput.Blink
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
	case key.Matches(msg, m.keys.PageUp):
		m.pickCursor -= m.pickerPageSize()
		if m.pickCursor < 0 {
			m.pickCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		m.pickCursor += m.pickerPageSize()
		if m.pickCursor >= len(items) {
			m.pickCursor = len(items) - 1
		}
		if m.pickCursor < 0 {
			m.pickCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.pickCursor = 0
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.pickCursor = len(items) - 1
		if m.pickCursor < 0 {
			m.pickCursor = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Accept):
		if len(items) == 0 {
			return m, nil
		}
		idx := items[m.pickCursor].index
		m.overlay = overlayNone
		m.search.Blur()
		m.saveHistoryPosition()
		if e, ok := m.hist.moveTo(idx); ok {
			m.prepareHistoryPosition(e)
			m.clearFilter()
			cmd := m.showIdentity(e.id)
			return m, cmd
		}
		return m, nil
	}
	return m, nil
}
