package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// bodyHeight is the content area available between the two-line header and
// two-line footer.
func (m Model) bodyHeight() int {
	h := m.height - 4
	if h < 1 {
		h = 1
	}
	return h
}

// View renders the current screen or overlay.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.height == 0 || m.width == 0 {
		return "starting olb…"
	}
	switch m.overlay {
	case overlayHelp:
		return m.helpView()
	case overlayRaw:
		return m.rawView()
	case overlayScope:
		return m.scopeView()
	case overlaySwitcher:
		return m.switcherView()
	case overlayPicker:
		return m.pickerView()
	case overlaySort:
		return m.sortView()
	case overlayTelemetry:
		return m.telemetryView()
	case overlayToken:
		return m.tokenView()
	case overlayRoleTree:
		return m.roleTreeView()
	case overlayHypervisorFeatures:
		return m.hypervisorFeaturesView()
	}
	if m.home {
		return m.homeView()
	}
	return m.listView()
}

func (m Model) listView() string {
	// Scroll hints bracket the scrolling region. For a top-level list the whole
	// body scrolls, so "▲ more" sits on the subtitle line above it. For a detail
	// overview the scrolling happens in the related sub-panel (the trailing region
	// of the body), so "▲ more" belongs on the line just above that panel — the
	// line at index len(body)-visibleRows()-1 — keeping the hint next to the list
	// it describes rather than at the top of the screen. "▼ more" always sits on
	// the flash line just below the body. On wide screens both hints mirror onto
	// the left edge too.
	above, below := m.listScrollMarkers()
	body := m.bodyLines()
	lines := make([]string, 0, m.height)
	lines = append(lines, m.breadcrumbLine())

	topOnSubtitle := true
	if !m.loc.isTopLevelList() {
		if i := len(body) - m.visibleRows() - 1; i >= 0 && i < len(body) {
			if above != "" {
				body[i] = m.edgeMarkerLine(body[i], above)
			}
			topOnSubtitle = false
		}
	}
	if topOnSubtitle {
		lines = append(lines, m.edgeMarkerLine(m.subtitleLine(), above))
	} else {
		lines = append(lines, m.clip(m.subtitleLine()))
	}
	lines = append(lines, body...)
	lines = append(lines, m.edgeMarkerLine(m.flashLine(), below))
	lines = append(lines, m.hintLine())
	return strings.Join(lines, "\n")
}

func (m Model) breadcrumbLine() string {
	trail := m.hist.trail()
	area := areaByKind(areaOf(m.activeWorkspace))
	chip := m.st.panelTitle.Render(fmt.Sprintf(" %s-%d ", string(area.key), viewNumber(m.activeWorkspace)))
	root := chip + " " + m.st.breadcrumb.Render(listKindOf(m.hist.rootIdentity()).rootLabel())
	parts := []string{root}
	for _, e := range trail {
		marker := " › "
		if e.viaRef {
			marker = " ↦ "
		}
		label := e.id.Label
		if label == "" {
			label = string(e.id.Type) + ":" + shortID(e.id.ID)
		}
		rendered := m.st.breadcrumb.Render(label)
		if e.dead {
			rendered = m.st.dead.Render(label)
		}
		parts = append(parts, m.st.crumbSep.Render(marker)+rendered)
	}
	if m.filtering {
		separator := m.st.crumbSep.Render("  ")
		fullWidth := m.width
		inputReserve := fullWidth / 2
		if inputReserve < 12 {
			inputReserve = 12
		}
		if inputReserve > 40 {
			inputReserve = 40
		}
		breadcrumbWidth := fullWidth - lipgloss.Width(separator) - inputReserve
		promptWidth := lipgloss.Width(m.filter.Prompt)
		if breadcrumbWidth < 1 {
			m.filter.Width = fullWidth - promptWidth
			if m.filter.Width < 1 {
				m.filter.Width = 1
			}
			return m.clip(m.filter.View())
		}
		m.width = breadcrumbWidth
		breadcrumb := m.fitBreadcrumb(parts)
		m.filter.Width = fullWidth - lipgloss.Width(breadcrumb) - lipgloss.Width(separator) - promptWidth
		if m.filter.Width < 1 {
			m.filter.Width = 1
		}
		line := breadcrumb + separator + m.filter.View()
		m.width = fullWidth
		return m.clip(line)
	}
	return m.fitBreadcrumb(parts)
}

// fitBreadcrumb drops the oldest path components first, preserving the
// rightmost component that identifies the object currently on screen.
func (m Model) fitBreadcrumb(parts []string) string {
	out := strings.Join(parts, "")
	if m.width <= 0 || lipgloss.Width(out) <= m.width {
		return out
	}
	prefix := m.st.crumbSep.Render("…")
	available := m.width - lipgloss.Width(prefix)
	if available <= 0 {
		return m.clip(prefix)
	}

	suffix := parts[len(parts)-1]
	if lipgloss.Width(suffix) > available {
		return prefix + lipgloss.NewStyle().MaxWidth(available).Render(suffix)
	}
	for i := len(parts) - 2; i >= 0; i-- {
		candidate := parts[i] + suffix
		if lipgloss.Width(candidate) > available {
			break
		}
		suffix = candidate
	}
	return prefix + suffix
}

func (m Model) subtitleLine() string {
	scope := m.st.statusBar.Render("scope: ") + m.st.title.Render(m.scopeText())
	parts := []string{scope, m.styledAutoRefreshLabel()}
	if restriction := m.identityListRestriction(); restriction != "" {
		parts = append(parts, m.st.statusBar.Render("showing "+restriction))
	}
	if !m.isOverview() {
		if len(m.entries) != len(m.allEntries) {
			parts = append(parts, m.st.statusBar.Render(fmt.Sprintf("%d/%d items", len(m.entries), len(m.allEntries))))
		} else {
			parts = append(parts, m.st.statusBar.Render(fmt.Sprintf("%d items", len(m.entries))))
		}
	}
	if m.status != statusAll && hasStatusEntries(m.allEntries) {
		parts = append(parts, m.st.statusBar.Render("status="+m.status.String()))
	}
	if v := m.filter.Value(); v != "" && !m.filtering && hasFilterableEntries(m.allEntries) {
		parts = append(parts, m.st.statusBar.Render("filter: "+v))
	}
	if m.loading {
		parts = append(parts, m.st.statusBar.Render(m.spinner.View()+" "+m.loadingLabel()))
	}
	return m.clip(strings.Join(parts, m.st.statusBar.Render("  ·  ")))
}

func (m Model) identityListRestriction() string {
	if !m.loc.isTopLevelList() {
		return ""
	}
	switch m.loc.listKind() {
	case kindUser:
		return m.usersRestriction
	case kindDomain:
		return m.domainsRestriction
	case kindGroup:
		return m.groupsRestriction
	case kindRole:
		return m.rolesRestriction
	default:
		return ""
	}
}

// visibleRows is the number of resource-list lines the body can show. The
// load-balancer list is rendered as a table, so it gives up one line to the
// column header; LB overview group headings occupy list lines of their own.
func (m Model) visibleRows() int {
	h := m.bodyHeight()
	if m.isLBOverview() {
		_, h = m.lbOverviewParts(h)
	} else if m.isListenerOverview() {
		_, h = m.listenerOverviewParts(h)
	} else if m.isVIPOverview() {
		_, h = m.vipOverviewParts(h)
	} else if m.isPoolOverview() {
		_, h = m.poolOverviewParts(h)
	} else if m.isMemberOverview() {
		_, h = m.memberOverviewParts(h)
	} else if m.isAmphoraOverview() {
		_, h = m.amphoraOverviewParts(h)
	} else if m.isHealthMonitorOverview() {
		_, h = m.healthMonitorOverviewParts(h)
	} else if m.isCOEClusterOverview() || m.isKubernetesServiceOverview() {
		h--
	} else if m.isGroupOverview() {
		_, h = m.identityOverviewParts(h, m.groupOverviewSummary)
	} else if m.isUserOverview() {
		_, h = m.identityOverviewParts(h, m.userOverviewSummary)
	} else if m.isDomainOverview() {
		_, h = m.identityOverviewParts(h, m.domainOverviewSummary)
	} else if m.isProjectOverview() {
		_, h = m.identityOverviewParts(h, m.projectOverviewSummary)
	} else if m.isRoleOverview() {
		_, h = m.identityOverviewParts(h, m.roleOverviewSummary)
	} else if m.isServiceOverview() {
		_, h = m.identityOverviewParts(h, m.serviceOverviewSummary)
	} else if m.isEndpointOverview() {
		_, h = m.identityOverviewParts(h, m.endpointOverviewSummary)
	} else if m.isRegionOverview() {
		_, h = m.identityOverviewParts(h, m.regionOverviewSummary)
	} else if m.isInstanceOverview() {
		_, h = m.identityOverviewParts(h, m.instanceOverviewSummary)
	} else if m.isHypervisorOverview() {
		_, h = m.identityOverviewParts(h, m.hypervisorOverviewSummary)
	} else if m.isAcceleratorOverview() {
		_, h = m.identityOverviewParts(h, m.acceleratorOverviewSummary)
	}
	if m.loc.isTopLevelList() && len(m.entries) > 0 {
		h -= 2 // blank scope separator + column-header row
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) bodyLines() []string {
	h := m.bodyHeight()
	if m.isLBOverview() {
		return m.lbOverviewLines(h)
	}
	if m.isListenerOverview() {
		return m.listenerOverviewLines(h)
	}
	if m.isVIPOverview() {
		return m.vipOverviewLines(h)
	}
	if m.isPoolOverview() {
		return m.poolOverviewLines(h)
	}
	if m.isMemberOverview() {
		return m.memberOverviewLines(h)
	}
	if m.isAmphoraOverview() {
		return m.amphoraOverviewLines(h)
	}
	if m.isHealthMonitorOverview() {
		return m.healthMonitorOverviewLines(h)
	}
	if m.isCOEClusterOverview() || m.isKubernetesServiceOverview() {
		return m.simpleKubernetesOverviewLines(h)
	}
	if m.isUserOverview() {
		return m.userOverviewLines(h)
	}
	if m.isDomainOverview() {
		return m.domainOverviewLines(h)
	}
	if m.isGroupOverview() {
		return m.groupOverviewLines(h)
	}
	if m.isProjectOverview() {
		return m.projectOverviewLines(h)
	}
	if m.isRoleOverview() {
		return m.roleOverviewLines(h)
	}
	if m.isServiceOverview() {
		return m.serviceOverviewLines(h)
	}
	if m.isEndpointOverview() {
		return m.endpointOverviewLines(h)
	}
	if m.isRegionOverview() {
		return m.regionOverviewLines(h)
	}
	if m.isInstanceOverview() {
		return m.instanceOverviewLines(h)
	}
	if m.isHypervisorOverview() {
		return m.hypervisorOverviewLines(h)
	}
	if m.isAcceleratorOverview() {
		return m.acceleratorOverviewLines(h)
	}
	if len(m.entries) == 0 {
		msg := "— empty —"
		switch {
		case m.refreshing:
			msg = m.spinner.View() + " refreshing…"
		case m.loading:
			msg = m.spinner.View() + " loading " + m.loadingWhat + "…"
		case m.loc.dead:
			msg = "this object was deleted since you last viewed it (press ← back or ctrl+home)"
		case m.loc.listKind() == kindAmphora && m.amphoraeErr != "":
			msg = m.amphoraeErr
		case m.loc.listKind() == kindUser && m.usersErr != "":
			msg = m.usersErr
		case m.loc.listKind() == kindDomain && m.domainsErr != "":
			msg = m.domainsErr
		case m.loc.listKind() == kindInstance && m.instancesErr != "":
			msg = m.instancesErr
		case m.loc.listKind() == kindHypervisor && m.hypervisorsErr != "":
			msg = m.hypervisorsErr
		case m.loc.listKind() == kindGroup && m.groupsErr != "":
			msg = m.groupsErr
		case m.loc.listKind() == kindProject && m.projectListErr != "":
			msg = m.projectListErr
		case m.loc.listKind() == kindRole && m.rolesErr != "":
			msg = m.rolesErr
		case m.filter.Value() != "" || m.status != statusAll:
			msg = "— no matches —"
		}
		lines := make([]string, 0, h)
		if m.loc.isTopLevelList() {
			// Keep the same visual separation from the scope line that populated
			// top-level lists get before their table header.
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.st.disabled.Render(msg))
		for len(lines) < h {
			lines = append(lines, "")
		}
		return lines
	}
	if m.loc.isTopLevelList() {
		// A blank line separates the scope line from the column headers, matching
		// the load-balancer overview's spacing above.
		return append([]string{""}, m.topLevelTableLines(h-1)...)
	}
	return m.resourceLines(h, "— empty —")
}

func (m Model) resourceLines(h int, empty string) []string {
	if h <= 0 {
		return nil
	}
	if len(m.entries) == 0 {
		if m.filter.Value() != "" || m.status != statusAll {
			empty = "— no matches —"
		}
		lines := []string{"  " + m.st.disabled.Render(empty)}
		for len(lines) < h {
			lines = append(lines, "")
		}
		return lines
	}
	lines := make([]string, 0, h)
	// Sticky group heading: in a grouped related list that overflows, keep one row
	// for the current group's heading so a scrolled-away section (e.g. PROJECTS)
	// stays labelled. The reserved row is the pinned heading while scrolled, or a
	// trailing blank at the top of the list.
	content := h
	if m.stickyHeadingsActive(h) {
		content = h - 1
		if sticky := m.stickyGroupHeading(m.top); sticky != "" {
			lines = append(lines, sticky)
		}
	}
	end := m.top + content
	if end > len(m.entries) {
		end = len(m.entries)
	}
	for i := m.top; i < end; i++ {
		lines = append(lines, m.renderRow(m.entries[i], i == m.cursor))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// stickyHeadingsActive reports whether the current related list reserves a row
// to pin the scrolled group's heading: an identity overview whose grouped related
// list overflows the region height h. Scoped to the identity overviews (whose
// related objects are always grouped) so the load-balancer overviews keep their
// existing row budget.
func (m Model) stickyHeadingsActive(h int) bool {
	if h <= 1 || len(m.entries) <= h || !m.isIdentityOverview() {
		return false
	}
	for _, e := range m.entries {
		if e.kind == entGroup {
			return true
		}
	}
	return false
}

func (m Model) isIdentityOverview() bool {
	return m.isUserOverview() || m.isDomainOverview() || m.isGroupOverview() || m.isProjectOverview() || m.isRoleOverview() ||
		m.isServiceOverview() || m.isEndpointOverview() || m.isRegionOverview() ||
		m.isInstanceOverview() || m.isHypervisorOverview() || m.isAcceleratorOverview()
}

// stickyGroupHeading renders the heading of the group containing the row at
// index i (the first visible row), or "" if i is itself a heading, is at the top,
// or has no preceding heading.
func (m Model) stickyGroupHeading(i int) string {
	if i <= 0 || i >= len(m.entries) || m.entries[i].kind == entGroup {
		return ""
	}
	for j := i - 1; j >= 0; j-- {
		if m.entries[j].kind == entGroup {
			return m.renderRow(m.entries[j], false)
		}
	}
	return ""
}

// listContentRows is the number of selectable-list rows actually available for
// content — the visible region minus the row reserved for a sticky heading.
// Cursor visibility (ensureVisible) and scroll hints use this rather than the raw
// region height so the pinned heading never hides the selected row.
func (m Model) listContentRows() int {
	h := m.visibleRows()
	if m.stickyHeadingsActive(h) {
		h--
	}
	return h
}

func (m Model) isLBOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeLoadBalancer
}

func (m Model) isVIPOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeVIP
}

func (m Model) isListenerOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeListener
}

