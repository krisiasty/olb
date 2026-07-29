package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/krisiasty/olb/internal/osclient"
)

const (
	// Ten displayed levels (including the selected root) are enough for normal
	// Keystone hierarchies while still exposing unexpectedly deep rules.
	roleTreeMaxDepth = 10
	// A depth limit alone does not protect against a very broad graph. Shared
	// roles remain fully expanded on each path until this exceptional safeguard.
	roleTreeMaxNodes = 2000
)

func (m *Model) resetRoleInferenceCache() {
	m.roleInferences = map[string][]osclient.Role{}
	m.roleInferencesLoaded = false
	m.roleInferencesLoading = false
	m.roleInferencesGeneration++
	m.roleTreeRoot = osclient.Role{}
	m.roleTreeErr = ""
}

func (m Model) roleTreeCandidate() (osclient.Role, bool) {
	if m.isRoleOverview() && m.loc.node != nil {
		role, ok := m.knownRoles[m.loc.node.ID]
		if !ok {
			role = osclient.Role{ID: m.loc.node.ID, Name: m.loc.node.Name}
		}
		return role, m.roleHasImpliedRoles(role)
	}
	if !m.loc.isTopLevelList() || m.loc.listKind() != kindRole ||
		m.cursor < 0 || m.cursor >= len(m.entries) {
		return osclient.Role{}, false
	}
	entry := m.entries[m.cursor]
	if entry.kind != entRole {
		return osclient.Role{}, false
	}
	return entry.role, m.roleHasImpliedRoles(entry.role)
}

func (m Model) roleHasImpliedRoles(role osclient.Role) bool {
	if role.ID == "" || role.TokenScoped {
		return false
	}
	if role.ImpliesRoles || len(m.roleInferences[role.ID]) > 0 {
		return true
	}
	if m.roleRelationsLoaded[role.ID] {
		return len(m.roleRelations[role.ID].implied) > 0
	}
	return false
}

func (m Model) canOpenRoleTree() bool {
	_, ok := m.roleTreeCandidate()
	return ok
}

// roleTreeContext reports whether t belongs to the current view at all. A role
// without implied roles may still explain why no tree opens; outside the roles
// list/detail, t is inactive and must be a silent no-op.
func (m Model) roleTreeContext() bool {
	if m.isRoleOverview() {
		return true
	}
	return m.loc.isTopLevelList() && m.loc.listKind() == kindRole &&
		m.cursor >= 0 && m.cursor < len(m.entries) && m.entries[m.cursor].kind == entRole
}

func (m Model) openRoleTree() (tea.Model, tea.Cmd) {
	root, ok := m.roleTreeCandidate()
	if !ok {
		return m, m.setFlash("inheritance tree is available for roles marked ⧉", false)
	}
	m.roleTreeRoot = root
	m.roleTreeErr = ""
	m.overlay = overlayRoleTree
	if m.roleInferencesLoaded {
		m.setupRoleTreeViewport(true)
		return m, nil
	}
	if m.roleInferencesLoading {
		m.setupRoleTreeViewport(true)
		return m, nil
	}
	m.roleInferencesLoading = true
	m.setupRoleTreeViewport(true)
	return m, m.loadRoleInferencesCmd()
}

func (m Model) onRoleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Quit), key.Matches(msg, m.keys.RoleTree):
		m.overlay = overlayNone
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) setupRoleTreeViewport(gotoTop bool) {
	offset := m.vp.YOffset
	content := ""
	switch {
	case m.roleInferencesLoading:
		content = "  loading role inference rules…"
	case m.roleTreeErr != "":
		content = "  " + m.st.flashErr.Render(m.roleTreeErr)
	default:
		content = m.roleTreeContent()
	}
	m.vp.Width, m.vp.Height = m.roleTreeModalSize(content)
	m.vp.SetContent(content)
	if gotoTop {
		m.vp.GotoTop()
	} else {
		m.vp.SetYOffset(offset)
	}
}

func (m Model) roleTreeView() string {
	return overlayCenter(m.listView(), m.roleTreeModalBox(), m.width, m.height)
}

func (m Model) roleTreeModalBox() string {
	title := m.st.modalTitle.Render("ROLE INHERITANCE")
	footer := m.st.modalHelp.Render("↑/↓/PgUp/PgDn scroll · esc/t/q close")
	above, below := m.scrollMarkers()
	width := m.vp.Width
	lines := []string{
		modalMarkerLine(title, above, width),
		m.st.modalRow.Width(width).Render(""),
	}
	for _, line := range strings.Split(m.vp.View(), "\n") {
		lines = append(lines, m.st.modalRow.Width(width).MaxWidth(width).Render(line))
	}
	lines = append(
		lines,
		m.st.modalRow.Width(width).Render(""),
		modalMarkerLine(footer, below, width),
	)
	return m.st.modalFrame.Render(strings.Join(lines, "\n"))
}

