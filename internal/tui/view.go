package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
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
	case overlayProject:
		return m.projectView()
	case overlayPicker:
		return m.pickerView()
	case overlayTelemetry:
		return m.telemetryView()
	}
	return m.listView()
}

func (m Model) listView() string {
	lines := make([]string, 0, m.height)
	lines = append(lines, m.breadcrumbLine())
	lines = append(lines, m.subtitleLine())
	lines = append(lines, m.bodyLines()...)
	lines = append(lines, m.flashLine())
	lines = append(lines, m.hintLine())
	return strings.Join(lines, "\n")
}

func (m Model) breadcrumbLine() string {
	trail := m.hist.trail()
	parts := []string{m.st.breadcrumb.Render(listKindOf(m.hist.rootIdentity()).rootLabel())}
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
	scope := m.st.statusBar.Render("scope: project ") + m.st.title.Render(projectLabel(m.project))
	if m.allProjects {
		if m.backend.SwitchCapability().GlobalAdmin {
			scope = m.st.statusBar.Render("scope: ") + m.st.title.Render("global admin · all projects")
		} else {
			scope = m.st.statusBar.Render("scope: ") + m.st.title.Render("all projects")
		}
	} else if m.backend.SwitchCapability().GlobalAdmin {
		scope = m.st.statusBar.Render("scope: ") + m.st.title.Render("global admin · project "+projectLabel(m.project))
	}
	parts := []string{scope, m.styledAutoRefreshLabel()}
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
	if !m.backend.SwitchCapability().CanSwitch {
		parts = append(parts, m.st.statusBar.Render("project-switch: disabled"))
	}
	if m.loading {
		parts = append(parts, m.st.statusBar.Render(m.spinner.View()+" "+m.loadingLabel()))
	}
	return m.clip(strings.Join(parts, m.st.statusBar.Render("  ·  ")))
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
		return append([]string{""}, m.lbTableLines(h-1)...)
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
	end := m.top + h
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

func (m Model) isStatsOverview() bool {
	return m.isLBOverview() || m.isListenerOverview()
}

func (m Model) isOverview() bool {
	return m.isLBOverview() || m.isVIPOverview() || m.isListenerOverview() || m.isPoolOverview() || m.isMemberOverview() || m.isAmphoraOverview() || m.isHealthMonitorOverview()
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
		visibleCount := selectableEntryCount(m.entries)
		allCount := selectableEntryCount(m.allEntries)
		title := fmt.Sprintf("RELATED OBJECTS %d", visibleCount)
		if visibleCount != allCount {
			title = fmt.Sprintf("RELATED OBJECTS %d/%d", visibleCount, allCount)
		}
		rendered := m.st.title.Render(title)
		errors, degraded := relatedIssueCounts(m.entries)
		lines = append(lines, m.clip(m.renderIssueCounts(rendered, errors, degraded)))
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
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[0], groups[1], leftWidth, rightWidth, gap), "\n")...)
		lines = append(lines, "")
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[2], groups[3], leftWidth, rightWidth, gap), "\n")...)
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width), "\n")...)
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
		visibleCount := selectableEntryCount(m.entries)
		allCount := selectableEntryCount(m.allEntries)
		title := fmt.Sprintf("RELATED OBJECTS %d", visibleCount)
		if visibleCount != allCount {
			title = fmt.Sprintf("RELATED OBJECTS %d/%d", visibleCount, allCount)
		}
		rendered := m.st.title.Render(title)
		errors, degraded := relatedIssueCounts(m.entries)
		lines = append(lines, m.clip(m.renderIssueCounts(rendered, errors, degraded)))
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
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[0], groups[1], leftWidth, rightWidth, gap), "\n")...)
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width), "\n")...)
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
			lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[i], groups[i+1], leftWidth, rightWidth, gap), "\n")...)
		}
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width), "\n")...)
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
		lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[0], groups[1], leftWidth, rightWidth, gap), "\n")...)
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width), "\n")...)
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

func (m Model) renderOverviewGroupPair(left, right overviewGroup, leftWidth, rightWidth, gap int) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderOverviewGroup(left, leftWidth),
		strings.Repeat(" ", gap),
		m.renderOverviewGroup(right, rightWidth),
	)
}