func (m Model) isPoolOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypePool
}

func (m Model) isMemberOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeMember
}

func (m Model) isAmphoraOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeAmphora
}

func (m Model) isHealthMonitorOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeHealthMonitor
}

func (m Model) isCOEClusterOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeCOECluster
}

func (m Model) isKubernetesServiceOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeKubeService
}

func (m Model) isStatsOverview() bool {
	return m.isLBOverview() || m.isListenerOverview()
}

func (m Model) isOverview() bool {
	return m.isLBOverview() || m.isVIPOverview() || m.isListenerOverview() || m.isPoolOverview() || m.isMemberOverview() || m.isAmphoraOverview() ||
		m.isHealthMonitorOverview() || m.isCOEClusterOverview() || m.isKubernetesServiceOverview() ||
		m.isUserOverview() || m.isDomainOverview() || m.isGroupOverview() || m.isProjectOverview() || m.isRoleOverview() ||
		m.isServiceOverview() || m.isEndpointOverview() || m.isRegionOverview() ||
		m.isInstanceOverview() || m.isHypervisorOverview() || m.isAcceleratorOverview()
}

func (m Model) vipOverviewParts(h int) (summary []string, relatedHeight int) {
	const fixedChrome = 3 // top gap, gap before related objects, related heading
	if h <= fixedChrome {
		return nil, 0
	}
	minRelated := 0
	if len(m.entries) > 0 {
		minRelated = 1
	}
	summary = m.vipOverviewSummary(h - fixedChrome - minRelated)
	relatedHeight = h - len(summary) - fixedChrome
	if relatedHeight < 0 {
		relatedHeight = 0
	}
	return summary, relatedHeight
}

// relatedObjectsPanelTitle renders the shared related-list section heading used
// by every detail surface. Counts reflect the active filter, and issue totals
// use the same styling as load-balancer related objects.
func (m Model) relatedObjectsPanelTitle() string {
	visibleCount := selectableEntryCount(m.entries)
	allCount := selectableEntryCount(m.allEntries)
	title := fmt.Sprintf("RELATED OBJECTS %d", visibleCount)
	if visibleCount != allCount {
		title = fmt.Sprintf("RELATED OBJECTS %d/%d", visibleCount, allCount)
	}
	rendered := m.st.panelTitle.Render(title)
	errors, degraded := relatedIssueCounts(m.entries)
	return m.clip(m.renderIssueCounts(rendered, errors, degraded))
}

func (m Model) vipOverviewLines(h int) []string {
	summary, relatedHeight := m.vipOverviewParts(h)
	lines := make([]string, 0, h)
	if len(lines) < h {
		lines = append(lines, "")
	}
	lines = append(lines, summary...)
	if len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) < h {
		lines = append(lines, m.relatedObjectsPanelTitle())
	}
	lines = append(lines, m.resourceLines(relatedHeight, "— no related objects —")...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) vipOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	n := m.loc.node
	loading := m.lbDetailLoading[n.ID] || m.lbFIPLoading[n.OwningLBID]
	title := m.overviewPanelTitle("VIP DETAILS", loading, m.lbDetailErr[n.ID], time.Time{}, false)
	groups := m.vipDetailGroups()
	lines := []string{m.clip(title)}
	if m.width >= 96 {
		gap := 3
		available := m.width - gap
		leftWidth := available / 2
		rightWidth := available - leftWidth
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[0], groups[1], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		lines = append(lines, "")
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[2], groups[3], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width, m.subsectionHeading), "\n")...)
	}
	return limitLines(lines, budget)
}

func (m Model) poolOverviewParts(h int) (summary []string, relatedHeight int) {
	const fixedChrome = 3
	if h <= fixedChrome {
		return nil, 0
	}
	minRelated := 1
	if len(m.entries) > 0 {
		selectable := 0
		for i, entry := range m.entries {
			if entry.selectable() {
				selectable++
			}
			minRelated = i + 1
			if selectable == 3 {
				break
			}
		}
	}
	if minRelated > h-fixedChrome {
		minRelated = h - fixedChrome
	}
	summary = m.poolOverviewSummary(h - fixedChrome - minRelated)
	relatedHeight = h - len(summary) - fixedChrome
	if relatedHeight < 0 {
		relatedHeight = 0
	}
	return summary, relatedHeight
}

func (m Model) poolOverviewLines(h int) []string {
	summary, relatedHeight := m.poolOverviewParts(h)
	lines := make([]string, 0, h)
	lines = append(lines, "")
	lines = append(lines, summary...)
	if len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) < h {
		lines = append(lines, m.relatedObjectsPanelTitle())
	}
	lines = append(lines, m.resourceLines(relatedHeight, "— no related objects —")...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) poolOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	n := m.loc.node
	title := m.overviewPanelTitle(
		"POOL DETAILS",
		!m.refreshing && m.lbDetailLoading[n.ID],
		m.lbDetailErr[n.ID],
		m.updatedAt(n.ID, sectionDetails),
		m.lbDetailErr[n.ID] != "",
	)
	groups := m.poolDetailGroups()
	lines := []string{m.clip(title)}
	if m.width >= 90 {
		gap := 3
		available := m.width - gap
		leftWidth := available / 2
		rightWidth := available - leftWidth
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[0], groups[1], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width, m.subsectionHeading), "\n")...)
	}
	return limitLines(lines, budget)
}

func (m Model) poolDetailGroups() []overviewGroup {
	n := m.loc.node
	projectID, projectName := n.Attrs["project_id"], ""
	if m.loc.tree != nil {
		if projectID == "" {
			projectID = m.loc.tree.Meta.ProjectID
		}
		projectName = m.loc.tree.Meta.ProjectName
	}
	name := n.Name
	if name == "" {
		name = shortID(n.ID)
	}
	poolFields := []overviewField{
		{label: "Name", value: name},
		{label: "ID", value: n.ID},
		{label: "Project name", value: displayValue(projectName)},
		{label: "Project ID", value: displayValue(projectID)},
	}
	if description := strings.TrimSpace(n.Attrs["description"]); description != "" {
		poolFields = append(poolFields, overviewField{label: "Description", value: description})
	}
	poolFields = append(poolFields,
		overviewField{label: "Operating", value: displayValue(n.OperatingStatus), status: true},
		overviewField{label: "Provisioning", value: displayValue(n.ProvisioningStatus), status: true},
		overviewField{label: "Admin state", value: adminStateLabel(n.Attrs["admin_state_up"]), status: true},
		overviewField{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
		overviewField{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
	)

	persistence := n.Attrs["session_persistence"]
	if persistence == "" {
		persistence = "none"
	}
	configFields := []overviewField{
		{label: "Protocol", value: displayValue(n.Attrs["protocol"])},
		{label: "Algorithm", value: displayValue(n.Attrs["lb_algorithm"])},
		{label: "Session persistence", value: persistence},
	}
	if cookie := strings.TrimSpace(n.Attrs["persistence_cookie"]); cookie != "" {
		configFields = append(configFields, overviewField{label: "Cookie name", value: cookie})
	}
	configFields = append(configFields,
		overviewField{label: "Members", value: displayValue(n.Attrs["member_count"])},
		overviewField{label: "Listeners", value: displayValue(n.Attrs["listener_count"])},
		overviewField{label: "Backend TLS", value: adminStateLabel(n.Attrs["tls_enabled"])},
	)
	if versions := strings.TrimSpace(n.Attrs["tls_versions"]); versions != "" {
		configFields = append(configFields, overviewField{label: "TLS versions", value: versions})
	}
	if protocols := strings.TrimSpace(n.Attrs["alpn_protocols"]); protocols != "" {
		configFields = append(configFields, overviewField{label: "ALPN", value: protocols})
	}
	if tags := strings.TrimSpace(n.Attrs["tags"]); tags != "" {
		configFields = append(configFields, overviewField{label: "Tags", value: tags})
	}
	return []overviewGroup{
		{fields: poolFields},
		{title: "CONFIGURATION", fields: configFields},
	}
}

func (m Model) memberOverviewParts(h int) (summary []string, relatedHeight int) {
	if h <= 1 {
		return m.memberOverviewSummary(h), 0
	}
	return m.memberOverviewSummary(h - 1), 0 // permanent gap below the subtitle
}

func (m Model) memberOverviewLines(h int) []string {
	summary, _ := m.memberOverviewParts(h)
	if h <= 1 {
		return summary
	}
	lines := make([]string, 0, h)
	if len(lines) < h {
		lines = append(lines, "")
	}
	lines = append(lines, summary...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) memberOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	n := m.loc.node
	title := m.overviewPanelTitle(
		"MEMBER DETAILS",
		!m.refreshing && m.lbDetailLoading[n.ID],
		m.lbDetailErr[n.ID],
		m.updatedAt(n.ID, sectionDetails),
		m.lbDetailErr[n.ID] != "",
	)
	if budget == 1 {
		return []string{m.clip(title)}
	}
	return limitLines(strings.Split(m.renderOverviewPanel(title, m.memberDetailFields(), m.width, budget-1), "\n"), budget)
}

func (m Model) memberDetailFields() []overviewField {
	n := m.loc.node
	name := strings.TrimSpace(n.Attrs["name"])
	if name == "" {
		name = n.Name
	}
	if name == "" {
		name = shortID(n.ID)
	}
	projectID, projectName := n.Attrs["project_id"], ""
	if m.loc.tree != nil {
		if projectID == "" {
			projectID = m.loc.tree.Meta.ProjectID
		}
		projectName = m.loc.tree.Meta.ProjectName
	}
	fields := []overviewField{
		{label: "Name", value: name},
		{label: "ID", value: n.ID},
		{label: "Project name", value: displayValue(projectName)},
		{label: "Project ID", value: displayValue(projectID)},
		{label: "Address", value: displayValue(n.Attrs["address"])},
		{label: "Protocol port", value: displayValue(n.Attrs["port"])},
		{label: "Subnet ID", value: displayValue(n.Attrs["subnet_id"])},
		{label: "Weight", value: displayValue(n.Attrs["weight"])},
		{label: "Backup", value: yesNoValue(n.Attrs["backup"])},
	}
	if address := strings.TrimSpace(n.Attrs["monitor_address"]); address != "" {
		fields = append(fields, overviewField{label: "Monitor address", value: address})
	}
	if port := strings.TrimSpace(n.Attrs["monitor_port"]); port != "" {
		fields = append(fields, overviewField{label: "Monitor port", value: port})
	}
	fields = append(fields,
		overviewField{label: "Operating", value: displayValue(n.OperatingStatus), status: true},
		overviewField{label: "Provisioning", value: displayValue(n.ProvisioningStatus), status: true},
		overviewField{label: "Admin state", value: adminStateLabel(n.Attrs["admin_state_up"]), status: true},
	)
	if tags := strings.TrimSpace(n.Attrs["tags"]); tags != "" {
		fields = append(fields, overviewField{label: "Tags", value: tags})
	}
	fields = append(fields,
		overviewField{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
		overviewField{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
	)
	return fields
}

func yesNoValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes":
		return "Yes"
	case "false", "no":
		return "No"
	default:
		return displayValue(value)
	}
}

func (m Model) amphoraOverviewParts(h int) (summary []string, relatedHeight int) {
	if h <= 1 {
		return m.amphoraOverviewSummary(h), 0
	}
	return m.amphoraOverviewSummary(h - 1), 0
}

func (m Model) amphoraOverviewLines(h int) []string {
	summary, _ := m.amphoraOverviewParts(h)
	if h <= 1 {
		return summary
	}
	lines := make([]string, 0, h)
	lines = append(lines, "")
	lines = append(lines, summary...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) amphoraOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	n := m.loc.node
	title := m.overviewPanelTitle(
		"AMPHORA DETAILS",
		!m.refreshing && m.lbDetailLoading[n.ID],
		m.lbDetailErr[n.ID],
		m.updatedAt(n.ID, sectionDetails),
		m.lbDetailErr[n.ID] != "",
	)
	if budget == 1 {
		return []string{m.clip(title)}
	}
	groups := m.amphoraDetailGroups()
	lines := []string{m.clip(title)}
	if m.width >= 90 {
		gap := 3
		available := m.width - gap
		leftWidth := available / 2
		rightWidth := available - leftWidth
		for i := 0; i < len(groups); i += 2 {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[i], groups[i+1], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		}
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width, m.subsectionHeading), "\n")...)
	}
	return limitLines(lines, budget)
}

