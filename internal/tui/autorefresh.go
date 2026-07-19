package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var autoRefreshIntervals = [...]time.Duration{
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

const (
	defaultAutoRefreshIntervalIndex = 2
	fullAutoRefreshInterval         = 30 * time.Second
)

type autoStatsTickMsg struct{ generation uint64 }
type autoFullTickMsg struct{ generation uint64 }

func autoStatsTickCmd(generation uint64, interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return autoStatsTickMsg{generation: generation}
	})
}

func autoFullTickCmd(generation uint64) tea.Cmd {
	return tea.Tick(fullAutoRefreshInterval, func(time.Time) tea.Msg {
		return autoFullTickMsg{generation: generation}
	})
}

func (m Model) autoRefreshInterval() time.Duration {
	index := m.autoIntervalIndex
	if index < 0 || index >= len(autoRefreshIntervals) {
		index = defaultAutoRefreshIntervalIndex
	}
	return autoRefreshIntervals[index]
}

func (m Model) scheduleAutoRefresh() tea.Cmd {
	if !m.autoRefreshEnabled {
		return nil
	}
	return tea.Batch(
		autoStatsTickCmd(m.autoGeneration, m.autoRefreshInterval()),
		autoFullTickCmd(m.autoGeneration),
	)
}

func (m Model) onAutoStatsTick(msg autoStatsTickMsg) (tea.Model, tea.Cmd) {
	if !m.validAutoRefreshGeneration(msg.generation) {
		return m, nil
	}
	nextTick := autoStatsTickCmd(msg.generation, m.autoRefreshInterval())
	if m.autoRefreshPaused() || !m.isLBOverview() {
		return m, nextTick
	}
	lbID := m.loc.node.ID
	if m.lbStatsLoading[lbID] || m.autoStatsLoading[lbID] {
		return m, nextTick
	}
	m.autoStatsLoading[lbID] = true
	return m, tea.Batch(m.autoStatsCmd(lbID), nextTick)
}

func (m Model) onAutoFullTick(msg autoFullTickMsg) (tea.Model, tea.Cmd) {
	if !m.validAutoRefreshGeneration(msg.generation) {
		return m, nil
	}
	nextTick := autoFullTickCmd(msg.generation)
	if m.autoRefreshPaused() || m.overviewRequestInFlight() {
		return m, nextTick
	}
	next, refreshCmd := m.beginRefresh(true)
	if refreshCmd == nil {
		return next, nextTick
	}
	return next, tea.Batch(refreshCmd, nextTick)
}

func (m Model) validAutoRefreshGeneration(generation uint64) bool {
	return m.autoRefreshEnabled && generation == m.autoGeneration
}

func (m Model) autoInteractionPaused() bool {
	return m.overlay != overlayNone || m.filtering || m.filter.Value() != ""
}

func (m Model) autoRefreshPaused() bool {
	return m.autoInteractionPaused() || m.loading || m.refreshing
}

func (m Model) overviewRequestInFlight() bool {
	return anyLoading(m.lbDetailLoading) || anyLoading(m.lbStatsLoading) ||
		anyLoading(m.lbFIPLoading) || anyLoading(m.lbAmphoraLoading) ||
		anyLoading(m.lbListenersLoading) || anyLoading(m.lbPoolsLoading) ||
		anyLoading(m.autoStatsLoading)
}

func anyLoading(values map[string]bool) bool {
	for _, loading := range values {
		if loading {
			return true
		}
	}
	return false
}

func (m Model) toggleAutoRefresh() (tea.Model, tea.Cmd) {
	m.autoRefreshEnabled = !m.autoRefreshEnabled
	m.autoGeneration++
	if !m.autoRefreshEnabled {
		m.statsSpinnerRunning = false
		flashCmd := m.setFlash("auto-refresh: off", false)
		return m, flashCmd
	}
	refreshCmd := m.scheduleAutoRefresh()
	staleStatsCmd := m.refreshStaleStatsCmd()
	spinnerCmd := m.ensureStatsSpinner()
	flashCmd := m.setFlash("auto-refresh: "+m.autoRefreshInterval().String(), false)
	return m, tea.Batch(
		refreshCmd,
		staleStatsCmd,
		spinnerCmd,
		flashCmd,
	)
}

// refreshStaleStatsCmd reconciles an overdue sample immediately when automatic
// refresh is enabled. The regular timer still owns all subsequent samples.
func (m *Model) refreshStaleStatsCmd() tea.Cmd {
	if !m.autoRefreshEnabled || !m.isLBOverview() || m.refreshing || m.autoRefreshPaused() {
		return nil
	}
	lbID := m.loc.node.ID
	if m.lbStatsLoading[lbID] || m.autoStatsLoading[lbID] {
		return nil
	}
	updated := m.updatedAt(lbID, sectionStats)
	if m.lbStatsErr[lbID] == "" && m.statsWithinAutoInterval(updated) {
		return nil
	}
	m.autoStatsLoading[lbID] = true
	return m.autoStatsCmd(lbID)
}

func (m Model) changeAutoRefreshInterval(delta int) (tea.Model, tea.Cmd) {
	next := m.autoIntervalIndex + delta
	if next < 0 {
		next = 0
	}
	if next >= len(autoRefreshIntervals) {
		next = len(autoRefreshIntervals) - 1
	}
	m.autoIntervalIndex = next
	// Invalidate the existing timers even at an interval boundary; otherwise
	// repeated +/- presses could schedule duplicate timers for one generation.
	m.autoGeneration++
	state := m.autoRefreshInterval().String()
	if !m.autoRefreshEnabled {
		state += " (off)"
		flashCmd := m.setFlash("auto-refresh interval: "+state, false)
		return m, flashCmd
	}
	refreshCmd := m.scheduleAutoRefresh()
	staleStatsCmd := m.refreshStaleStatsCmd()
	spinnerCmd := m.ensureStatsSpinner()
	flashCmd := m.setFlash("auto-refresh interval: "+state, false)
	return m, tea.Batch(
		refreshCmd,
		staleStatsCmd,
		spinnerCmd,
		flashCmd,
	)
}

func (m Model) autoRefreshLabel() string {
	if !m.autoRefreshEnabled {
		return "refresh: manual"
	}
	state := m.autoRefreshInterval().String() + "/" + fullAutoRefreshInterval.String()
	if m.autoInteractionPaused() {
		state += ", paused"
	}
	return fmt.Sprintf("refresh: auto (%s)", state)
}

func (m Model) styledAutoRefreshLabel() string {
	label := m.autoRefreshLabel()
	if !m.autoRefreshEnabled {
		return m.st.refreshManual.Render(label)
	}
	if m.autoInteractionPaused() {
		return m.st.refreshPaused.Render(label)
	}
	return m.st.refreshAuto.Render(label)
}
