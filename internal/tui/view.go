package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
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
	out := m.st.breadcrumb.Render("load balancers")
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
		out += m.st.crumbSep.Render(marker) + rendered
	}
	return m.clip(out)
}

func (m Model) subtitleLine() string {
	scope := m.st.statusBar.Render("scope: project ") + m.st.title.Render(projectLabel(m.project))
	if m.allProjects {
		scope = m.st.statusBar.Render("scope: ") + m.st.title.Render("all accessible projects")
	}
	parts := []string{scope, m.styledAutoRefreshLabel()}
	if !m.isLBOverview() {
		if len(m.entries) != len(m.allEntries) {
			parts = append(parts, m.st.statusBar.Render(fmt.Sprintf("%d/%d items", len(m.entries), len(m.allEntries))))
		} else {
			parts = append(parts, m.st.statusBar.Render(fmt.Sprintf("%d items", len(m.entries))))
		}
	}
	if m.status != statusAll {
		parts = append(parts, m.st.statusBar.Render("status="+m.status.String()))
	}
	if v := m.filter.Value(); v != "" && !m.filtering {
		parts = append(parts, m.st.statusBar.Render("/"+v))
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
	}
	if m.loc.isList() && len(m.entries) > 0 {
		h--
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
	if len(m.entries) == 0 {
		msg := "— empty —"
		switch {
		case m.refreshing:
			msg = m.spinner.View() + " refreshing…"
		case m.loading:
			msg = m.spinner.View() + " loading " + m.loadingWhat + "…"
		case m.loc.dead:
			msg = "this object was deleted since you last viewed it (press ← back or ctrl+home)"
		case m.filter.Value() != "" || m.status != statusAll:
			msg = "— no matches —"
		}
		lines := []string{"  " + m.st.disabled.Render(msg)}
		for len(lines) < h {
			lines = append(lines, "")
		}
		return lines
	}
	if m.loc.isList() {
		return m.lbTableLines(h)
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
	label  string
	value  string
	status bool
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
	detailTitle := m.overviewPanelTitle("DETAILS", !m.refreshing && m.lbDetailLoading[lbID], m.lbDetailErr[lbID], m.updatedAt(lbID, sectionDetails), m.lbDetailErr[lbID] != "")
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
		return []string{m.clip(m.st.title.Render("DETAILS · STATS"))}
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
	return []overviewField{
		{label: "Name", value: name},
		{label: "ID", value: n.ID},
		{label: "Project name", value: displayValue(projectName)},
		{label: "Project ID", value: displayValue(projectID)},
		{label: "Primary VIP", value: displayValue(vip)},
		{label: "Provider", value: displayValue(n.Attrs["provider"])},
		{label: "Operating", value: displayValue(n.OperatingStatus), status: true},
		{label: "Provisioning", value: displayValue(n.ProvisioningStatus), status: true},
		{label: "Admin state", value: adminStateLabel(adminState), status: true},
	}
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
		{label: "Connections", value: withSignedRate("total_connections")},
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

func limitLines(lines []string, limit int) []string {
	if len(lines) > limit {
		return lines[:limit]
	}
	return lines
}

// lbColumnTitles are the load-balancer table headers; the project column
// appears only in all-projects mode.
func (m Model) lbColumnTitles() []string {
	if m.allProjects {
		return []string{"NAME", "PROJECT", "PROVIDER", "VIP", "PROVISIONING", "OPERATING"}
	}
	return []string{"NAME", "PROVIDER", "VIP", "PROVISIONING", "OPERATING"}
}

func (m Model) lbRowCells(e entry) []string {
	lb := e.lb
	name := lb.Name
	if name == "" {
		name = shortID(lb.ID)
	}
	if m.allProjects {
		proj := lb.ProjectName
		if proj == "" {
			proj = shortID(lb.ProjectID)
		}
		return []string{name, proj, lb.Provider, lb.VipAddress, lb.ProvisioningStatus, lb.OperatingStatus}
	}
	return []string{name, lb.Provider, lb.VipAddress, lb.ProvisioningStatus, lb.OperatingStatus}
}

// lbTableLines renders the load-balancer list as a Lip Gloss table (column
// header plus the scrolled window of rows), the selected row highlighted and
// the status columns colored. It returns exactly h lines.
func (m Model) lbTableLines(h int) []string {
	titles := m.lbColumnTitles()
	statusCols := map[int]bool{len(titles) - 1: true, len(titles) - 2: true} // OPERATING, PROVISIONING

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
		rows[i] = m.lbRowCells(e)
	}
	selRow := m.cursor - start

	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(false).
		Width(m.width).
		Headers(titles...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == table.HeaderRow:
				return m.st.tableHeader
			case row == selRow:
				return m.st.tableSelected
			case statusCols[col] && row >= 0 && row < len(rows):
				return m.st.tableCell.Foreground(statusColor(rows[row][col]))
			default:
				return m.st.tableCell
			}
		})

	out := strings.Split(t.Render(), "\n")
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

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
	if m.isLBOverview() {
		indent = "  "
	}
	plain := indent + navigationMarker(e) + relationCell + "  " + target
	if extra != "" {
		plain += "  " + extra
	}
	if notable {
		plain += "  [" + eff + "]"
	}
	plain = navigationChevron(plain, m.width)

	if sel {
		return m.st.selected.Width(m.width).Render(clipRunes(plain, m.width))
	}

	var marker string
	switch e.kind {
	case entRef:
		marker = m.st.refMarker.Render("→ ")
	case entBackRef:
		marker = m.st.backRefMarker.Render("← ")
	default:
		marker = lipgloss.NewStyle().Foreground(statusColor(eff)).Render("●") + " "
	}
	seg := indent + marker + m.st.panelLabel.Render(relationCell) + "  " + target
	if extra != "" {
		seg += "  " + m.st.attrs.Render(extra)
	}
	if notable {
		seg += "  " + lipgloss.NewStyle().Foreground(statusColor(eff)).Render("["+eff+"]")
	}
	return navigationStyledChevron(seg, m.width, m.st.refMarker)
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
	case entChild:
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
	if e.kind == entChild && e.node != nil {
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
		return m.clip(m.filter.View())
	}
	hint := "enter open · ←/esc back · → fwd · y/j raw · i/n/o copy · / filter · s status · p project · r refresh · a auto · +/- interval · h history · ? help · q quit"
	return m.clip(m.st.help.Render(hint))
}