func (m Model) amphoraDetailGroups() []overviewGroup {
	n := m.loc.node
	lbName := ""
	for _, lb := range m.lbs {
		if lb.ID == n.OwningLBID {
			lbName = lb.Name
			break
		}
	}
	expires, expiryColor := certificateExpiryDisplay(n.Attrs["cert_expiration"], m.clock())
	return []overviewGroup{
		{fields: []overviewField{
			{label: "ID", value: n.ID},
			{label: "Load balancer name", value: displayValue(lbName)},
			{label: "Load balancer ID", value: displayValue(n.OwningLBID)},
			{label: "Role", value: displayValue(n.Attrs["role"])},
			{label: "Status", value: displayValue(n.Attrs["status"]), status: true},
		}},
		{title: "COMPUTE", fields: []overviewField{
			{label: "Compute ID", value: displayValue(n.Attrs["compute_id"])},
			{label: "Image ID", value: displayValue(n.Attrs["image_id"])},
			{label: "Cached zone", value: displayValue(n.Attrs["cached_zone"])},
		}},
		{title: "NETWORK", fields: []overviewField{
			{label: "Management IP", value: displayValue(n.Attrs["lb_network_ip"])},
			{label: "HA IP", value: displayValue(n.Attrs["ha_ip"])},
			{label: "HA port ID", value: displayValue(n.Attrs["ha_port_id"])},
		}},
		{title: "VRRP", fields: []overviewField{
			{label: "IP", value: displayValue(n.Attrs["vrrp_ip"])},
			{label: "Port ID", value: displayValue(n.Attrs["vrrp_port_id"])},
			{label: "Interface", value: displayValue(n.Attrs["vrrp_interface"])},
			{label: "ID", value: displayValue(n.Attrs["vrrp_id"])},
			{label: "Priority", value: displayValue(n.Attrs["vrrp_priority"])},
		}},
		{title: "INTERNAL CERTIFICATE", fields: []overviewField{
			{label: "Expires", value: expires, color: expiryColor},
			{label: "Busy", value: yesNoValue(n.Attrs["cert_busy"])},
		}},
		{title: "LIFECYCLE", fields: []overviewField{
			{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
			{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
		}},
	}
}

func (m Model) healthMonitorOverviewParts(h int) (summary []string, relatedHeight int) {
	if h <= 1 {
		return m.healthMonitorOverviewSummary(h), 0
	}
	return m.healthMonitorOverviewSummary(h - 1), 0
}

func (m Model) healthMonitorOverviewLines(h int) []string {
	summary, _ := m.healthMonitorOverviewParts(h)
	if h <= 1 {
		return summary
	}
	lines := make([]string, 0, h)
	lines = append(lines, "")
	lines = append(lines, summary...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) healthMonitorOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	n := m.loc.node
	title := m.overviewPanelTitle(
		"HEALTH MONITOR DETAILS",
		!m.refreshing && m.lbDetailLoading[n.ID],
		m.lbDetailErr[n.ID],
		m.updatedAt(n.ID, sectionDetails),
		m.lbDetailErr[n.ID] != "",
	)
	groups := m.healthMonitorDetailGroups()
	lines := []string{m.clip(title)}
	if m.width >= 90 {
		gap := 3
		available := m.width - gap
		leftWidth := available / 2
		rightWidth := available - leftWidth
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[0], groups[1], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width, m.subsectionHeading), "\n")...)
	}
	return limitLines(lines, budget)
}

func (m Model) healthMonitorDetailGroups() []overviewGroup {
	n := m.loc.node
	projectID, projectName := n.Attrs["project_id"], ""
	if m.loc.tree != nil {
		if projectID == "" {
			projectID = m.loc.tree.Meta.ProjectID
		}
		projectName = m.loc.tree.Meta.ProjectName
	}
	name := n.Name
	if name == "" {
		name = shortID(n.ID)
	}
	monitorFields := []overviewField{
		{label: "Name", value: name},
		{label: "ID", value: n.ID},
		{label: "Project name", value: displayValue(projectName)},
		{label: "Project ID", value: displayValue(projectID)},
		{label: "Operating", value: displayValue(n.OperatingStatus), status: true},
		{label: "Provisioning", value: displayValue(n.ProvisioningStatus), status: true},
		{label: "Admin state", value: adminStateLabel(n.Attrs["admin_state_up"]), status: true},
		{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
		{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
	}
	if tags := strings.TrimSpace(n.Attrs["tags"]); tags != "" {
		monitorFields = append(monitorFields, overviewField{label: "Tags", value: tags})
	}

	seconds := func(value string) string {
		value = strings.TrimSpace(value)
		if value == "" {
			return displayValue(value)
		}
		return value + " s"
	}
	configFields := []overviewField{
		{label: "Type", value: displayValue(n.Attrs["type"])},
		{label: "Delay", value: seconds(n.Attrs["delay"])},
		{label: "Timeout", value: seconds(n.Attrs["timeout"])},
		{label: "Max retries", value: displayValue(n.Attrs["max_retries"])},
		{label: "Max retries down", value: displayValue(n.Attrs["max_retries_down"])},
	}
	monitorType := strings.ToUpper(strings.TrimSpace(n.Attrs["type"]))
	if monitorType == "HTTP" || monitorType == "HTTPS" {
		configFields = append(configFields,
			overviewField{label: "HTTP method", value: displayValue(n.Attrs["http_method"])},
			overviewField{label: "URL path", value: displayValue(n.Attrs["url_path"])},
			overviewField{label: "Expected codes", value: displayValue(n.Attrs["expected_codes"])},
		)
		if version := strings.TrimSpace(n.Attrs["http_version"]); version != "" {
			configFields = append(configFields, overviewField{label: "HTTP version", value: version})
		}
		if domain := strings.TrimSpace(n.Attrs["domain_name"]); domain != "" {
			configFields = append(configFields, overviewField{label: "Domain name", value: domain})
		}
	}
	return []overviewGroup{
		{fields: monitorFields},
		{title: "CONFIGURATION", fields: configFields},
	}
}

type overviewGroup struct {
	title  string
	fields []overviewField
}

func (m Model) vipDetailGroups() []overviewGroup {
	n := m.loc.node
	kind := "Primary VIP"
	if n.Attrs["vip_kind"] == "additional" {
		kind = "Additional VIP"
	}
	projectID, projectName := "", ""
	if m.loc.tree != nil {
		projectID = m.loc.tree.Meta.ProjectID
		projectName = m.loc.tree.Meta.ProjectName
	}
	return []overviewGroup{
		{fields: []overviewField{
			{label: "Type", value: kind},
			{label: "Address", value: displayValue(n.Attrs["address"])},
			{label: "Floating IP", value: displayValue(n.Attrs["floating_ip"])},
			{label: "Project name", value: displayValue(projectName), breakBefore: true, subheading: "PROJECT"},
			{label: "Project ID", value: displayValue(projectID)},
		}},
		{title: "PORT", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["port_name"])},
			{label: "ID", value: displayValue(n.Attrs["port_id"])},
			{label: "Security groups", value: displayValue(n.Attrs["security_group_ids"])},
		}},
		{title: "SUBNET", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["subnet_name"])},
			{label: "ID", value: displayValue(n.Attrs["subnet_id"])},
		}},
		{title: "NETWORK", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["network_name"])},
			{label: "ID", value: displayValue(n.Attrs["network_id"])},
		}},
	}
}

// subsectionHeading renders an overview detail subsection header (IDENTITY,
// STATE, COMPUTE, …) in the same style and spacing as the related-object group
// headers, so both read consistently.
func (m Model) subsectionHeading(title string) string {
	return " " + m.st.relatedGroup.Render(title)
}

func (m Model) renderOverviewGroupPair(left, right overviewGroup, leftWidth, rightWidth, gap int, heading func(string) string) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderOverviewGroup(left, leftWidth, heading),
		strings.Repeat(" ", gap),
		m.renderOverviewGroup(right, rightWidth, heading),
	)
}