func (m Model) renderOverviewGroup(group overviewGroup, width int) string {
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
		lines = append(lines, m.st.groupHeading.Render(group.title))
	}
	for _, field := range group.fields {
		if field.breakBefore {
			lines = append(lines, "")
		}
		if field.subheading != "" {
			lines = append(lines, m.st.groupHeading.Render(field.subheading))
		}
		label := m.st.panelLabel.Render(padRight(field.label, labelWidth))
		value := field.value
		if field.status && value != "—" {
			value = lipgloss.NewStyle().Foreground(statusColor(value)).Render(value)
		} else if field.color != lipgloss.Color("") && value != "—" {
			value = lipgloss.NewStyle().Foreground(field.color).Render(value)
		}
		line := "  " + label + "  " + value
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width).Render(line))
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
		visibleCount := selectableEntryCount(m.entries)
		allCount := selectableEntryCount(m.allEntries)
		title := fmt.Sprintf("RELATED OBJECTS %d", visibleCount)
		if visibleCount != allCount {
			title = fmt.Sprintf("RELATED OBJECTS %d/%d", visibleCount, allCount)
		}
		renderedTitle := m.st.title.Render(title)
		errorCount, degradedCount := relatedIssueCounts(m.entries)
		renderedTitle = m.renderIssueCounts(renderedTitle, errorCount, degradedCount)
		lbID := m.loc.node.ID
		title = m.overviewPanelTitleRendered(renderedTitle, false, m.lbRelatedErr[lbID], m.updatedAt(lbID, sectionRelated), m.lbRelatedErr[lbID] != "")
		lines = append(lines, m.clip(title))
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
		visibleCount := selectableEntryCount(m.entries)
		allCount := selectableEntryCount(m.allEntries)
		title := fmt.Sprintf("RELATED OBJECTS %d", visibleCount)
		if visibleCount != allCount {
			title = fmt.Sprintf("RELATED OBJECTS %d/%d", visibleCount, allCount)
		}
		rendered := m.st.title.Render(title)
		errors, degraded := relatedIssueCounts(m.entries)
		rendered = m.renderIssueCounts(rendered, errors, degraded)
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
		return []string{m.clip(m.st.title.Render("LISTENER DETAILS · STATS"))}
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
		return []string{m.clip(m.st.title.Render("LOAD BALANCER DETAILS · STATS"))}
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
		return m.st.title.Render("STATS") + " · " + m.st.disabled.Render(m.statsSpinner.View())
	}
	overdue := m.autoRefreshEnabled && !updated.IsZero() && !m.statsWithinAutoInterval(updated)
	return m.overviewPanelTitle("STATS", loading, errText, updated, errText != "" || overdue)
}