// roleTreeModalSize keeps the popup content-sized like the token modal, but
// limits a long tree to roughly three quarters of the terminal height. The
// remaining rows leave the underlying view visible and make the modal nature
// clear. Width includes the longest visible tree line, title, or footer, capped
// so the rounded frame always fits.
func (m Model) roleTreeModalSize(content string) (width, viewportHeight int) {
	title := "ROLE INHERITANCE"
	footer := "↑/↓/PgUp/PgDn scroll · esc/t/q close"
	// Reserve enough room for the complete footer and the widest possible scroll
	// marker. The area switcher baseline then keeps all special-purpose popups
	// visually consistent even when a particular role tree is narrow.
	footerWithMarker := lipgloss.Width(footer) + 1 + lipgloss.Width("100% ▼ more")
	width = max(lipgloss.Width(title), footerWithMarker, m.areaSwitcherBaselineWidth())
	contentLines := strings.Split(content, "\n")
	for _, line := range contentLines {
		if w := ansi.StringWidth(line); w > width {
			width = w
		}
	}

	width = m.constrainModalWidth(width)

	frameHeight := m.st.modalFrame.GetVerticalFrameSize()
	maxOuterHeight := m.height * 3 / 4
	minOuterHeight := frameHeight + 5 // title + separator + one viewport row + separator + footer
	if maxOuterHeight < minOuterHeight {
		maxOuterHeight = minOuterHeight
	}
	if available := m.height - 2; available > 0 && maxOuterHeight > available {
		maxOuterHeight = available
	}
	viewportHeight = maxOuterHeight - frameHeight - 4
	if viewportHeight > len(contentLines) {
		viewportHeight = len(contentLines)
	}
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	return width, viewportHeight
}

// modalMarkerLine places a scroll marker on a fixed-width line inside a modal.
// Unlike the generic overlay helper, it preserves the marker on narrow
// terminals by clipping the title/footer first.
func modalMarkerLine(left, marker string, width int) string {
	lineStyle := lipgloss.NewStyle().Width(width).MaxWidth(width)
	if marker == "" {
		return lineStyle.Render(left)
	}
	markerWidth := lipgloss.Width(marker)
	if width <= markerWidth {
		return lipgloss.NewStyle().MaxWidth(width).Render(marker)
	}
	left = lipgloss.NewStyle().MaxWidth(width - markerWidth - 1).Render(left)
	padding := width - lipgloss.Width(left) - markerWidth
	if padding < 1 {
		padding = 1
	}
	return left + strings.Repeat(" ", padding) + marker
}

type roleTreeRender struct {
	lines          []string
	nodes          int
	depthTruncated bool
	nodeTruncated  bool
	cycle          bool
	stopped        bool
}

func (m Model) roleTreeContent() string {
	root := m.roleTreeRoot
	render := roleTreeRender{
		lines: []string{m.st.title.Render(roleTreeLabel(root))},
		nodes: 1,
	}
	path := map[string]bool{root.ID: true}
	m.renderRoleTreeChildren(root.ID, "", 1, path, &render)

	var warnings []string
	if render.depthTruncated {
		warnings = append(warnings, fmt.Sprintf(
			"WARNING · depth limit %d reached; (...) marks roles with deeper implied roles.",
			roleTreeMaxDepth,
		))
	}
	if render.nodeTruncated {
		warnings = append(warnings, fmt.Sprintf(
			"WARNING · tree stopped after %d roles; (...) marks omitted branches.",
			roleTreeMaxNodes,
		))
	}
	if render.cycle {
		warnings = append(warnings, "WARNING · cyclic inference detected; (↻ cycle) marks branches not expanded again.")
	}
	if len(warnings) == 0 {
		return strings.Join(render.lines, "\n")
	}
	warningStyle := m.st.groupHeading.Foreground(statusColor("DEGRADED"))
	for i := range warnings {
		warnings[i] = warningStyle.Render(warnings[i])
	}
	return strings.Join(warnings, "\n") + "\n\n" + strings.Join(render.lines, "\n")
}

func (m Model) renderRoleTreeChildren(
	parentID, prefix string,
	parentLevel int,
	path map[string]bool,
	render *roleTreeRender,
) {
	children := m.roleInferences[parentID]
	for i, child := range children {
		if render.nodes >= roleTreeMaxNodes {
			render.lines = append(render.lines, prefix+"└─ (...)")
			render.nodeTruncated = true
			render.stopped = true
			return
		}

		last := i == len(children)-1
		connector, continuation := "├─ ", "│  "
		if last {
			connector, continuation = "└─ ", "   "
		}
		line := prefix + connector + roleTreeLabel(child)
		render.nodes++
		childLevel := parentLevel + 1

		if path[child.ID] {
			render.lines = append(render.lines, line+" (↻ cycle)")
			render.cycle = true
			continue
		}
		grandchildren := m.roleInferences[child.ID]
		if childLevel >= roleTreeMaxDepth && len(grandchildren) > 0 {
			render.lines = append(render.lines, line+" (...)")
			render.depthTruncated = true
			continue
		}

		render.lines = append(render.lines, line)
		if len(grandchildren) == 0 {
			continue
		}
		path[child.ID] = true
		m.renderRoleTreeChildren(child.ID, prefix+continuation, childLevel, path, render)
		delete(path, child.ID)
		if render.stopped {
			return
		}
	}
}

func roleTreeLabel(role osclient.Role) string {
	if role.Name != "" {
		return role.Name
	}
	return shortID(role.ID)
}