func (m Model) renderOverviewGroup(group overviewGroup, width int, heading func(string) string) string {
	if width < 1 {
		width = 1
	}
	labelWidth := 0
	for _, field := range group.fields {
		if fieldWidth := lipgloss.Width(field.label); fieldWidth > labelWidth {
			labelWidth = fieldWidth
		}
	}
	if cap := (width - 2) / 2; labelWidth > cap {
		labelWidth = cap
	}
	lines := make([]string, 0, len(group.fields)+1)
	if group.title != "" {
		lines = append(lines, heading(group.title))
	}
	for _, field := range group.fields {
		if field.breakBefore {
			lines = append(lines, "")
		}
		if field.subheading != "" {
			lines = append(lines, heading(field.subheading))
		}
		label := m.st.panelLabel.Render(padRight(field.label, labelWidth))
		value := field.value
		if field.status && value != "—" {
			value = lipgloss.NewStyle().Foreground(statusColor(value)).Render(value)
		} else if field.color != lipgloss.Color("") && value != "—" {
			value = lipgloss.NewStyle().Foreground(field.color).Render(value)
		}
		lines = append(lines, wrapOverviewValue("  "+label+"  ", value, width)...)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// lbOverviewParts computes the summary and related-list allocation. The
// summary compacts first so navigation always retains at least a few rows.
func (m Model) lbOverviewParts(h int) (summary []string, relatedHeight int) {
	const fixedChrome = 3 // top gap, gap before related objects, related heading
	if h <= fixedChrome {
		return nil, 0
	}
	minRelated := 1
	if len(m.entries) > 0 {
		selectable := 0
		for i, e := range m.entries {
			if e.selectable() {
				selectable++
			}
			minRelated = i + 1
			if selectable == 3 {
				break
			}
		}
	}
	if minRelated > h-fixedChrome {
		minRelated = h - fixedChrome
	}
	summary = m.lbOverviewSummary(h - fixedChrome - minRelated)
	relatedHeight = h - len(summary) - fixedChrome
	if relatedHeight < 0 {
		relatedHeight = 0
	}
	return summary, relatedHeight
}

func (m Model) lbOverviewLines(h int) []string {
	summary, relatedHeight := m.lbOverviewParts(h)
	lines := make([]string, 0, h)
	if len(lines) < h {
		lines = append(lines, "") // permanent separation from project/status line
	}
	lines = append(lines, summary...)
	if len(lines) < h {
		lines = append(lines, "") // permanent separation before related objects
	}
	if len(lines) < h {
		renderedTitle := m.relatedObjectsPanelTitle()
		lbID := m.loc.node.ID
		renderedTitle = m.overviewPanelTitleRendered(renderedTitle, false, m.lbRelatedErr[lbID], m.updatedAt(lbID, sectionRelated), m.lbRelatedErr[lbID] != "")
		lines = append(lines, m.clip(renderedTitle))
	}
	lines = append(lines, m.resourceLines(relatedHeight, "— no related objects —")...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

type overviewField struct {
	label       string
	value       string
	status      bool
	color       lipgloss.Color
	breakBefore bool
	subheading  string
}

func (m Model) listenerOverviewParts(h int) (summary []string, relatedHeight int) {
	const fixedChrome = 3
	if h <= fixedChrome {
		return nil, 0
	}
	minRelated := 1
	if len(m.entries) > 0 {
		selectable := 0
		for i, entry := range m.entries {
			if entry.selectable() {
				selectable++
			}
			minRelated = i + 1
			if selectable == 3 {
				break
			}
		}
	}
	if minRelated > h-fixedChrome {
		minRelated = h - fixedChrome
	}
	summary = m.listenerOverviewSummary(h - fixedChrome - minRelated)
	relatedHeight = h - len(summary) - fixedChrome
	if relatedHeight < 0 {
		relatedHeight = 0
	}
	return summary, relatedHeight
}

func (m Model) listenerOverviewLines(h int) []string {
	summary, relatedHeight := m.listenerOverviewParts(h)
	lines := make([]string, 0, h)
	lines = append(lines, "")
	lines = append(lines, summary...)
	if len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) < h {
		rendered := m.relatedObjectsPanelTitle()
		id := m.loc.node.ID
		rendered = m.overviewPanelTitleRendered(rendered, false, m.lbRelatedErr[id], m.updatedAt(id, sectionRelated), m.lbRelatedErr[id] != "")
		lines = append(lines, m.clip(rendered))
	}
	lines = append(lines, m.resourceLines(relatedHeight, "— no related objects —")...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) listenerOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	n := m.loc.node
	detailTitle := m.overviewPanelTitle("LISTENER DETAILS", !m.refreshing && m.lbDetailLoading[n.ID], m.lbDetailErr[n.ID], m.updatedAt(n.ID, sectionDetails), m.lbDetailErr[n.ID] != "")
	statsTitle := m.statsPanelTitle(n.ID)
	details := m.listenerDetailFields()
	stats := m.lbStatFields()
	if m.width >= 90 {
		limit := budget - 1
		if limit < 0 {
			limit = 0
		}
		gap := 3
		available := m.width - gap
		leftWidth := available * 3 / 5
		rightWidth := available - leftWidth
		left := m.renderOverviewPanel(detailTitle, details, leftWidth, limit)
		right := m.renderOverviewPanel(statsTitle, stats, rightWidth, limit)
		return limitLines(strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right), "\n"), budget)
	}
	if budget == 1 {
		return []string{m.clip(m.st.panelTitle.Render("LISTENER DETAILS · STATS"))}
	}
	if budget == 2 {
		return []string{m.clip(detailTitle), ""}
	}
	fieldBudget := budget - 3
	detailLimit := (fieldBudget + 1) / 2
	statsLimit := fieldBudget - detailLimit
	if detailLimit > len(details) {
		statsLimit += detailLimit - len(details)
		detailLimit = len(details)
	}
	if statsLimit > len(stats) {
		detailLimit += statsLimit - len(stats)
		statsLimit = len(stats)
		if detailLimit > len(details) {
			detailLimit = len(details)
		}
	}
	left := strings.Split(m.renderOverviewPanel(detailTitle, details, m.width, detailLimit), "\n")
	right := strings.Split(m.renderOverviewPanel(statsTitle, stats, m.width, statsLimit), "\n")
	return limitLines(append(append(left, ""), right...), budget)
}

func (m Model) listenerDetailFields() []overviewField {
	n := m.loc.node
	name := n.Name
	if name == "" {
		name = shortID(n.ID)
	}
	projectID, projectName := "", ""
	if m.loc.tree != nil {
		projectID = m.loc.tree.Meta.ProjectID
		projectName = m.loc.tree.Meta.ProjectName
	}
	fields := []overviewField{
		{label: "Name", value: name},
		{label: "ID", value: n.ID},
		{label: "Project name", value: displayValue(projectName)},
		{label: "Project ID", value: displayValue(projectID)},
	}
	if description := strings.TrimSpace(n.Attrs["description"]); description != "" {
		fields = append(fields, overviewField{label: "Description", value: description})
	}
	fields = append(fields,
		overviewField{label: "Protocol", value: displayValue(listenerProtocolLabel(n.Attrs["protocol"]))},
		overviewField{label: "Port", value: displayValue(n.Attrs["port"])},
		overviewField{label: "Connection limit", value: displayValue(n.Attrs["connection_limit"])},
	)
	if allowed := strings.TrimSpace(n.Attrs["allowed_cidrs"]); allowed != "" {
		fields = append(fields, overviewField{label: "Allowed CIDRs", value: allowed})
	}
	fields = append(fields,
		overviewField{label: "Operating", value: displayValue(n.OperatingStatus), status: true},
		overviewField{label: "Provisioning", value: displayValue(n.ProvisioningStatus), status: true},
		overviewField{label: "Admin state", value: adminStateLabel(n.Attrs["admin_state_up"]), status: true},
		overviewField{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
		overviewField{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
	)
	if n.Attrs["protocol"] == "TERMINATED_HTTPS" {
		fields = append(fields, m.listenerCertificateFields()...)
	}
	return fields
}

func (m Model) listenerCertificateFields() []overviewField {
	n := m.loc.node
	certificate := n.Attrs["certificate_name"]
	if certificate == "" {
		certificate = shortReference(n.Attrs["certificate_ref"])
	}
	if certErr := strings.TrimSpace(n.Attrs["certificate_error"]); certErr != "" {
		certificate = m.st.disabled.Render("— information unavailable —")
	}
	expires, expiryColor := certificateExpiryDisplay(n.Attrs["certificate_not_after"], m.clock())
	fields := []overviewField{
		{label: "Certificate", value: displayValue(certificate)},
		{label: "Expires", value: expires, color: expiryColor},
		{label: "Subject", value: displayValue(n.Attrs["certificate_subject"])},
		{label: "Issuer", value: displayValue(n.Attrs["certificate_issuer"])},
		{label: "Valid from", value: displayTimestamp(n.Attrs["certificate_not_before"])},
		{label: "SNI certificates", value: displayValue(n.Attrs["sni_certificate_count"])},
	}
	if versions := strings.TrimSpace(n.Attrs["tls_versions"]); versions != "" {
		fields = append(fields, overviewField{label: "TLS versions", value: versions})
	}
	if protocols := strings.TrimSpace(n.Attrs["alpn_protocols"]); protocols != "" {
		fields = append(fields, overviewField{label: "ALPN", value: protocols})
	}
	return fields
}

func shortReference(ref string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(ref), "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return ""
	}
	return shortID(parts[len(parts)-1])
}

func certificateExpiryDisplay(value string, now time.Time) (string, lipgloss.Color) {
	if value == "" {
		return "—", lipgloss.Color("")
	}
	expires, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value, lipgloss.Color("")
	}
	remaining := expires.Sub(now)
	label := displayTimestamp(value)
	var color lipgloss.Color
	switch {
	case remaining <= 0:
		label += " (expired)"
		color = lipgloss.Color("196")
	case remaining < 14*24*time.Hour:
		label += fmt.Sprintf(" (%dd remaining)", daysRemaining(remaining))
		color = lipgloss.Color("214")
	case remaining < 30*24*time.Hour:
		label += fmt.Sprintf(" (%dd remaining)", daysRemaining(remaining))
		color = lipgloss.Color("226")
	default:
		label += fmt.Sprintf(" (%dd remaining)", daysRemaining(remaining))
		color = lipgloss.Color("42")
	}
	return label, color
}

func daysRemaining(duration time.Duration) int {
	return int((duration + 24*time.Hour - 1) / (24 * time.Hour))
}

func (m Model) lbOverviewSummary(budget int) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	details := m.lbDetailFields()
	stats := m.lbStatFields()
	// A full refresh is already announced in the subtitle and keeps the last
	// committed panel values visible. Per-panel loading labels would duplicate
	// that status and make the retained values look unavailable.
	lbID := m.loc.node.ID
	detailTitle := m.overviewPanelTitle("LOAD BALANCER DETAILS", !m.refreshing && m.lbDetailLoading[lbID], m.lbDetailErr[lbID], m.updatedAt(lbID, sectionDetails), m.lbDetailErr[lbID] != "")
	statsTitle := m.statsPanelTitle(lbID)

	if m.width >= 90 {
		limit := budget - 1
		if limit < 0 {
			limit = 0
		}
		gap := 3
		available := m.width - gap
		leftWidth := available * 3 / 5
		rightWidth := available - leftWidth
		left := m.renderOverviewPanel(detailTitle, details, leftWidth, limit)
		right := m.renderOverviewPanel(statsTitle, stats, rightWidth, limit)
		joined := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)
		return limitLines(strings.Split(joined, "\n"), budget)
	}

	// Narrow terminals stack the panels. Divide the available field rows between
	// them, prioritizing the first few identity and traffic values.
	if budget == 1 {
		return []string{m.clip(m.st.panelTitle.Render("LOAD BALANCER DETAILS · STATS"))}
	}
	if budget == 2 {
		return []string{m.clip(detailTitle), ""}
	}
	fieldBudget := budget - 3 // two headings and their permanent separating row
	detailLimit := (fieldBudget + 1) / 2
	statsLimit := fieldBudget - detailLimit
	if detailLimit > len(details) {
		statsLimit += detailLimit - len(details)
		detailLimit = len(details)
	}
	if statsLimit > len(stats) {
		detailLimit += statsLimit - len(stats)
		statsLimit = len(stats)
		if detailLimit > len(details) {
			detailLimit = len(details)
		}
	}
	left := strings.Split(m.renderOverviewPanel(detailTitle, details, m.width, detailLimit), "\n")
	right := strings.Split(m.renderOverviewPanel(statsTitle, stats, m.width, statsLimit), "\n")
	stacked := append(left, "")
	stacked = append(stacked, right...)
	return limitLines(stacked, budget)
}

func (m Model) lbDetailFields() []overviewField {
	n := m.loc.node
	name := n.Name
	if name == "" {
		name = shortID(n.ID)
	}
	vip := n.Attrs["vip_address"]
	primary := primaryVIP(n)
	if vip == "" && primary != nil {
		vip = primary.Name
	}
	if primary != nil {
		if floatingIP := primary.Attrs["floating_ip"]; vip != "" && floatingIP != "" {
			vip += " (" + floatingIP + ")"
		}
	}
	adminState := n.Attrs["admin_state_up"]
	if adminState == "" && m.lbDetailLoading[n.ID] {
		adminState = "…"
	}
	var projectID, projectName string
	if m.loc.tree != nil {
		projectID = m.loc.tree.Meta.ProjectID
		projectName = m.loc.tree.Meta.ProjectName
	}
	fields := []overviewField{
		{label: "Name", value: name},
		{label: "ID", value: n.ID},
		{label: "Project name", value: displayValue(projectName)},
		{label: "Project ID", value: displayValue(projectID)},
	}
	if description := strings.TrimSpace(n.Attrs["description"]); description != "" {
		fields = append(fields, overviewField{label: "Description", value: description})
	}
	fields = append(fields,
		overviewField{label: "Primary VIP", value: displayValue(vip)},
		overviewField{label: "Provider", value: displayValue(n.Attrs["provider"])},
		overviewField{label: "Flavor ID", value: displayValue(n.Attrs["flavor_id"])},
		overviewField{label: "Operating", value: displayValue(n.OperatingStatus), status: true},
		overviewField{label: "Provisioning", value: displayValue(n.ProvisioningStatus), status: true},
		overviewField{label: "Admin state", value: adminStateLabel(adminState), status: true},
		overviewField{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
		overviewField{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
	)
	return fields
}

func (m Model) loadingLabel() string {
	if m.refreshing {
		return "refreshing…"
	}
	return m.loadingWhat
}

func adminStateLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "enabled":
		return "ENABLED"
	case "false", "disabled":
		return "DISABLED"
	default:
		return displayValue(value)
	}
}

func (m Model) lbStatFields() []overviewField {
	n := m.loc.node
	stats := m.lbStats[n.ID]
	changes := m.lbStatsChanges[n.ID]
	value := func(key string, bytes bool) string {
		if stats == nil {
			if m.lbStatsLoading[n.ID] {
				return "…"
			}
			return "—"
		}
		v, ok := stats[key]
		if !ok || v == nil {
			return "—"
		}
		formatted := formatStatCount(v)
		if bytes {
			formatted = formatStatBytes(v)
		}
		return formatted
	}
	withByteRate := func(key string) string {
		formatted := value(key, true)
		if change, ok := changes[key]; ok {
			formatted += " (" + formatByteRate(change.rate) + ")"
		}
		return formatted
	}
	withSignedRate := func(key string) string {
		formatted := value(key, false)
		if change, ok := changes[key]; ok {
			formatted += " (+" + formatCounterRate(change.rate) + ")"
		}
		return formatted
	}
	withDelta := func(key string) string {
		formatted := value(key, false)
		if change, ok := changes[key]; ok {
			formatted += " (+" + formatCounterDelta(change.delta) + ")"
		}
		return formatted
	}
	return []overviewField{
		{label: "Active connections", value: value("active_connections", false)},
		{label: "Total connections", value: withSignedRate("total_connections")},
		{label: "Request errors", value: withDelta("request_errors")},
		{label: "Bytes in", value: withByteRate("bytes_in")},
		{label: "Bytes out", value: withByteRate("bytes_out")},
	}
}

func (m Model) statsPanelTitle(lbID string) string {
	updated := m.updatedAt(lbID, sectionStats)
	errText := m.lbStatsErr[lbID]
	loading := !m.refreshing && m.lbStatsLoading[lbID]
	if errText == "" && m.statsWithinAutoInterval(updated) {
		return m.st.panelTitle.Render("STATS") + " · " + m.st.disabled.Render(m.statsSpinner.View())
	}
	overdue := m.autoRefreshEnabled && !updated.IsZero() && !m.statsWithinAutoInterval(updated)
	return m.overviewPanelTitle("STATS", loading, errText, updated, errText != "" || overdue)
}

func (m Model) overviewPanelTitle(title string, loading bool, errText string, updatedAt time.Time, stale bool) string {
	return m.overviewPanelTitleRendered(m.st.panelTitle.Render(title), loading, errText, updatedAt, stale)
}

func (m Model) overviewPanelTitleRendered(title string, loading bool, errText string, updatedAt time.Time, stale bool) string {
	state := ""
	if freshness := m.freshnessLabel(updatedAt); freshness != "" {
		state = " · " + m.st.disabled.Render(freshness)
		if stale {
			state += " · " + m.st.flashErr.Render("stale")
		}
	} else if loading {
		state = " · " + m.st.disabled.Render("loading…")
	} else if errText != "" {
		state = " · " + m.st.flashErr.Render("unavailable")
	}
	return title + state
}

func (m Model) renderOverviewPanel(title string, fields []overviewField, width, limit int) string {
	if width < 1 {
		width = 1
	}
	if limit > len(fields) {
		limit = len(fields)
	}
	if limit < 0 {
		limit = 0
	}
	labelWidth := 0
	for _, field := range fields[:limit] {
		if w := lipgloss.Width(field.label); w > labelWidth {
			labelWidth = w
		}
	}
	if cap := width / 2; labelWidth > cap {
		labelWidth = cap
	}
	lines := []string{lipgloss.NewStyle().MaxWidth(width).Render(title)}
	for _, field := range fields[:limit] {
		label := m.st.panelLabel.Render(padRight(field.label, labelWidth))
		value := field.value
		if field.status && value != "—" {
			value = lipgloss.NewStyle().Foreground(statusColor(value)).Render(value)
		} else if field.color != lipgloss.Color("") && value != "—" {
			value = lipgloss.NewStyle().Foreground(field.color).Render(value)
		}
		lines = append(lines, wrapOverviewValue(label+"  ", value, width)...)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// wrapOverviewValue wraps values inside the upper details area and aligns
// continuation lines with the first value cell. Related-object rows use their
// own single-line renderer and intentionally remain clipped.
func wrapOverviewValue(prefix, value string, width int) []string {
	prefixWidth := lipgloss.Width(prefix)
	valueWidth := width - prefixWidth
	if valueWidth < 1 {
		return []string{lipgloss.NewStyle().MaxWidth(width).Render(prefix + value)}
	}
	wrapped := strings.Split(ansi.Wrap(value, valueWidth, " "), "\n")
	lines := make([]string, 0, len(wrapped))
	for i, part := range wrapped {
		linePrefix := strings.Repeat(" ", prefixWidth)
		if i == 0 {
			linePrefix = prefix
		}
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width).Render(linePrefix+part))
	}
	return lines
}