func (m Model) overviewPanelTitle(title string, loading bool, errText string, updatedAt time.Time, stale bool) string {
	return m.overviewPanelTitleRendered(m.st.title.Render(title), loading, errText, updatedAt, stale)
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
		line := label + "  " + value
		lines = append(lines, lipgloss.NewStyle().MaxWidth(width).Render(line))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
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
// appears only in all-projects mode. The d toggle relabels the name/project
// columns to reflect whether they show IDs or names.
func (m Model) lbColumnTitles() []string {
	name := "NAME"
	if m.showIDs {
		name = "LOAD BALANCER ID"
	}
	if m.allProjects {
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
	if m.allProjects {
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

// lbTableLines renders the active top-level list as a fixed-width table: a header
// row plus the scrolled window of rows, the selected row highlighted and the
// status columns colored. It returns exactly h lines.
//
// Column widths are computed here rather than delegated to lip gloss's table,
// whose auto-sizer enforces no per-column minimum and starves narrow columns
// (protocol, port) to a single character whenever another column (a long name or
// a UUID) is wide. layoutColumnWidths keeps every column readable and always
// sums to the terminal width so the highlight bar is flush.
func (m Model) lbTableLines(h int) []string {
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
		if i == m.cursor-start {
			out = append(out, selStyle.Render(tableRowText(cells, widths)))
			continue
		}
		out = append(out, m.tableDataRow(cells, widths, statusCols))
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
func (m Model) tableDataRow(cells []string, widths []int, statusCols map[int]bool) string {
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
	return b.String()
}

// layoutColumnWidths sizes columns to their natural content width, then expands
// the shortest columns (or shrinks the widest, never below a readable minimum) so
// the row exactly fills total. gap is the inter-column spacing counted for every
// column.
func layoutColumnWidths(titles []string, rows [][]string, total, gap int) []int {
	n := len(titles)
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

	budget := total - gap*n
	if budget < n {
		budget = n // degenerate: at least one column of width 1 each
	}
	sum := 0
	for _, w := range widths {
		sum += w
	}

	const minWidth = 4 // never starve a column below this while others can give
	for sum < budget { // expand the shortest column, evening the row out
		mi := 0
		for j := 1; j < n; j++ {
			if widths[j] < widths[mi] {
				mi = j
			}
		}
		widths[mi]++
		sum++
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
		heading := m.st.groupHeading.Render("── " + e.label)
		return m.clip(m.renderIssueCounts(heading, e.issueErrors, e.issueDegraded))
	}
	eff := e.oper
	if eff == "" {
		eff = e.prov
	}
	healthy := eff == "ONLINE" || eff == "ACTIVE"
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
	body := relationCell + "  " + target
	if extra != "" {
		body += "  " + extra
	}
	if showLBStatuses {
		if statuses := relatedLoadBalancerStatusPlain(e); statuses != "" {
			if extra != "" {
				body += " · " + statuses
			} else {
				body += "  " + statuses
			}
		}
	} else if notable {
		body += "  [" + eff + "]"
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
		seg += "  " + m.st.attrs.Render(extra)
	}
	if showLBStatuses {
		if extra != "" {
			seg += m.st.attrs.Render(" · ") + m.relatedLoadBalancerStatus(e)
		} else {
			seg += "  " + m.relatedLoadBalancerStatus(e)
		}
	} else if notable {
		seg += "  " + lipgloss.NewStyle().Foreground(statusColor(eff)).Render("["+eff+"]")
	}
	return navigationStyledChevron(seg, m.width, m.st.refMarker)
}

func (m Model) renderSelectedOverviewRow(e entry, status, relationCell, target, extra string, notable, showLBStatuses bool) string {
	seg := m.st.refMarker.Render("▶ ") + m.styledNavigationMarker(e, status) +
		m.st.panelLabel.Bold(true).Render(relationCell) + "  " + lipgloss.NewStyle().Bold(true).Render(target)
	if extra != "" {
		seg += "  " + m.st.attrs.Render(extra)
	}
	if showLBStatuses {
		if extra != "" {
			seg += m.st.attrs.Render(" · ") + m.relatedLoadBalancerStatus(e)
		} else {
			seg += "  " + m.relatedLoadBalancerStatus(e)
		}
	} else if notable {
		seg += "  " + lipgloss.NewStyle().Foreground(statusColor(status)).Render("["+status+"]")
	}
	return navigationStyledChevron(seg, m.width, m.st.refMarker)
}

func (m Model) styledNavigationMarker(e entry, status string) string {
	switch e.kind {
	case entRef:
		return m.st.refMarker.Render("→ ")
	case entBackRef:
		return m.st.backRefMarker.Render("← ")
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
		fixed += 2 + lipgloss.Width(extra)
	}
	if statuses := relatedLoadBalancerStatusPlain(e); statuses != "" {
		if extra != "" {
			fixed += 3 // " · "
		} else {
			fixed += 2
		}
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
	if idSuffix == "" || available <= suffixWidth {
		return clipRunes(full, available)
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
		"enter open", "←/esc back", "→ fwd", "1-5 views", "y/j raw", "i/n copy",
	}
	if m.loc.isTopLevelList() {
		parts = append(parts, "d names/ids")
	}
	if hasFilterableEntries(m.allEntries) {
		parts = append(parts, "/ filter")
	}
	if hasStatusEntries(m.allEntries) {
		parts = append(parts, "s status")
	}
	parts = append(parts, "p project", "r refresh", "a auto")
	if m.isStatsOverview() {
		parts = append(parts, "+/- interval")
	}
	parts = append(parts, "h history", "t telemetry", "? help", "q quit")
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

func (m Model) helpView() string {
	title := m.st.overlayTitle.Render("olb — help")
	footer := m.st.help.Render("esc / ? / q  close   ·   ↑/↓ scroll")
	return title + "\n" + m.vp.View() + "\n" + m.clip(footer)
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
	return m.st.overlayTitle.Render(m.clip(title)) + "\n" + m.vp.View() + "\n" + m.clip(footer)
}

func (m Model) projectView() string {
	title := m.projectTitleLine()
	cap := m.backend.SwitchCapability()
	if !cap.CanSwitch {
		body := m.st.flashErr.Render(cap.Reason) + "\n\n" + m.st.help.Render(cap.Suggest)
		cur := "\n\ncurrent project: " + projectLabel(m.project)
		return title + "\n\n" + body + cur + "\n\n" + m.st.help.Render("esc / q  close")
	}
	if m.loading && len(m.projects) == 0 {
		kind := "accessible projects"
		if cap.GlobalAdmin {
			kind = "all projects"
		}
		return title + "\n\n" + m.spinner.View() + " loading " + kind + "…\n\n" + m.st.help.Render("esc cancel")
	}
	fp := m.filteredProjects()
	type prow struct {
		label    string
		current  bool
		disabled bool
	}
	allSelectable := m.allProjectsSelectable()
	rows := make([]prow, 0, len(fp)+1)
	if m.hasAllProjectsRow() {
		rows = append(rows, prow{label: "⟨ all projects ⟩", current: m.allProjects, disabled: !allSelectable})
	}
	for _, p := range fp {
		label := p.Name
		if label == "" {
			label = p.ID
		}
		rows = append(rows, prow{label: label, current: !m.allProjects && p.ID == m.project.ID})
	}

	var b strings.Builder
	b.WriteString(title + "\n\n")
	maxRows := m.projectPageSize()
	start := 0
	if m.projCursor >= maxRows {
		start = m.projCursor - maxRows + 1
	}
	end := start + maxRows
	if end > len(rows) {
		end = len(rows)
	}
	for i := start; i < end; i++ {
		label := rows[i].label
		if rows[i].disabled {
			reason := cap.AllProjectsReason
			if reason == "" {
				reason = "requires --global-admin"
			}
			label += "  (" + reason + ")"
		}
		if rows[i].current {
			label += m.st.relationship.Render(" (current)")
		}
		if i == m.projCursor {
			b.WriteString(m.st.selected.Width(m.width).Render(clipRunes("▸ "+label, m.width)) + "\n")
		} else if rows[i].disabled {
			b.WriteString("  " + m.st.disabled.Render(m.clip(label)) + "\n")
		} else {
			b.WriteString("  " + m.clip(label) + "\n")
		}
	}
	if len(fp) == 0 && m.search.Value() != "" {
		b.WriteString("  " + m.st.disabled.Render("— no matching projects —") + "\n")
	}
	if !cap.GlobalAdmin {
		b.WriteString("\n" + m.st.help.Render("global view: restart with --global-admin"))
	}
	footer := "enter select · arrows/page/home/end move · / filter · esc/q cancel"
	if m.search.Focused() {
		footer = "type to filter · enter apply · esc clear"
	}
	b.WriteString("\n" + m.st.help.Render(footer))
	return b.String()
}

func (m Model) projectTitleLine() string {
	title := m.st.overlayTitle.Render("Switch project")
	query := m.search.Value()
	if !m.search.Focused() && query == "" {
		return m.clip(title)
	}

	separator := m.st.crumbSep.Render("  ")
	if m.search.Focused() {
		inputWidth := m.width - lipgloss.Width(title) - lipgloss.Width(separator) - lipgloss.Width(m.search.Prompt)
		if inputWidth < 1 {
			inputWidth = 1
		}
		m.search.Width = inputWidth
		return m.clip(title + separator + m.search.View())
	}
	return m.clip(title + separator + m.st.statusBar.Render("filter: "+query))
}

func (m Model) projectPageSize() int {
	rows := m.height - 6
	if !m.backend.SwitchCapability().GlobalAdmin {
		rows--
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func (m Model) allProjectsSelectable() bool {
	cap := m.backend.SwitchCapability()
	return cap.GlobalAdmin && cap.CanAllProjects
}

func (m Model) hasAllProjectsRow() bool {
	return m.backend.SwitchCapability().GlobalAdmin
}

func (m Model) firstProjectCursor() int {
	if m.hasAllProjectsRow() && !m.allProjectsSelectable() && len(m.filteredProjects()) > 0 {
		return 1
	}
	return 0
}

func (m Model) currentProjectCursor() int {
	offset := 0
	if m.hasAllProjectsRow() {
		if m.allProjects {
			return 0
		}
		offset = 1
	}
	for i, project := range m.filteredProjects() {
		if project.ID != "" && project.ID == m.project.ID {
			return offset + i
		}
	}
	return m.firstProjectCursor()
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
	b.WriteString(title + "\n")
	searchLine := ""
	if m.search.Focused() {
		searchLine = m.search.View()
	} else if query := m.search.Value(); query != "" {
		searchLine = m.st.statusBar.Render("filter: " + query)
	}
	b.WriteString(searchLine + "\n\n")
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
		b.WriteString("  " + m.st.disabled.Render("— no history —") + "\n")
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
			b.WriteString(m.st.selected.Width(m.width).Render(clipRunes("▸ "+selectedLabel, m.width)) + "\n")
		} else {
			b.WriteString("  " + m.clip(label) + "\n")
		}
	}
	footer := "enter jump · arrows/page/home/end move · / filter · esc cancel"
	if m.search.Focused() {
		footer = "enter apply · esc clear · type to filter"
	}
	b.WriteString("\n" + m.st.help.Render(footer))
	return b.String()
}

func (m Model) pickerPageSize() int {
	rows := m.height - 7
	if rows < 1 {
		return 1
	}
	return rows
}

func (m Model) filteredProjects() []osclient.ProjectInfo {
	q := strings.ToLower(strings.TrimSpace(m.search.Value()))
	if q == "" {
		return m.projects
	}
	var out []osclient.ProjectInfo
	for _, p := range m.projects {
		if strings.Contains(strings.ToLower(p.Name+" "+p.ID), q) {
			out = append(out, p)
		}
	}
	return out
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
Move
  ↑ / ↓            selection up / down
  PgUp / PgDn      page up / down
  Home / End       top / bottom

Navigate
  enter            open selected — drill into a child or follow a reference edge
  ← / esc / ⌫      back (history)      → forward (history)
  ctrl+home        jump to the active view's pinned root history entry
  h                history picker overlay

Top-level views (drill into an item to open its detail)
  1                load balancers
  2                virtual IPs (VIP address, port, subnet, network, owner)
  3                listeners
  4                pools
  5                amphorae (admin only)

Inspect
  y                show raw API object as YAML
  j                show raw API object as JSON
  i                copy object id to clipboard (OSC 52)
  n                copy object name to clipboard
  c                copy the displayed raw object (inside the YAML/JSON view)

{{list_controls}}Global
  p                project switcher
  r                refresh — re-fetch current tree, prune dead history
  a                toggle automatic refresh (enabled by default)
{{stats_interval_controls}}  t                application and API telemetry
  ?                this help
  q                quit (back out, then exit)      ctrl+c  force quit

Telemetry overlay
  r                refresh the displayed snapshot
  a                toggle snapshot auto-refresh (enabled by default)
  + / -            lengthen / shorten snapshot interval (= is +)
  z                reset all collected API statistics

Status colors
{{status_legend}}

Notes
	• load-balancer/listener details show stats/full refresh cadences (for
	  example, 5s/30s); other views show the fixed full cadence (30s).
	• enter is the only descent key; arrows are reserved for history.
	• 1-5 switch persistent views; each keeps its own history, cursor, and filters.
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
	{status: "ONLINE", description: "healthy / ready", values: "ONLINE · ACTIVE · ENABLED · ALLOCATED · READY"},
	{status: "DEGRADED", description: "degraded / changing", values: "DEGRADED · DRAINING · BOOTING · PENDING_*"},
	{status: "ERROR", description: "error", values: "ERROR · FAILOVER_STOPPED"},
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