// --- overlays -------------------------------------------------------------

func (m *Model) setupHelpViewport() {
	m.vp.Width = m.width
	m.vp.Height = m.height - 2
	m.vp.SetContent(helpContent())
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
	footer := m.st.help.Render("esc/q close · o copy · ↑/↓ scroll")
	return m.st.overlayTitle.Render(m.clip(title)) + "\n" + m.vp.View() + "\n" + m.clip(footer)
}

func (m Model) projectView() string {
	title := m.st.overlayTitle.Render("Switch project")
	cap := m.backend.SwitchCapability()
	if !cap.CanSwitch {
		body := m.st.flashErr.Render(cap.Reason) + "\n\n" + m.st.help.Render(cap.Suggest)
		cur := "\n\ncurrent project: " + projectLabel(m.project)
		return title + "\n\n" + body + cur + "\n\n" + m.st.help.Render("esc / q  close")
	}
	if m.loading && len(m.projects) == 0 {
		return title + "\n\n" + m.spinner.View() + " loading accessible projects…\n\n" + m.st.help.Render("esc cancel")
	}
	fp := m.filteredProjects()
	// Row 0 is the synthetic "all projects" option; the rest are projects.
	type prow struct {
		label   string
		current bool
	}
	rows := []prow{{label: "⟨ all accessible projects ⟩", current: m.allProjects}}
	for _, p := range fp {
		label := p.Name
		if label == "" {
			label = p.ID
		}
		rows = append(rows, prow{label: label, current: !m.allProjects && p.ID == m.project.ID})
	}

	var b strings.Builder
	b.WriteString(title + "\n")
	b.WriteString(m.search.View() + "\n\n")
	maxRows := m.height - 7
	if maxRows < 1 {
		maxRows = 1
	}
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
		if rows[i].current {
			label += m.st.relationship.Render(" (current)")
		}
		if i == m.projCursor {
			b.WriteString(m.st.selected.Width(m.width).Render(clipRunes("▸ "+rows[i].label, m.width)) + "\n")
		} else {
			b.WriteString("  " + m.clip(label) + "\n")
		}
	}
	if len(fp) == 0 && m.search.Value() != "" {
		b.WriteString("  " + m.st.disabled.Render("— no matching projects —") + "\n")
	}
	b.WriteString("\n" + m.st.help.Render("enter select · ↑/↓ move · type to filter · esc cancel"))
	return b.String()
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
		label := "load balancers"
		if !e.id.IsLBList() {
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
	title := m.st.overlayTitle.Render("History")
	items := m.pickerItems()
	var b strings.Builder
	b.WriteString(title + "\n")
	b.WriteString(m.search.View() + "\n\n")
	maxRows := m.height - 7
	if maxRows < 1 {
		maxRows = 1
	}
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
		if it.current {
			label += m.st.relationship.Render(" (here)")
		}
		if it.dead {
			label = m.st.dead.Render(it.label) + m.st.relationship.Render(" (deleted)")
		}
		if i == m.pickCursor {
			b.WriteString(m.st.selected.Width(m.width).Render(clipRunes("▸ "+it.label, m.width)) + "\n")
		} else {
			b.WriteString("  " + m.clip(label) + "\n")
		}
	}
	b.WriteString("\n" + m.st.help.Render("enter jump · ↑/↓ move · type to filter · esc cancel"))
	return b.String()
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

func helpContent() string {
	return strings.TrimLeft(`
Move
  ↑ / ↓            selection up / down
  PgUp / PgDn      page up / down
  Home / End       top / bottom

Navigate
  enter            open selected — drill into a child or follow a reference edge
  ← / esc / ⌫      back (history)      → forward (history)
  ctrl+home        return to the load balancer list
  h                history picker overlay

Inspect
  y                show raw API object as YAML
  j                show raw API object as JSON
  i                copy object id to clipboard (OSC 52)
  n                copy object name to clipboard
  o                copy the displayed raw object (after y or j)

Search
  /                filter current list (substring)
  s                cycle status filter — all / error / degraded

Global
  p                project switcher
  r                refresh — re-fetch current tree, prune dead history
  a                toggle automatic refresh (enabled by default)
  + / -            lengthen / shorten the stats refresh interval
  =                same as + (no Shift required)
  ?                this help
  q                quit (back out, then exit)      ctrl+c  force quit

Notes
	• auto-refresh header intervals are stats/full (for example, 5s/30s).
	• enter is the only descent key; arrows are reserved for history.
  • esc clears an active filter first, otherwise it is back.
  • → reference edges are shared/cross-cutting; ← back-references answer
    "who points at me?".  ↦ in the breadcrumb marks a reference jump.
  • Reference targets and cross-service edges (floating IP, Nova instance)
    resolve lazily on landing. Amphora VMs load in the LB overview. These
    surfaces degrade gracefully when a scope or admin RBAC is missing.
  • Auto-refresh updates visible LB stats at 1/2/5/10/30/60-second intervals
    (5 seconds by default) and refreshes lists/details/related objects every
    30 seconds. It pauses while overlays or text filters are active.
`, "\n")
}