func displayValue(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func displayTimestamp(value string) string {
	if value == "" {
		return "—"
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02 15:04:05 UTC")
}

func limitLines(lines []string, limit int) []string {
	if len(lines) > limit {
		return lines[:limit]
	}
	return lines
}

// lbColumnTitles are the load-balancer table headers; the project column
// appears outside project scope. The d toggle relabels the name/project
// columns to reflect whether they show IDs or names.
func (m Model) lbColumnTitles() []string {
	name := "NAME"
	if m.showIDs {
		name = "LOAD BALANCER ID"
	}
	if m.multiProjectScope {
		project := "PROJECT"
		if m.showIDs {
			project = "PROJECT ID"
		}
		return []string{name, project, "PROVIDER", "VIP", "PROVISIONING", "OPERATING"}
	}
	return []string{name, "PROVIDER", "VIP", "PROVISIONING", "OPERATING"}
}

func (m Model) lbRowCells(e entry) []string {
	lb := e.lb
	name := lbNameCell(lb.Name, lb.ID, m.showIDs)
	if m.multiProjectScope {
		project := lbNameCell(lb.ProjectName, lb.ProjectID, m.showIDs)
		return []string{name, project, lb.Provider, lb.VipAddress, lb.ProvisioningStatus, lb.OperatingStatus}
	}
	return []string{name, lb.Provider, lb.VipAddress, lb.ProvisioningStatus, lb.OperatingStatus}
}

// lbNameCell renders a name/ID column cell. In ID mode it shows the full id
// (the point of the toggle is to read/copy it); otherwise the name, falling back
// to a short id when the name is unknown.
func lbNameCell(name, id string, showIDs bool) string {
	if showIDs {
		return id
	}
	if name != "" {
		return name
	}
	return shortID(id)
}

// tableColumnGap is the number of spaces rendered after every column (including
// the last, so the row fills the width and the selection bar spans it).
const tableColumnGap = 2

// topLevelTableLines renders every area's top-level list as a fixed-width table:
// a header row plus the scrolled window of rows, the selected row highlighted
// and the status columns colored. It returns exactly h lines.
//
// Column widths are computed here rather than delegated to lip gloss's table,
// whose auto-sizer enforces no per-column minimum and starves narrow columns
// (protocol, port) to a single character whenever another column (a long name or
// a UUID) is wide. layoutColumnWidths keeps every column readable and always
// sums to the terminal width so the highlight bar is flush.
func (m Model) topLevelTableLines(h int) []string {
	titles := m.columnTitles()
	statusCols := m.statusColumnSet(len(titles))

	vis := h - 1 // header row
	if vis < 1 {
		vis = 1
	}
	start := m.top
	end := start + vis
	if end > len(m.entries) {
		end = len(m.entries)
	}
	window := m.entries[start:end]
	rows := make([][]string, len(window))
	for i, e := range window {
		rows[i] = m.rowCells(e)
	}

	widths := layoutColumnWidths(titles, rows, m.width, tableColumnGap)
	headerStyle := m.st.tableHeader.Padding(0)
	selStyle := m.st.tableSelected.Padding(0)

	out := make([]string, 0, h)
	out = append(out, headerStyle.Render(tableRowText(titles, widths)))
	for i, cells := range rows {
		e := window[i]
		if i == m.cursor-start {
			out = append(out, m.styleRoleImplicationMarker(e, tableRowText(cells, widths), selStyle))
			continue
		}
		if e.kind == entUser && e.user.Service {
			// Service/system accounts recede: the whole row is dimmed (the leading
			// marker from userRowCells flags it).
			out = append(out, m.st.attrs.Render(tableRowText(cells, widths)))
			continue
		}
		if e.kind == entRole && e.role.ImpliesRoles {
			// Composite roles use the same subdued foreground as service/system
			// users; only their ⧉ marker is bold.
			out = append(out, m.styleRoleImplicationMarker(e, tableRowText(cells, widths), m.st.attrs))
			continue
		}
		out = append(out, m.tableDataRow(e, cells, widths, statusCols))
	}
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// tableRowText lays cells into fixed columns with the standard gap, producing an
// unstyled line exactly (sum(widths) + gap*len) wide.
func tableRowText(cells []string, widths []int) string {
	var b strings.Builder
	for j, w := range widths {
		cell := ""
		if j < len(cells) {
			cell = cells[j]
		}
		b.WriteString(truncPad(cell, w))
		b.WriteString(strings.Repeat(" ", tableColumnGap))
	}
	return b.String()
}

// tableDataRow renders a non-selected row, coloring only the status columns.
func (m Model) tableDataRow(e entry, cells []string, widths []int, statusCols map[int]bool) string {
	var b strings.Builder
	for j, w := range widths {
		cell := ""
		if j < len(cells) {
			cell = cells[j]
		}
		text := truncPad(cell, w)
		if statusCols[j] {
			text = m.st.tableCell.Padding(0).Foreground(statusColor(cell)).Render(text)
		}
		b.WriteString(text)
		b.WriteString(strings.Repeat(" ", tableColumnGap))
	}
	return m.styleRoleImplicationMarker(e, b.String(), lipgloss.NewStyle())
}

func (m Model) styleRoleImplicationMarker(e entry, row string, base lipgloss.Style) string {
	const marker = "⧉"
	if e.kind != entRole || !e.role.ImpliesRoles || !strings.HasPrefix(row, marker) {
		return base.Render(row)
	}
	markerStyle := base.Bold(true)
	return markerStyle.Render(marker) + base.Render(strings.TrimPrefix(row, marker))
}

// layoutColumnWidths sizes columns to their natural content width, then
// distributes surplus terminal width in proportion to those natural widths.
// Every column participates, but content-heavy columns receive more room than
// compact state/type/count columns. When space is tight, the widest columns
// shrink first and retain a readable minimum. gap is the inter-column spacing
// counted for every column.
func layoutColumnWidths(titles []string, rows [][]string, total, gap int) []int {
	n := len(titles)
	if n == 0 {
		return nil
	}
	widths := make([]int, n)
	for j, title := range titles {
		widths[j] = runeLen(title)
	}
	for _, cells := range rows {
		for j := 0; j < n && j < len(cells); j++ {
			if w := runeLen(cells[j]); w > widths[j] {
				widths[j] = w
			}
		}
	}
	for j := range widths {
		if widths[j] < 1 {
			widths[j] = 1
		}
	}

	budget := total - gap*n
	if budget < n {
		budget = n // degenerate: at least one column of width 1 each
	}
	sum := 0
	for _, w := range widths {
		sum += w
	}

	const minWidth = 4 // never starve a column below this while others can give
	if sum < budget {
		extra := budget - sum
		naturalTotal := sum
		remainders := make([]int, n)
		allocated := 0
		for j, natural := range widths {
			numerator := extra * natural
			add := numerator / naturalTotal
			widths[j] += add
			allocated += add
			remainders[j] = numerator % naturalTotal
		}
		// Integer division leaves fewer than n cells. Give those cells to the
		// largest fractional shares, with column order as the stable tie-break.
		for cells := extra - allocated; cells > 0; cells-- {
			best := 0
			for j := 1; j < n; j++ {
				if remainders[j] > remainders[best] {
					best = j
				}
			}
			widths[best]++
			remainders[best] = -1
		}
		sum = budget
	}
	for sum > budget { // shrink the widest column above the floor
		mi := -1
		for j := 0; j < n; j++ {
			if widths[j] > minWidth && (mi < 0 || widths[j] > widths[mi]) {
				mi = j
			}
		}
		if mi < 0 {
			break
		}
		widths[mi]--
		sum--
	}
	for sum > budget { // terminal too narrow even at the floor: take from the widest
		mi := 0
		for j := 1; j < n; j++ {
			if widths[j] > widths[mi] {
				mi = j
			}
		}
		if widths[mi] <= 1 {
			break
		}
		widths[mi]--
		sum--
	}
	return widths
}

// truncPad fits s into exactly w display cells, truncating with an ellipsis or
// right-padding with spaces.
func truncPad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > w {
		if w == 1 {
			return "…"
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

func runeLen(s string) int { return len([]rune(s)) }

func (m Model) renderRow(e entry, sel bool) string {
	if e.kind == entGroup {
		heading := " " + m.st.relatedGroup.Render(e.label)
		return m.clip(m.renderIssueCounts(heading, e.issueErrors, e.issueDegraded))
	}
	if e.kind == entUser || e.kind == entDomain || e.kind == entUserGroup || e.kind == entProject ||
		e.kind == entRole || e.kind == entAssignment || e.kind == entService || e.kind == entEndpoint ||
		e.kind == entRegion || e.kind == entInstance || e.kind == entHypervisor || e.kind == entAccelerator {
		return m.renderIdentityRow(e, sel)
	}
	eff := e.oper
	if eff == "" {
		eff = e.prov
	}
	healthy := eff == "ONLINE" || eff == "ACTIVE" || eff == "HEALTHY"
	if e.node != nil && e.node.Type == model.TypeAmphora && eff == "ALLOCATED" {
		healthy = true
	}
	notable := eff != "" && !healthy
	relation := navigationRelation(e)
	target := navigationTarget(e)
	extra := strings.TrimSpace(e.extra)
	if strings.EqualFold(extra, target) {
		extra = ""
	}

	relationWidth := m.navigationRelationWidth()
	relationCell := padRight(relation, relationWidth)
	indent := ""
	if m.isOverview() {
		indent = "  "
	}
	showLBStatuses := relatedLoadBalancerEntry(e)
	if showLBStatuses {
		target = m.fitRelatedLoadBalancerTarget(e, relationCell, extra)
	}
	attrSeparator := "  "
	if m.isOverview() {
		attrSeparator = " · "
	}
	body := relationCell + "  " + target
	if extra != "" {
		body += attrSeparator + extra
	}
	if showLBStatuses {
		if statuses := relatedLoadBalancerStatusPlain(e); statuses != "" {
			body += attrSeparator + statuses
		}
	} else if notable {
		body += attrSeparator + "[" + eff + "]"
	}

	if sel {
		if m.isOverview() {
			return m.renderSelectedOverviewRow(e, eff, relationCell, target, extra, notable, showLBStatuses)
		}
		marker := navigationMarker(e)
		prefix := indent + marker
		prefixWidth := lipgloss.Width(prefix)
		if m.width <= prefixWidth {
			return m.selectedMarkerStyle(e, eff).Width(m.width).Render(clipRunes(prefix, m.width))
		}
		remaining := m.width - prefixWidth
		body = navigationChevron(body, remaining)
		return m.selectedMarkerStyle(e, eff).Render(prefix) +
			m.st.selected.Width(remaining).Render(clipRunes(body, remaining))
	}

	marker := m.styledNavigationMarker(e, eff)
	seg := indent + marker + m.st.panelLabel.Render(relationCell) + "  " + target
	if extra != "" {
		seg += m.st.attrs.Render(attrSeparator + extra)
	}
	if showLBStatuses {
		if relatedLoadBalancerStatusPlain(e) != "" {
			seg += m.st.attrs.Render(attrSeparator) + m.relatedLoadBalancerStatus(e)
		}
	} else if notable {
		seg += m.st.attrs.Render(attrSeparator) + lipgloss.NewStyle().Foreground(statusColor(eff)).Render("["+eff+"]")
	}
	return navigationStyledChevron(seg, m.width, m.st.refMarker)
}

// renderIdentityRow renders an identity-area object (user / domain / group /
// project) as a clean related-object row: a neutral bullet, the object's name,
// and dimmed secondary facts, with a navigability chevron. The row drops the
// "type:" prefix carried by the label — the group heading (DOMAIN, USERS, …)
// already names the type — and omits the edge/relation chrome used by graph
// rows. These rows only appear as related objects, always in an overview, so
// selection uses the overview's ▶ + bold style.
func (m Model) renderIdentityRow(e entry, sel bool) string {
	// Assignment rows keep the actor's type prefix (user:/group:) since a section
	// mixes both; the other identity rows drop it (their heading names the type).
	name := e.label
	if e.kind != entAssignment {
		name = identityRowName(e.label)
	}
	extra := strings.TrimSpace(e.extra)
	status := e.oper
	if status == "" {
		status = e.prov
	}
	if sel {
		seg := m.st.refMarker.Render("▶ ") + m.styledNavigationMarker(e, status) + lipgloss.NewStyle().Bold(true).Render(name)
		if extra != "" {
			seg += m.styledIdentityExtra(e, extra)
		}
		return navigationStyledChevron(seg, m.width, m.st.refMarker)
	}
	seg := "  " + m.styledNavigationMarker(e, status) + name
	if extra != "" {
		seg += m.styledIdentityExtra(e, extra)
	}
	return navigationStyledChevron(seg, m.width, m.st.refMarker)
}

// styledIdentityExtra highlights broad direct and effective role-assignment
// targets without recoloring the actor/role or the row's selection chrome.
func (m Model) styledIdentityExtra(e entry, extra string) string {
	style := m.st.attrs
	if e.kind == entAssignment && e.assignmentPivot != pivotTarget {
		if color, ok := assignmentScopeColor(e.assignment.TargetType); ok {
			style = lipgloss.NewStyle().Foreground(color)
		}
	}
	return m.st.attrs.Render(" · ") + style.Render(extra)
}

// identityRowName strips the leading "type:" from an identity label (e.g.
// "domain:Default" → "Default"). Only the first colon is removed, so names that
// themselves contain a colon survive.
func identityRowName(label string) string {
	if i := strings.IndexByte(label, ':'); i >= 0 {
		return label[i+1:]
	}
	return label
}

func (m Model) renderSelectedOverviewRow(e entry, status, relationCell, target, extra string, notable, showLBStatuses bool) string {
	seg := m.st.refMarker.Render("▶ ") + m.styledNavigationMarker(e, status) +
		m.st.panelLabel.Bold(true).Render(relationCell) + "  " + lipgloss.NewStyle().Bold(true).Render(target)
	if extra != "" {
		seg += m.st.attrs.Render(" · " + extra)
	}
	if showLBStatuses {
		if relatedLoadBalancerStatusPlain(e) != "" {
			seg += m.st.attrs.Render(" · ") + m.relatedLoadBalancerStatus(e)
		}
	} else if notable {
		seg += m.st.attrs.Render(" · ") + lipgloss.NewStyle().Foreground(statusColor(status)).Render("["+status+"]")
	}
	return navigationStyledChevron(seg, m.width, m.st.refMarker)
}

func (m Model) styledNavigationMarker(e entry, status string) string {
	switch e.kind {
	case entRef:
		return m.st.refMarker.Render("→ ")
	case entBackRef:
		return m.st.backRefMarker.Render("← ")
	case entAssignment:
		// A role assignment marks whether the grant is held directly (solid ●) or
		// inherited via a group or a parent/domain scope (hollow ○). Token-derived
		// effective roles use ◆ because their origin is not present in the token.
		glyph := "●"
		if e.assignment.TokenScoped {
			glyph = "◆"
		} else if e.assignment.Inherited {
			glyph = "○"
		}
		return lipgloss.NewStyle().Foreground(statusColor(status)).Render(glyph) + " "
	default:
		return lipgloss.NewStyle().Foreground(statusColor(status)).Render("●") + " "
	}
}

func relatedLoadBalancerEntry(e entry) bool {
	return e.kind == entRelated && e.node != nil && e.node.Type == model.TypeLoadBalancer
}

func relatedLoadBalancerStatusPlain(e entry) string {
	var parts []string
	if e.oper != "" {
		parts = append(parts, e.oper)
	}
	if e.prov != "" {
		parts = append(parts, e.prov)
	}
	return strings.Join(parts, ", ")
}

// fitRelatedLoadBalancerTarget reserves the diagnostic suffix before sizing the
// target. Only the human name is shortened; the short ID and both statuses stay
// visible whenever the fixed row chrome itself fits the terminal.
func (m Model) fitRelatedLoadBalancerTarget(e entry, relationCell, extra string) string {
	if e.node == nil {
		return navigationTarget(e)
	}
	name := e.node.Name
	hasName := name != ""
	if !hasName {
		name = shortID(e.node.ID)
	}
	idSuffix := ""
	if hasName && e.node.ID != "" {
		idSuffix = " (" + shortID(e.node.ID) + ")"
	}
	full := name + idSuffix

	// Selection/status marker (4), relationship, target gap (2), trailing open
	// chevron (3), then the summary and status separators.
	fixed := 4 + lipgloss.Width(relationCell) + 2 + 3
	if extra != "" {
		fixed += 3 + lipgloss.Width(extra)
	}
	if statuses := relatedLoadBalancerStatusPlain(e); statuses != "" {
		fixed += 3 // " · "
		fixed += lipgloss.Width(statuses)
	}
	available := m.width - fixed
	if available >= lipgloss.Width(full) {
		return full
	}
	if available <= 0 {
		return ""
	}
	suffixWidth := lipgloss.Width(idSuffix)
	if idSuffix == "" {
		return clipRunes(full, available)
	}
	if available <= suffixWidth {
		// When separators consume the final cell that would otherwise hold part
		// of the name, compact the separating space before sacrificing the ID.
		compactSuffix := strings.TrimSpace(idSuffix)
		if available > lipgloss.Width(compactSuffix) {
			return clipRunes("…"+compactSuffix, available)
		}
		return clipRunes(compactSuffix, available)
	}
	nameWidth := available - suffixWidth
	if nameWidth == 1 && lipgloss.Width(name) > 1 {
		return "…" + idSuffix
	}
	return clipRunes(name, nameWidth) + idSuffix
}

func (m Model) relatedLoadBalancerStatus(e entry) string {
	var parts []string
	if e.oper != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(statusColor(e.oper)).Render(e.oper))
	}
	if e.prov != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(statusColor(e.prov)).Render(e.prov))
	}
	return strings.Join(parts, m.st.attrs.Render(", "))
}

func (m Model) selectedMarkerStyle(e entry, status string) lipgloss.Style {
	var color lipgloss.TerminalColor = statusColor(status)
	switch e.kind {
	case entRef:
		color = m.st.refMarker.GetForeground()
	case entBackRef:
		color = m.st.backRefMarker.GetForeground()
	}
	return m.st.selected.Foreground(color)
}

func (m Model) renderIssueCounts(base string, errors, degraded int) string {
	if errors > 0 {
		base += m.st.statusBar.Render(" · ") + lipgloss.NewStyle().Bold(true).Foreground(statusColor("ERROR")).Render(fmt.Sprintf("ERROR %d", errors))
	}
	if degraded > 0 {
		base += m.st.statusBar.Render(" · ") + lipgloss.NewStyle().Bold(true).Foreground(statusColor("DEGRADED")).Render(fmt.Sprintf("DEGRADED %d", degraded))
	}
	return base
}

// navigationRelation returns the stable left-hand label for a resource link.
// Containment rows use the target type; graph edges use the relationship name
// while their marker communicates the edge direction.
func navigationRelation(e entry) string {
	switch e.kind {
	case entRef, entBackRef:
		if e.relationship != "" {
			return upperFirst(e.relationship)
		}
		if e.edge != nil {
			return nodeTypeLabel(e.edge.TargetType)
		}
	case entChild, entRelated:
		if e.node != nil {
			if e.node.Type == model.TypeVIP {
				if e.node.Attrs["vip_kind"] == "additional" {
					return "Additional VIP"
				}
				return "Primary VIP"
			}
			return nodeTypeLabel(e.node.Type)
		}
	}
	return "Resource"
}

// navigationTarget returns the identity or summary users are navigating to.
// Child rows don't repeat their type prefix because it already occupies the
// relationship column; reference targets retain it to disambiguate graph jumps.
func navigationTarget(e entry) string {
	if (e.kind == entChild || e.kind == entRelated) && e.node != nil {
		target := e.node.Name
		if target == "" {
			target = shortID(e.node.ID)
		}
		if e.node.Type == model.TypeVIP {
			if floatingIP := e.node.Attrs["floating_ip"]; floatingIP != "" {
				target += " (" + floatingIP + ")"
			}
		}
		if e.node.Type == model.TypePool && e.showID {
			target += " (" + shortID(e.node.ID) + ")"
		}
		if e.node.Type == model.TypeAmphora {
			if role := e.node.Attrs["role"]; role != "" {
				target += " (" + role + ")"
			}
		}
		if e.kind == entRelated && e.node.Type == model.TypeLoadBalancer && e.node.Name != "" && e.node.ID != "" {
			target += " (" + shortID(e.node.ID) + ")"
		}
		return target
	}
	return e.label
}

func nodeTypeLabel(t model.NodeType) string {
	switch t {
	case model.TypeLoadBalancer:
		return "Load balancer"
	case model.TypeVIP:
		return "VIP"
	case model.TypeFloatingIP:
		return "Floating IP"
	case model.TypeListener:
		return "Listener"
	case model.TypePool:
		return "Pool"
	case model.TypeMember:
		return "Member"
	case model.TypeHealthMonitor:
		return "Health monitor"
	case model.TypeL7Policy:
		return "L7 policy"
	case model.TypeL7Rule:
		return "L7 rule"
	case model.TypeAmphora:
		return "Amphora"
	case model.TypeInstance:
		return "Instance"
	case model.TypeHypervisor:
		return "Hypervisor"
	case model.TypeCOECluster:
		return "COE cluster"
	case model.TypeKubeService:
		return "Kubernetes service"
	default:
		return upperFirst(string(t))
	}
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func navigationMarker(e entry) string {
	switch e.kind {
	case entRef:
		return "→ "
	case entBackRef:
		return "← "
	default:
		return "● "
	}
}

// navigationRelationWidth aligns heterogeneous resource links without giving
// them table semantics. The cap preserves useful target space in narrow views.
func (m Model) navigationRelationWidth() int {
	w := 0
	for _, e := range m.entries {
		if !e.selectable() {
			continue
		}
		if n := len([]rune(navigationRelation(e))); n > w {
			w = n
		}
	}
	if w < 12 {
		w = 12
	}
	cap := m.width / 3
	if cap < 1 {
		cap = 1
	}
	if w > cap {
		w = cap
	}
	return w
}

func padRight(s string, width int) string {
	s = clipRunes(s, width)
	padding := width - len([]rune(s))
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

// navigationChevron keeps the open affordance next to the row content. A
// terminal-wide gap makes heterogeneous resource links look like a table.
func navigationChevron(s string, width int) string {
	if width <= 0 {
		return s + "  ›"
	}
	if width == 1 {
		return "›"
	}
	if width == 2 {
		return " ›"
	}
	s = clipRunes(s, width-3)
	return s + "  ›"
}

func navigationStyledChevron(s string, width int, chevronStyle lipgloss.Style) string {
	if width <= 0 {
		return s + "  " + chevronStyle.Render("›")
	}
	if width == 1 {
		return chevronStyle.Render("›")
	}
	if width == 2 {
		return " " + chevronStyle.Render("›")
	}
	if width == 3 {
		return "  " + chevronStyle.Render("›")
	}
	s = lipgloss.NewStyle().MaxWidth(width - 3).Render(s)
	return s + "  " + chevronStyle.Render("›")
}

func (m Model) flashLine() string {
	if m.flash == "" {
		return ""
	}
	st := m.st.flash
	if m.flashErr {
		st = m.st.flashErr
	}
	return m.clip(st.Render(m.flash))
}

func (m Model) hintLine() string {
	if m.filtering {
		return m.clip(m.st.help.Render("type to filter · enter apply · esc clear"))
	}
	parts := []string{
		"enter open", "←/esc/→ hist", "space area", "1-9 views", "y/j raw", "i/n copy",
	}
	if m.loc.isTopLevelList() {
		parts = append(parts, "d names/ids", "o sort")
	}
	if m.canOpenRoleTree() {
		parts = append(parts, "t inheritance tree")
	}
	if m.isHypervisorOverview() {
		parts = append(parts, "f CPU features")
	}
	if hasFilterableEntries(m.allEntries) {
		parts = append(parts, "/ filter")
	}
	if hasStatusEntries(m.allEntries) {
		parts = append(parts, "s status")
	}
	parts = append(parts, "tab scope", "r refresh", "a auto")
	if m.isStatsOverview() {
		parts = append(parts, "+/- interval")
	}
	parts = append(parts, "h history", "# telemetry", "? help", "q quit")
	return m.clip(m.st.help.Render(strings.Join(parts, " · ")))
}

// --- overlays -------------------------------------------------------------

func (m *Model) setupHelpViewport() {
	m.vp.Width = m.width
	m.vp.Height = m.height - 2
	m.vp.SetContent(helpContent(
		m.loc.isTopLevelList(),
		hasFilterableEntries(m.allEntries),
		hasStatusEntries(m.allEntries),
		m.isStatsOverview(),
	))
	m.vp.GotoTop()
}

// scrollMarkers returns the edge hints for the shared overlay viewport: an
// arrow toward off-screen content plus the scroll percentage. The "above" hint
// goes on the title line, the "below" hint on the footer. Both are empty when
// the content fits without scrolling.
func (m Model) scrollMarkers() (above, below string) {
	if m.vp.TotalLineCount() <= m.vp.VisibleLineCount() {
		return "", ""
	}
	// Rendered as reversed, padded chips (the panel-header style) so they stand
	// out against the title/footer chrome.
	if !m.vp.AtTop() {
		above = m.st.panelTitle.Render("▲ more")
	}
	pct := int(m.vp.ScrollPercent()*100 + 0.5)
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	label := fmt.Sprintf("%d%%", pct)
	if !m.vp.AtBottom() {
		label += " ▼ more"
	}
	return above, m.st.panelTitle.Render(label)
}

// listScrollMarkers returns the edge hints for the main list — the m.entries
// scroll region shared by top-level tables, node lists, and overview related
// lists. "▲ more" shows when rows are scrolled off the top, "▼ more" when rows
// remain below. Both are empty when the list fits (or has no rows).
func (m Model) listScrollMarkers() (above, below string) {
	total := len(m.entries)
	if total == 0 {
		return "", ""
	}
	visible := m.listContentRows()
	if total <= visible {
		return "", ""
	}
	// "above" reflects genuinely hidden content, not the raw scroll offset: in a
	// grouped list the first content row sits at index 1 (after its heading) and
	// that heading is pinned (sticky), so m.top settles at 1 at the visual top.
	// Keying off a hidden *selectable* row instead clears the hint at the top.
	if m.hasSelectableBefore(m.top) {
		above = m.st.panelTitle.Render("▲ more")
	}
	if m.top+visible < total {
		below = m.st.panelTitle.Render("▼ more")
	}
	return above, below
}

// hasSelectableBefore reports whether any selectable (non-heading) row is hidden
// above the given scroll offset — i.e. there is real content to scroll up to.
func (m Model) hasSelectableBefore(top int) bool {
	if top > len(m.entries) {
		top = len(m.entries)
	}
	for i := 0; i < top; i++ {
		if m.entries[i].selectable() {
			return true
		}
	}
	return false
}

// wideScreenWidth is the terminal width at/above which scroll markers are
// mirrored on both edges of their line — on a wide row a single right-aligned
// marker sits far from the left-aligned content and is easy to miss.
const wideScreenWidth = 100

// edgeMarkerLine places a scroll marker on a line: mirrored on both the left and
// right edges when the terminal is wide, otherwise just right-aligned (via
// rightMarkerLine). An empty marker leaves the line as-is.
func (m Model) edgeMarkerLine(left, marker string) string {
	if marker == "" {
		return m.clip(left)
	}
	mw := lipgloss.Width(marker)
	if m.width < wideScreenWidth || m.width <= 2*mw+3 {
		return m.rightMarkerLine(left, marker)
	}
	prefix := marker + " "
	inner := lipgloss.NewStyle().MaxWidth(m.width - 2*mw - 3).Render(left)
	pad := m.width - lipgloss.Width(prefix) - lipgloss.Width(inner) - mw
	if pad < 1 {
		pad = 1
	}
	return prefix + inner + strings.Repeat(" ", pad) + marker
}

// rightMarkerLine right-aligns marker on a line, clipping the left content as
// needed so the marker always fits. Unlike scrollLine (which drops the marker on
// a narrow line), this keeps the marker — the scroll hint is the point — at the
// cost of trimming the line's own content. An empty marker leaves the line as-is.
func (m Model) rightMarkerLine(left, marker string) string {
	if marker == "" {
		return m.clip(left)
	}
	mw := lipgloss.Width(marker)
	if m.width <= mw {
		return m.clip(marker)
	}
	left = lipgloss.NewStyle().MaxWidth(m.width - mw - 1).Render(left)
	pad := m.width - lipgloss.Width(left) - mw
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + marker
}

// scrollLine right-aligns a scroll marker on an overlay title/footer line,
// dropping the marker (never the content) when the line is too narrow.
func (m Model) scrollLine(left, marker string) string {
	if marker == "" {
		return m.clip(left)
	}
	lw, rw := lipgloss.Width(left), lipgloss.Width(marker)
	if lw+1+rw > m.width {
		return m.clip(left)
	}
	return left + strings.Repeat(" ", m.width-lw-rw) + marker
}

func (m Model) helpView() string {
	title := m.st.overlayTitle.Render("OLB — OpenStack Live Browser — help")
	footer := m.st.help.Render("esc / ? / q  close   ·   ↑/↓ scroll")
	above, below := m.scrollMarkers()
	return m.scrollLine(title, above) + "\n" + m.vp.View() + "\n" + m.scrollLine(footer, below)
}

func (m *Model) setupRawViewport() {
	m.rawTitle = ""
	m.vp.Width = m.width
	m.vp.Height = m.height - 2
	m.vp.SetContent(m.rawContent)
	m.vp.GotoTop()
}

func (m *Model) setupRawViewportTitle(title string) {
	m.rawTitle = title
	m.vp.Width = m.width
	m.vp.Height = m.height - 2
	m.vp.SetContent(m.rawContent)
	m.vp.GotoTop()
}

func (m Model) rawView() string {
	title := m.rawTitle
	if title == "" {
		obj := "object"
		if m.loc.node != nil {
			obj = m.loc.node.Label()
		}
		title = "raw " + strings.ToUpper(m.rawFormat) + " — " + obj
	}
	footer := m.flashLine()
	if footer == "" {
		footer = m.st.help.Render("esc/q close · c copy · ↑/↓ scroll")
	}
	above, below := m.scrollMarkers()
	return m.scrollLine(m.st.overlayTitle.Render(title), above) + "\n" + m.vp.View() + "\n" + m.scrollLine(footer, below)
}

func (m Model) scopeView() string {
	return overlayCenter(m.selectorBaseView(), m.scopeModalBox(), m.width, m.height)
}

func (m Model) scopeModalBox() string {
	const title = "SWITCH AUTHENTICATION SCOPE"
	footer := "enter select · arrows/page/home/end move · / filter · esc/q cancel"
	if m.search.Focused() {
		footer = "type to filter · enter apply · esc clear"
	} else if m.loading && m.loadingWhat == "switching authentication scope" {
		footer = m.spinner.View() + " switching authentication scope…"
	}

	scopes := m.filteredScopes()
	items := scopeSelectorItems(scopes)
	labels := make([]string, 0, len(items)+2)
	for _, item := range items {
		label := item.label
		if !item.header && scopes[item.scopeIndex].Equal(m.scope) {
			label += " (current)"
		}
		labels = append(labels, "      "+label)
	}
	if m.loading && len(m.scopes) == 0 {
		labels = append(labels, "  loading available scopes…")
	}
	if m.scopeError != "" {
		labels = append(labels, m.scopeError)
	}
	width := m.selectorModalWidth(title, footer, labels)
	errorLines := m.scopeErrorLines(width)
	errorRows := len(errorLines)
	if errorRows > 0 {
		errorRows++ // blank separator before the in-view error
	}
	maxRows := m.selectorModalRowCapacity(errorRows)

	var lines []string
	selectedDisplay := 0
	for i, item := range items {
		if !item.header && item.scopeIndex == m.scopeCursor {
			selectedDisplay = i
			break
		}
	}
	start, end := selectorWindow(len(items), selectedDisplay, maxRows)
	above, below := m.selectorScrollMarkers(start, end, len(items))
	lines = append(lines, modalMarkerLine(m.selectorTitleLine(title, width), above, width))
	lines = append(lines, m.modalContentLine("", width))

	if m.loading && len(m.scopes) == 0 {
		lines = append(lines, m.modalContentLine(m.spinner.View()+" loading available scopes…", width))
	} else {
		for i := start; i < end; i++ {
			item := items[i]
			if item.header {
				lines = append(lines, m.modalContentLine(m.st.relatedGroup.Render(item.label), width))
				continue
			}
			scope := scopes[item.scopeIndex]
			label := item.label
			if scope.Equal(m.scope) {
				label += m.st.relationship.Render(" (current)")
			}
			if item.scopeIndex == m.scopeCursor {
				lines = append(lines, m.st.selected.Width(width).MaxWidth(width).Render("    ▸ "+label))
			} else {
				lines = append(lines, m.modalContentLine("      "+label, width))
			}
		}
	}
	if len(scopes) == 0 && !(m.loading && len(m.scopes) == 0) {
		empty := "— no available scopes —"
		if m.search.Value() != "" {
			empty = "— no matching scopes —"
		}
		lines = append(lines, m.modalContentLine("  "+m.st.disabled.Render(empty), width))
	}
	if len(errorLines) > 0 {
		lines = append(lines, m.modalContentLine("", width))
		for _, line := range errorLines {
			lines = append(lines, m.modalContentLine(m.st.flashErr.Render("  "+line), width))
		}
	}
	lines = append(lines, m.modalContentLine("", width))
	lines = append(lines, modalMarkerLine(m.st.modalHelp.Render(footer), below, width))
	return m.st.modalFrame.Render(strings.Join(lines, "\n"))
}

func (m Model) scopeErrorLines(width int) []string {
	if m.scopeError == "" {
		return nil
	}
	width -= 4
	if width < 1 {
		width = 1
	}
	return strings.Split(ansi.Wrap(m.scopeError, width, " "), "\n")
}

// filteredSwitcherRows returns the switcher rows matching the active filter (a
// substring of the "<area> › <view>" label), or all rows when the filter is
// empty.
func (m Model) filteredSwitcherRows() []switcherRow {
	all := switcherRows()
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))
	if q == "" {
		return all
	}
	out := make([]switcherRow, 0, len(all))
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.label), q) {
			out = append(out, r)
		}
	}
	return out
}

