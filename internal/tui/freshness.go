package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	freshnessTickInterval = time.Second
	statsFreshnessGrace   = time.Second
)

type freshnessTickMsg struct{}

type overviewSection int

const (
	sectionDetails overviewSection = iota
	sectionStats
	sectionRelated
)

type overviewFreshness struct {
	details time.Time
	stats   time.Time
	related time.Time
}

func freshnessTickCmd() tea.Cmd {
	return tea.Tick(freshnessTickInterval, func(time.Time) tea.Msg {
		return freshnessTickMsg{}
	})
}

func (m *Model) markFresh(lbID string, section overviewSection) {
	if lbID == "" {
		return
	}
	freshness := m.lbFreshness[lbID]
	now := m.clock()
	switch section {
	case sectionDetails:
		freshness.details = now
	case sectionStats:
		freshness.stats = now
	case sectionRelated:
		freshness.related = now
	}
	m.lbFreshness[lbID] = freshness
}

func (m Model) updatedAt(lbID string, section overviewSection) time.Time {
	freshness := m.lbFreshness[lbID]
	switch section {
	case sectionDetails:
		return freshness.details
	case sectionStats:
		return freshness.stats
	case sectionRelated:
		return freshness.related
	default:
		return time.Time{}
	}
}

func (m Model) freshnessLabel(updated time.Time) string {
	if updated.IsZero() {
		return ""
	}
	age := m.clock().Sub(updated)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Second:
		return "updated now"
	case age < time.Minute:
		return fmt.Sprintf("updated %ds ago", int(age/time.Second))
	case age < time.Hour:
		return fmt.Sprintf("updated %dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("updated %dh ago", int(age/time.Hour))
	default:
		return fmt.Sprintf("updated %dd ago", int(age/(24*time.Hour)))
	}
}

func (m Model) statsWithinAutoInterval(updated time.Time) bool {
	if !m.autoRefreshEnabled || updated.IsZero() {
		return false
	}
	age := m.clock().Sub(updated)
	return age >= 0 && age < m.autoRefreshInterval()+statsFreshnessGrace
}

func (m *Model) ensureStatsSpinner() tea.Cmd {
	if !m.isLBOverview() {
		return nil
	}
	updated := m.updatedAt(m.currentLBID(), sectionStats)
	if m.statsSpinnerRunning || !m.statsWithinAutoInterval(updated) {
		return nil
	}
	m.statsSpinnerRunning = true
	return m.statsSpinner.Tick
}

func (m Model) currentLBID() string {
	if m.loc.node == nil {
		return ""
	}
	return m.loc.node.OwningLBID
}
