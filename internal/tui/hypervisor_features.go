package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const (
	hypervisorFeaturesTitle  = "CPU FEATURES"
	hypervisorFeaturesFooter = "↑/↓/PgUp/PgDn scroll · esc/f/q close"
)

func (m Model) openHypervisorFeatures() (tea.Model, tea.Cmd) {
	if !m.isHypervisorOverview() {
		return m, nil
	}
	m.overlay = overlayHypervisorFeatures
	m.setupHypervisorFeaturesViewport(true)
	return m, nil
}

func (m Model) onHypervisorFeaturesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Quit), key.Matches(msg, m.keys.Features):
		m.overlay = overlayNone
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *Model) setupHypervisorFeaturesViewport(gotoTop bool) {
	offset := m.vp.YOffset
	content := m.hypervisorFeaturesContent()
	width, _ := m.scrollModalSize(hypervisorFeaturesTitle, hypervisorFeaturesFooter, content)
	wrapped := ansi.Wrap(content, width, " ")
	_, height := m.scrollModalSize(hypervisorFeaturesTitle, hypervisorFeaturesFooter, wrapped)
	m.vp.Width, m.vp.Height = width, height
	m.vp.SetContent(wrapped)
	if gotoTop {
		m.vp.GotoTop()
	} else {
		m.vp.SetYOffset(offset)
	}
}

func (m Model) hypervisorFeaturesView() string {
	return overlayCenter(m.listView(), m.hypervisorFeaturesModalBox(), m.width, m.height)
}

func (m Model) hypervisorFeaturesModalBox() string {
	title := m.st.modalTitle.Render(hypervisorFeaturesTitle)
	footer := m.st.modalHelp.Render(hypervisorFeaturesFooter)
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

func (m Model) hypervisorFeaturesContent() string {
	if m.loc.node == nil {
		return "  — no CPU features reported —"
	}
	raw := strings.TrimSpace(m.loc.node.Attrs["cpu_features"])
	if raw == "" {
		return "  — no CPU features reported —"
	}
	parts := strings.Split(raw, ",")
	features := make([]string, 0, len(parts))
	for _, part := range parts {
		if feature := strings.TrimSpace(part); feature != "" {
			features = append(features, feature)
		}
	}
	if len(features) == 0 {
		return "  — no CPU features reported —"
	}
	sort.Strings(features)
	return "  " + strings.Join(features, " ")
}