// switcherItem is one rendered line of the switcher: either a non-selectable
// area heading or a selectable view row. viewIdx indexes the filtered view rows
// (the space m.switchCursor moves through); it is -1 for a heading. key is the
// area's uppercase accelerator, shown as a chip on the heading.
type switcherItem struct {
	header  bool
	key     rune
	label   string
	viewIdx int
}

// switcherItems interleaves area headings with their view rows, so the overlay
// reads like the related-objects list: an uppercase group heading with a count,
// then the views beneath it. Headings appear only for areas that still have a
// matching row, so filtering drops empty groups.
func (m Model) switcherItems(rows []switcherRow) []switcherItem {
	counts := map[areaKind]int{}
	for _, r := range rows {
		counts[r.area]++
	}
	items := make([]switcherItem, 0, len(rows)+len(areas))
	first := true
	var lastArea areaKind
	for i, r := range rows {
		if first || r.area != lastArea {
			area := areaByKind(r.area)
			title := strings.ToUpper(area.label)
			items = append(items, switcherItem{header: true, key: area.key, label: fmt.Sprintf("%s %d", title, counts[r.area]), viewIdx: -1})
			lastArea, first = r.area, false
		}
		items = append(items, switcherItem{label: r.view.rootLabel(), viewIdx: i})
	}
	return items
}

