package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"gopkg.in/yaml.v3"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// bodyHeight is the number of list rows available between the two-line header
// and the two-line footer.
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
	case overlayDetail:
		return m.detailView()
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
	scope := "project: " + projectLabel(m.project)
	if m.allProjects {
		scope = "scope: all accessible projects"
	}
	parts := []string{scope}
	if len(m.entries) != len(m.allEntries) {
		parts = append(parts, fmt.Sprintf("%d/%d items", len(m.entries), len(m.allEntries)))
	} else {
		parts = append(parts, fmt.Sprintf("%d items", len(m.entries)))
	}
	if m.status != statusAll {
		parts = append(parts, "status="+m.status.String())
	}
	if v := m.filter.Value(); v != "" && !m.filtering {
		parts = append(parts, "/"+v)
	}
	if !m.backend.SwitchCapability().CanSwitch {
		parts = append(parts, "project-switch: disabled")
	}
	if m.loading {
		parts = append(parts, m.spinner.View()+" "+m.loadingWhat)
	}
	return m.clip(m.st.statusBar.Render(strings.Join(parts, "  ·  ")))
}

// visibleRows is the number of selectable rows the body can show. The
// load-balancer list is rendered as a table, so it gives up one line to the
// column header.
func (m Model) visibleRows() int {
	h := m.bodyHeight()
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
	if len(m.entries) == 0 {
		msg := "— empty —"
		switch {
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
	lines := make([]string, 0, h)
	end := m.top + m.visibleRows()
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
	eff := e.oper
	if eff == "" {
		eff = e.prov
	}
	notable := eff != "" && eff != "ONLINE" && eff != "ACTIVE"

	if sel {
		var icon string
		switch e.kind {
		case entRef:
			icon = "→ "
		case entBackRef:
			icon = "← "
		default:
			icon = "● "
		}
		plain := icon + e.label
		if e.relationship != "" {
			plain += " (" + e.relationship + ")"
		}
		if notable {
			plain += " [" + eff + "]"
		}
		if e.extra != "" {
			plain += "  " + e.extra
		}
		return m.st.selected.Width(m.width).Render(clipRunes(plain, m.width))
	}

	var icon string
	switch e.kind {
	case entRef:
		icon = m.st.refMarker.Render("→ ")
	case entBackRef:
		icon = m.st.backRefMarker.Render("← ")
	default:
		icon = lipgloss.NewStyle().Foreground(statusColor(eff)).Render("●") + " "
	}
	seg := icon + e.label
	if e.relationship != "" {
		seg += m.st.relationship.Render(" (" + e.relationship + ")")
	}
	if notable {
		seg += " " + lipgloss.NewStyle().Foreground(statusColor(eff)).Render("["+eff+"]")
	}
	if e.extra != "" {
		seg += "  " + m.st.attrs.Render(e.extra)
	}
	return m.clip(seg)
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
	hint := "enter open · ←/esc back · → fwd · d detail · y/j raw · i/n/o copy · / filter · s status · p project · r refresh · h history · ? help · q quit"
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

func (m *Model) refreshDetailOverlay() {
	m.vp.Width = m.width
	m.vp.Height = m.height - 2
	m.vp.SetContent(m.detailContent())
}

func (m Model) detailView() string {
	obj := "object"
	if m.loc.node != nil {
		obj = m.loc.node.Label()
	}
	title := m.st.overlayTitle.Render("detail — " + obj)
	footer := m.st.help.Render("esc/q close · ↑/↓ scroll")
	return title + "\n" + m.vp.View() + "\n" + m.clip(footer)
}

func (m Model) detailContent() string {
	n := m.loc.node
	if n == nil {
		return "no object"
	}
	var b strings.Builder
	writeKV(&b, "type", string(n.Type))
	writeKV(&b, "id", n.ID)
	if n.Name != "" {
		writeKV(&b, "name", n.Name)
	}
	if n.ProvisioningStatus != "" {
		writeKV(&b, "provisioning_status", n.ProvisioningStatus)
	}
	if n.OperatingStatus != "" {
		writeKV(&b, "operating_status", n.OperatingStatus)
	}
	keys := make([]string, 0, len(n.Attrs))
	for k := range n.Attrs {
		if strings.HasPrefix(k, "_") {
			continue // internal markers (e.g. _lazy)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeKV(&b, k, n.Attrs[k])
	}
	if !n.DetailLoaded && n.Type != model.TypeVIP {
		b.WriteString("\n" + m.st.disabled.Render("loading full configuration…"))
	}
	if n.Type == model.TypeLoadBalancer {
		b.WriteString("\nstats:\n")
		if s, ok := m.lbStats[n.ID]; ok {
			for _, k := range []string{"active_connections", "total_connections", "bytes_in", "bytes_out", "request_errors"} {
				writeKV(&b, "  "+k, fmt.Sprint(s[k]))
			}
		} else {
			b.WriteString("  " + m.st.disabled.Render("loading…") + "\n")
		}
	}
	return b.String()
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

func writeKV(b *strings.Builder, k, v string) {
	if v == "" {
		return
	}
	fmt.Fprintf(b, "%-22s %s\n", k+":", v)
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
  d                toggle detail panel (lazy-loaded full config; LB adds stats)
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
  ?                this help
  q                quit (back out, then exit)      ctrl+c  force quit

Notes
  • enter is the only descent key; arrows are reserved for history.
  • esc clears an active filter first, otherwise it is back.
  • → reference edges are shared/cross-cutting; ← back-references answer
    "who points at me?".  ↦ in the breadcrumb marks a reference jump.
  • Reference targets and cross-service edges (floating IP, Nova instance,
    amphorae) resolve lazily on landing and degrade gracefully when a scope
    or admin RBAC is missing.
`, "\n")
}