func (m Model) switcherView() string {
	return overlayCenter(m.selectorBaseView(), m.switcherModalBox(), m.width, m.height)
}

func (m Model) switcherModalBox() string {
	const title = areaSwitcherTitle
	rows := m.filteredSwitcherRows()
	items := m.switcherItems(rows)
	footer := areaSwitcherFooter()
	if m.search.Focused() {
		footer = "type to filter · enter apply · esc clear"
	}

	labels := make([]string, 0, len(items))
	for _, item := range items {
		if item.header {
			labels = append(labels, " "+string(item.key)+"  "+item.label)
			continue
		}
		label := item.label
		if rows[item.viewIdx].view == m.activeWorkspace {
			label += " (current)"
		}
		labels = append(labels, "      "+label)
	}
	width := m.selectorModalWidth(title, footer, labels)

	// Window on the selected view's display line so its heading stays in view.
	selDisp := 0
	for i, it := range items {
		if !it.header && it.viewIdx == m.switchCursor {
			selDisp = i
			break
		}
	}

	maxRows := m.selectorModalRowCapacity(0)
	start, end := selectorWindow(len(items), selDisp, maxRows)
	above, below := m.selectorScrollMarkers(start, end, len(items))
	lines := []string{
		modalMarkerLine(m.selectorTitleLine(title, width), above, width),
		m.modalContentLine("", width),
	}
	for i := start; i < end; i++ {
		it := items[i]
		if it.header {
			chip := m.st.panelTitle.Render(" " + string(it.key) + " ")
			lines = append(lines, m.modalContentLine(chip+" "+m.st.relatedGroup.Render(it.label), width))
			continue
		}
		label := it.label
		if rows[it.viewIdx].view == m.activeWorkspace {
			label += m.st.relationship.Render(" (current)")
		}
		if it.viewIdx == m.switchCursor {
			lines = append(lines, m.st.selected.Width(width).MaxWidth(width).Render("    ▸ "+label))
		} else {
			lines = append(lines, m.modalContentLine("      "+label, width))
		}
	}
	if len(rows) == 0 && m.search.Value() != "" {
		lines = append(lines, m.modalContentLine("  "+m.st.disabled.Render("— no matching views —"), width))
	}
	lines = append(lines, m.modalContentLine("", width))
	lines = append(lines, modalMarkerLine(m.st.modalHelp.Render(footer), below, width))
	return m.st.modalFrame.Render(strings.Join(lines, "\n"))
}

func (m Model) selectorTitleLine(title string, width int) string {
	renderedTitle := m.st.modalTitle.Render(title)
	query := m.search.Value()
	if !m.search.Focused() && query == "" {
		return modalMarkerLine(renderedTitle, "", width)
	}
	separator := m.st.crumbSep.Render("  ")
	if m.search.Focused() {
		inputWidth := width - lipgloss.Width(renderedTitle) - lipgloss.Width(separator) - lipgloss.Width(m.search.Prompt)
		if inputWidth < 1 {
			inputWidth = 1
		}
		search := m.search
		search.Width = inputWidth
		return modalMarkerLine(renderedTitle+separator+search.View(), "", width)
	}
	return modalMarkerLine(renderedTitle+separator+m.st.statusBar.Render("filter: "+query), "", width)
}

func (m Model) selectorBaseView() string {
	if m.home {
		return m.homeView()
	}
	return m.listView()
}

const areaSwitcherTitle = "SWITCH AREA / VIEW"

func areaSwitcherFooter() string {
	return "enter select · " + strings.Join(areaKeyStrings(), "/") + " jump to area · arrows/page move · / filter · esc/q cancel"
}

func (m Model) areaSwitcherBaselineWidth() int {
	return m.constrainModalWidth(max(
		lipgloss.Width(areaSwitcherTitle),
		lipgloss.Width(areaSwitcherFooter()),
	))
}

func (m Model) selectorModalWidth(title, footer string, labels []string) int {
	width := max(lipgloss.Width(title), lipgloss.Width(footer))
	if query := m.search.Value(); query != "" || m.search.Focused() {
		if w := lipgloss.Width(title + "  " + m.search.Prompt + query); w > width {
			width = w
		}
	}
	for _, label := range labels {
		if w := lipgloss.Width(label); w > width {
			width = w
		}
	}
	return m.constrainModalWidth(width)
}

func (m Model) constrainModalWidth(width int) int {
	if available := m.width - m.st.modalFrame.GetHorizontalFrameSize() - 2; available > 0 && width > available {
		width = available
	}
	if width < 1 {
		width = 1
	}
	return width
}

func (m Model) selectorModalRowCapacity(extraRows int) int {
	frameHeight := m.st.modalFrame.GetVerticalFrameSize()
	maxOuterHeight := m.height * 3 / 4
	minOuterHeight := frameHeight + 5 + extraRows // title + blank + one row + blank + footer
	if maxOuterHeight < minOuterHeight {
		maxOuterHeight = minOuterHeight
	}
	if available := m.height - 2; available > 0 && maxOuterHeight > available {
		maxOuterHeight = available
	}
	rows := maxOuterHeight - frameHeight - 4 - extraRows
	if rows < 1 {
		return 1
	}
	return rows
}

func (m Model) overlayPageSize() int {
	return m.selectorModalRowCapacity(0)
}

func selectorWindow(total, selected, capacity int) (start, end int) {
	if capacity < 1 {
		capacity = 1
	}
	if selected >= capacity {
		start = selected - capacity + 1
	}
	end = start + capacity
	if end > total {
		end = total
	}
	return start, end
}

func (m Model) selectorScrollMarkers(start, end, total int) (above, below string) {
	if start > 0 {
		above = m.st.panelTitle.Render("▲ more")
	}
	if end < total {
		below = m.st.panelTitle.Render("▼ more")
	}
	return above, below
}

func (m Model) modalContentLine(content string, width int) string {
	content = lipgloss.NewStyle().MaxWidth(width).Render(content)
	return m.st.modalRow.Width(width).MaxWidth(width).Render(content)
}

type scopeSelectorItem struct {
	header     bool
	label      string
	scopeIndex int
}

func scopeSelectorItems(scopes []osclient.ScopeInfo) []scopeSelectorItem {
	items := make([]scopeSelectorItem, 0, len(scopes)+4)
	addGroup := func(header string, indexes []int) {
		if len(indexes) == 0 {
			return
		}
		items = append(items, scopeSelectorItem{header: true, label: header, scopeIndex: -1})
		for _, index := range indexes {
			scope := scopes[index]
			label := scope.Label()
			if scope.Kind == osclient.ScopeSystem {
				label = "system:" + scope.Label()
			}
			items = append(items, scopeSelectorItem{label: label, scopeIndex: index})
		}
	}

	var system, domain []int
	projectGroups := make(map[string][]int)
	projectOrder := make([]string, 0)
	for i, scope := range scopes {
		switch scope.Kind {
		case osclient.ScopeSystem:
			system = append(system, i)
		case osclient.ScopeDomain:
			domain = append(domain, i)
		case osclient.ScopeProject:
			group := scope.DomainName
			if group == "" {
				group = scope.DomainID
			}
			if group == "" {
				group = "unknown"
			}
			if _, exists := projectGroups[group]; !exists {
				projectOrder = append(projectOrder, group)
			}
			projectGroups[group] = append(projectGroups[group], i)
		}
	}
	addGroup("SYSTEM", system)
	addGroup("DOMAINS", domain)
	for _, group := range projectOrder {
		addGroup("PROJECTS · domain: "+group, projectGroups[group])
	}
	return items
}

func (m Model) filteredScopes() []osclient.ScopeInfo {
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))
	if q == "" {
		return m.scopes
	}
	out := make([]osclient.ScopeInfo, 0, len(m.scopes))
	for _, scope := range m.scopes {
		haystack := strings.Join([]string{
			string(scope.Kind), scope.ID, scope.Name, scope.DomainID, scope.DomainName,
		}, " ")
		if strings.Contains(strings.ToLower(haystack), q) {
			out = append(out, scope)
		}
	}
	return out
}

func (m Model) currentScopeCursor() int {
	for i, scope := range m.filteredScopes() {
		if scope.Equal(m.scope) {
			return i
		}
	}
	return 0
}

type pickItem struct {
	index   int
	label   string
	dead    bool
	current bool
}

func (m Model) pickerItems() []pickItem {
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))
	var items []pickItem
	for i, e := range m.hist.entries {
		label := ""
		if e.id.IsTopLevelList() {
			label = listKindOf(e.id).rootLabel()
		} else {
			label = e.id.Label
			if label == "" {
				label = string(e.id.Type) + ":" + shortID(e.id.ID)
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(label), q) {
			continue
		}
		items = append(items, pickItem{index: i, label: label, dead: e.dead, current: i == m.hist.cursor})
	}
	return items
}

func (m Model) pickerView() string {
	title := m.st.overlayTitle.Render("History · " + m.activeWorkspace.rootLabel())
	items := m.pickerItems()
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	searchLine := ""
	if m.search.Focused() {
		searchLine = m.search.View()
	} else if query := m.search.Value(); query != "" {
		searchLine = m.st.statusBar.Render("filter: " + query)
	}
	b.WriteString(searchLine)
	b.WriteString("\n\n")
	maxRows := m.pickerPageSize()
	start := 0
	if m.pickCursor >= maxRows {
		start = m.pickCursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(items) {
		end = len(items)
	}
	if len(items) == 0 {
		b.WriteString("  ")
		b.WriteString(m.st.disabled.Render("— no history —"))
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		it := items[i]
		label := it.label
		selectedLabel := it.label
		if it.dead {
			label = m.st.dead.Render(it.label)
		}
		if it.current {
			label += m.st.relationship.Render(" (here)")
			selectedLabel += " (here)"
		}
		if it.dead {
			label += m.st.relationship.Render(" (deleted)")
			selectedLabel += " (deleted)"
		}
		if i == m.pickCursor {
			b.WriteString(m.st.selected.Width(m.width).Render(clipRunes("▸ "+selectedLabel, m.width)))
			b.WriteString("\n")
		} else {
			b.WriteString("  ")
			b.WriteString(m.clip(label))
			b.WriteString("\n")
		}
	}
	footer := "enter jump · arrows/page/home/end move · / filter · esc cancel"
	if m.search.Focused() {
		footer = "enter apply · esc clear · type to filter"
	}
	b.WriteString("\n")
	b.WriteString(m.st.help.Render(footer))
	return b.String()
}

func (m Model) pickerPageSize() int {
	rows := m.height - 7
	if rows < 1 {
		return 1
	}
	return rows
}

// sortView renders the sort-column picker as a compact pop-up centered over the
// list, so the list it will reorder stays visible behind the modal.
func (m Model) sortView() string {
	return overlayCenter(m.listView(), m.sortModalBox(), m.width, m.height)
}

// sortModalBox builds the bordered pop-up panel: a title, one row per sortable
// column (the active column marked, the highlighted row barred), and a footer.
// Every inner line is rendered at the same width so the dark panel background
// fills uniformly. The sort is always ascending.
func (m Model) sortModalBox() string {
	cols := m.sortColumns()
	title := "Sort · " + m.activeWorkspace.rootLabel()
	footer := "↑/↓ move · enter select · esc cancel"

	labels := make([]string, len(cols))
	iw := max(lipgloss.Width(title), lipgloss.Width(footer))
	for i, c := range cols {
		label := c.label
		if c.key == m.sortKey {
			label += " (active)"
		}
		labels[i] = label
		if w := lipgloss.Width("▸ " + label); w > iw {
			iw = w
		}
	}

	lines := []string{m.st.modalTitle.Width(iw).Render(title), m.st.modalRow.Width(iw).Render("")}
	for i, label := range labels {
		if i == m.sortCursor {
			lines = append(lines, m.st.selected.Width(iw).Render("▸ "+label))
		} else {
			lines = append(lines, m.st.modalRow.Width(iw).Render("  "+label))
		}
	}
	lines = append(lines, m.st.modalRow.Width(iw).Render(""), m.st.modalHelp.Width(iw).Render(footer))
	return m.st.modalFrame.Render(strings.Join(lines, "\n"))
}

// overlayCenter composites box centered over base (each sized to width×height).
// Cutting is ANSI-aware so the base's styling is preserved on either side of the
// box, with resets around the box so its background cannot bleed. A box that
// does not fit falls back to being placed on a blank backdrop.
func overlayCenter(base, box string, width, height int) string {
	if width <= 0 || height <= 0 {
		return box
	}
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		if w := ansi.StringWidth(l); w > bw {
			bw = w
		}
	}
	bh := len(boxLines)
	if bw >= width || bh >= height {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
	}

	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	baseLines = baseLines[:height]

	top := (height - bh) / 2
	left := (width - bw) / 2
	for i, bl := range boxLines {
		row := top + i
		leftPart := ansi.Cut(baseLines[row], 0, left)
		if pad := left - ansi.StringWidth(leftPart); pad > 0 {
			leftPart += strings.Repeat(" ", pad)
		}
		rightPart := ansi.Cut(baseLines[row], left+bw, width)
		baseLines[row] = leftPart + "\x1b[0m" + bl + "\x1b[0m" + rightPart
	}
	return strings.Join(baseLines, "\n")
}

// --- helpers --------------------------------------------------------------

func (m Model) clip(s string) string {
	if m.width <= 0 {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(s)
}

func clipRunes(s string, w int) string {
	if w <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func marshalRaw(v any, format string) string {
	if v == nil {
		return "(no raw object)"
	}
	switch format {
	case "json":
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "error: " + err.Error()
		}
		return string(out)
	default:
		out, err := yaml.Marshal(v)
		if err != nil {
			return "error: " + err.Error()
		}
		return string(out)
	}
}

func helpContent(showNameIDToggle, showFilter, showStatusFilter, showStatsIntervalControls bool) string {
	content := strings.TrimLeft(`
OLB — Browse your OpenStack cloud live.

Move
  ↑ / ↓            selection up / down
  PgUp / PgDn      page up / down
  Home / End       top / bottom

Navigate
  enter            open selected — drill into a child or follow a reference edge
  ← / esc / ⌫      back (history)      → forward (history)
  ctrl+home        jump to the active view's pinned root history entry
  h                history picker overlay

Areas & views (drill into an item to open its detail)
  `+"`"+`                overview / home landing — scope, identity, areas (opens here)
  space            switch area / view — searchable overlay
  S / A / C / L    jump to the catalog / identity / compute / load-balancer area
  1-9              switch view within the active area
  catalog area:        1 regions · 2 services · 3 endpoints
  identity area:       1 domains · 2 projects · 3 groups · 4 users · 5 roles
  compute area:        1 instances · 2 hypervisors
  load-balancer area:  1 load balancers · 2 virtual IPs · 3 listeners ·
                       4 pools · 5 amphorae (admin only)

Inspect
  y                show raw API object as YAML
  j                show raw API object as JSON
  i                copy object id to clipboard (OSC 52)
  n                copy object name to clipboard
  c                copy the displayed raw object (inside the YAML/JSON view)
  t                full inheritance tree (roles marked ⧉)
  f                CPU feature list (hypervisor details only)

{{list_controls}}Global
  tab              authentication scope switcher
  *                current token (whoami) — user, scope, roles, expiry
  r                refresh — re-fetch current tree, prune dead history
  a                toggle automatic refresh (enabled by default)
{{stats_interval_controls}}  #                application and API telemetry
  ?                this help
  q                quit (back out, then exit)      ctrl+c  force quit

Telemetry overlay
  r                refresh the displayed snapshot
  a                toggle snapshot auto-refresh (enabled by default)
  + / -            lengthen / shorten snapshot interval (= is +)
  z                reset all collected API statistics

Status colors
{{status_legend}}

Row markers
  ●  role assignment held directly    ○  inherited (via a group or parent scope)
  ⚙  service / system account (in the users list)
  ⧉  role includes one or more implied roles

Notes
	• load-balancer/listener details show stats/full refresh cadences (for
	  example, 5s/30s); COE cluster and Kubernetes service details show their
	  Magnum cadence (60s), while other views show the fixed full cadence (30s).
	• enter is the only descent key; arrows are reserved for history.
	• number keys switch views within the active area; space (or S/A/C/L) switches areas.
	  Each view keeps its own history, cursor, and filters. Cross-area reference
	  edges open in place and never change the active area.
  • esc clears an active filter first, otherwise it is back.
  • → reference edges are shared/cross-cutting; ← back-references answer
    "who points at me?".  ↦ in the breadcrumb marks a reference jump.
  • Reference targets and cross-service edges (floating IP, Nova instance)
    resolve lazily on landing. Amphora VMs load in the LB overview. These
    surfaces degrade gracefully when a scope or admin RBAC is missing.
  • Auto-refresh updates visible load-balancer/listener stats at
    1/2/5/10/30/60-second intervals
    (5 seconds by default) and refreshes lists/details/related objects every
    30 seconds. It pauses while overlays or text filters are active.
  • Telemetry is process-local. API metrics record endpoint labels, outcomes,
    and timings only—never bodies, credentials, query values, or full UUIDs.
    The overlay does not pause the application's normal API auto-refresh.
`, "\n")
	var listControls []string
	if showNameIDToggle {
		listControls = append(listControls, "  d                toggle top-level tables between names and IDs")
		listControls = append(listControls, "  o                sort the list by a column — name / id / IP, ascending")
	}
	if showFilter {
		listControls = append(listControls, "  /                filter current list (substring)")
	}
	if showStatusFilter {
		listControls = append(listControls, "  s                cycle status filter — all / error / degraded")
	}
	listHelp := ""
	if len(listControls) > 0 {
		listHelp = "List\n" + strings.Join(listControls, "\n") + "\n\n"
	}
	content = strings.Replace(content, "{{list_controls}}", listHelp, 1)
	statsIntervalHelp := ""
	if showStatsIntervalControls {
		statsIntervalHelp = "  + / -            lengthen / shorten the stats refresh interval\n" +
			"  =                same as + (no Shift required)\n"
	}
	content = strings.Replace(content, "{{stats_interval_controls}}", statsIntervalHelp, 1)
	return strings.Replace(content, "{{status_legend}}", statusLegend(), 1)
}

type statusLegendEntry struct {
	status      string
	description string
	values      string
}

var statusLegendEntries = [...]statusLegendEntry{
	{status: "ONLINE", description: "healthy / ready", values: "ONLINE · ACTIVE · ENABLED · ALLOCATED · READY · UP"},
	{status: "DEGRADED", description: "degraded / changing", values: "DEGRADED · DRAINING · BOOTING · PENDING_*"},
	{status: "ERROR", description: "error", values: "ERROR · FAILOVER_STOPPED · DOWN"},
	{status: "OFFLINE", description: "inactive / unmonitored", values: "OFFLINE · NO_MONITOR · DISABLED · DELETED"},
	{status: "", description: "no health status", values: "VIP / not applicable"},
}

func statusLegend() string {
	lines := make([]string, 0, len(statusLegendEntries))
	for _, item := range statusLegendEntries {
		text := "● " + padRight(item.description, 23) + item.values
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(statusColor(item.status)).Render(text))
	}
	return strings.Join(lines, "\n")
}
