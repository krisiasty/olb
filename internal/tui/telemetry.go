package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/telemetry"
)

type telemetryTickMsg struct{ generation uint64 }

func telemetryTickCmd(generation uint64, interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return telemetryTickMsg{generation: generation}
	})
}

func (m Model) telemetryInterval() time.Duration {
	index := m.telemetryIntervalIndex
	if index < 0 || index >= len(autoRefreshIntervals) {
		index = defaultAutoRefreshIntervalIndex
	}
	return autoRefreshIntervals[index]
}

func (m Model) telemetryBackend() (TelemetryBackend, bool) {
	backend, ok := m.backend.(TelemetryBackend)
	return backend, ok
}

func (m Model) openTelemetry() (tea.Model, tea.Cmd) {
	m.overlay = overlayTelemetry
	m.telemetryGeneration++
	m.refreshTelemetryContent(true)
	if !m.telemetryAutoEnabled {
		return m, nil
	}
	return m, telemetryTickCmd(m.telemetryGeneration, m.telemetryInterval())
}

func (m Model) onTelemetryTick(msg telemetryTickMsg) (tea.Model, tea.Cmd) {
	if m.overlay != overlayTelemetry || !m.telemetryAutoEnabled || msg.generation != m.telemetryGeneration {
		return m, nil
	}
	m.refreshTelemetryContent(false)
	return m, telemetryTickCmd(msg.generation, m.telemetryInterval())
}

func (m Model) onTelemetryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Cancel), key.Matches(msg, m.keys.Quit), key.Matches(msg, m.keys.Telemetry):
		m.telemetryGeneration++
		m.overlay = overlayNone
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		m.refreshTelemetryContent(false)
		return m, nil
	case key.Matches(msg, m.keys.Reset):
		if backend, ok := m.telemetryBackend(); ok {
			backend.ResetTelemetry()
		}
		m.refreshTelemetryContent(true)
		return m, nil
	case key.Matches(msg, m.keys.AutoRefresh):
		m.telemetryAutoEnabled = !m.telemetryAutoEnabled
		m.telemetryGeneration++
		if !m.telemetryAutoEnabled {
			return m, nil
		}
		m.refreshTelemetryContent(false)
		return m, telemetryTickCmd(m.telemetryGeneration, m.telemetryInterval())
	case key.Matches(msg, m.keys.IntervalUp):
		return m.changeTelemetryInterval(1)
	case key.Matches(msg, m.keys.IntervalDown):
		return m.changeTelemetryInterval(-1)
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) changeTelemetryInterval(delta int) (tea.Model, tea.Cmd) {
	next := m.telemetryIntervalIndex + delta
	if next < 0 {
		next = 0
	}
	if next >= len(autoRefreshIntervals) {
		next = len(autoRefreshIntervals) - 1
	}
	m.telemetryIntervalIndex = next
	m.telemetryGeneration++
	if !m.telemetryAutoEnabled {
		return m, nil
	}
	return m, telemetryTickCmd(m.telemetryGeneration, m.telemetryInterval())
}

func (m *Model) refreshTelemetryContent(gotoTop bool) {
	m.applicationTelemetry = telemetry.CaptureApplicationSnapshot()
	backend, ok := m.telemetryBackend()
	if ok {
		m.telemetrySnapshot = backend.TelemetrySnapshot()
		m.telemetryUpdatedAt = m.clock()
	} else {
		m.telemetrySnapshot = telemetry.Snapshot{}
		m.telemetryUpdatedAt = time.Time{}
	}
	m.rebuildTelemetryContent(gotoTop)
}

func (m *Model) rebuildTelemetryContent(gotoTop bool) {
	_, available := m.telemetryBackend()
	offset := m.vp.YOffset
	m.vp.Width = m.width
	m.vp.Height = m.height - 2
	if m.vp.Height < 1 {
		m.vp.Height = 1
	}
	m.vp.SetContent(m.telemetryContent(available))
	if gotoTop {
		m.vp.GotoTop()
	} else {
		m.vp.SetYOffset(offset)
	}
}

func (m Model) telemetryRefreshLabel() string {
	if !m.telemetryAutoEnabled {
		return m.st.refreshManual.Render("refresh: manual")
	}
	return m.st.refreshAuto.Render("refresh: auto (" + m.telemetryInterval().String() + ")")
}

func (m Model) telemetryView() string {
	title := m.st.overlayTitle.Render("Telemetry") + " · " + m.telemetryRefreshLabel()
	if freshness := m.freshnessLabel(m.telemetryUpdatedAt); freshness != "" {
		title += " · " + m.st.disabled.Render(freshness)
	}
	if threshold := m.telemetrySnapshot.SlowThreshold; threshold > 0 {
		title += " · " + m.st.disabled.Render("slow ≥"+formatTelemetryDuration(threshold))
	}
	footer := m.st.help.Render("r refresh · a auto/manual · +/- interval · z reset API · ↑/↓ scroll · esc/t/q close")
	return m.clip(title) + "\n" + m.vp.View() + "\n" + m.clip(footer)
}

func (m Model) telemetryContent(available bool) string {
	application := m.applicationTelemetryContent()
	api := m.apiTelemetryContent(available)
	if m.width >= 110 {
		const gap = 3
		leftWidth := (m.width - gap) * 2 / 5
		if leftWidth > 52 {
			leftWidth = 52
		}
		rightWidth := m.width - gap - leftWidth
		return "\n" + lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.NewStyle().Width(leftWidth).MaxWidth(leftWidth).Render(application),
			strings.Repeat(" ", gap),
			lipgloss.NewStyle().Width(rightWidth).MaxWidth(rightWidth).Render(api),
		)
	}
	return "\n" + application + "\n\n" + api
}

func (m Model) apiTelemetryContent(available bool) string {
	snapshot := m.telemetrySnapshot
	var b strings.Builder
	b.WriteString(m.st.groupHeading.Render("API REQUESTS"))
	b.WriteString("\n")
	if !available {
		b.WriteString("\n  " + m.st.disabled.Render("— API telemetry is unavailable for this backend —"))
		return b.String()
	}
	b.WriteString(m.st.title.Render(fmt.Sprintf("TOTAL %d", snapshot.Calls)))
	b.WriteString(m.st.statusBar.Render(" · "))
	b.WriteString(telemetryMetric("SUCCESS", snapshot.Successes, statusColor("ONLINE")))
	b.WriteString(m.st.statusBar.Render(" · "))
	b.WriteString(telemetryMetric("SLOW", snapshot.Slow, statusColor("DEGRADED")))
	b.WriteString(m.st.statusBar.Render(" · "))
	b.WriteString(telemetryMetric("TIMEOUT", snapshot.Timeouts, telemetryTimeoutColor()))
	b.WriteString(m.st.statusBar.Render(" · "))
	b.WriteString(telemetryMetric("ERROR", snapshot.Errors, statusColor("ERROR")))
	b.WriteString("\n")
	if !snapshot.StartedAt.IsZero() {
		b.WriteString(m.st.disabled.Render("collected since " + snapshot.StartedAt.Local().Format("2006-01-02 15:04:05") + " · SLOW overlaps response outcomes"))
		b.WriteString("\n")
	}
	if len(snapshot.Endpoints) == 0 {
		b.WriteString("\n  " + m.st.disabled.Render("— no completed API calls since reset —"))
		return b.String()
	}
	for _, endpoint := range snapshot.Endpoints {
		b.WriteString("\n")
		heading := m.st.groupHeading
		if color, ok := telemetryEndpointIssueColor(endpoint); ok {
			heading = heading.Foreground(color)
		}
		b.WriteString(heading.Render(endpoint.Endpoint))
		b.WriteString("\n  ")
		fmt.Fprintf(&b, "calls %d", endpoint.Calls)
		b.WriteString(m.st.statusBar.Render(" · "))
		b.WriteString(telemetryMetric("success", endpoint.Successes, statusColor("ONLINE")))
		b.WriteString(m.st.statusBar.Render(" · "))
		b.WriteString(telemetryMetric("slow", endpoint.Slow, statusColor("DEGRADED")))
		b.WriteString(m.st.statusBar.Render(" · "))
		b.WriteString(telemetryMetric("timeout", endpoint.Timeouts, telemetryTimeoutColor()))
		b.WriteString(m.st.statusBar.Render(" · "))
		b.WriteString(telemetryMetric("error", endpoint.Errors, statusColor("ERROR")))
		b.WriteString("\n  ")
		b.WriteString(m.st.attrs.Render(fmt.Sprintf("latency min %s · avg %s · median %s · p95 %s · p99 %s · max %s",
			formatTelemetryDuration(endpoint.Min), formatTelemetryDuration(endpoint.Average),
			formatTelemetryDuration(endpoint.Median), formatTelemetryDuration(endpoint.P95),
			formatTelemetryDuration(endpoint.P99), formatTelemetryDuration(endpoint.Max))))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) applicationTelemetryContent() string {
	snapshot := m.applicationTelemetry
	group := overviewGroup{title: "APPLICATION", fields: []overviewField{
		{label: "Uptime", value: formatTelemetryUptime(snapshot.Uptime), subheading: "RUNTIME"},
		{label: "Goroutines", value: currentAndMax(snapshot.Goroutines, snapshot.MaxGoroutines)},
		{label: "OS threads", value: currentAndMax(snapshot.Threads, snapshot.MaxThreads)},
		{label: "GOMAXPROCS", value: fmt.Sprint(snapshot.GOMAXPROCS)},
		{label: "Logical CPUs", value: fmt.Sprint(snapshot.LogicalCPUs)},
		{label: "Heap allocated", value: bytesCurrentAndMax(snapshot.HeapAlloc, snapshot.MaxHeapAlloc), breakBefore: true, subheading: "MEMORY"},
		{label: "Heap in use", value: bytesCurrentAndMax(snapshot.HeapInuse, snapshot.MaxHeapInuse)},
		{label: "Stack in use", value: bytesCurrentAndMax(snapshot.StackInuse, snapshot.MaxStackInuse)},
		{label: "Runtime reserved", value: bytesCurrentAndMax(snapshot.RuntimeSys, snapshot.MaxRuntimeSys)},
		{label: "Live heap objects", value: countCurrentAndMax(snapshot.HeapObjects, snapshot.MaxHeapObjects)},
	}}
	return strings.TrimRight(m.renderOverviewGroup(group, m.width), " \n")
}

func currentAndMax(current, maximum int) string {
	return fmt.Sprintf("%d (max %d)", current, maximum)
}

func bytesCurrentAndMax(current, maximum uint64) string {
	return fmt.Sprintf("%s (max %s)",
		formatTelemetryFraction(formatIEC(float64(current))),
		formatTelemetryFraction(formatIEC(float64(maximum))))
}

func countCurrentAndMax(current, maximum uint64) string {
	return fmt.Sprintf("%s (max %s)", formatStatCount(current), formatStatCount(maximum))
}

func formatTelemetryUptime(uptime time.Duration) string {
	if uptime < time.Second {
		return "0s"
	}
	return uptime.Truncate(time.Second).String()
}

func telemetryTimeoutColor() lipgloss.Color {
	return lipgloss.Color("135") // violet, distinct from red errors
}

func telemetryMetric(label string, count int, color lipgloss.Color) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	if count > 0 {
		style = style.Bold(true).Foreground(color)
	}
	return style.Render(fmt.Sprintf("%s %d", label, count))
}

func telemetryEndpointIssueColor(endpoint telemetry.EndpointStats) (lipgloss.Color, bool) {
	switch {
	case endpoint.Errors > 0:
		return statusColor("ERROR"), true
	case endpoint.Timeouts > 0:
		return telemetryTimeoutColor(), true
	case endpoint.Slow > 0:
		return statusColor("DEGRADED"), true
	default:
		return "", false
	}
}

func formatTelemetryDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0"
	}
	var formatted string
	switch {
	case duration < time.Millisecond:
		formatted = duration.Round(10 * time.Microsecond).String()
	case duration < time.Second:
		formatted = duration.Round(time.Millisecond).String()
	default:
		formatted = duration.Round(100 * time.Millisecond).String()
	}
	return formatTelemetryFraction(formatted)
}

func formatTelemetryFraction(value string) string {
	dot := strings.LastIndexByte(value, '.')
	if dot < 0 {
		return value
	}
	end := dot + 1
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end-dot == 2 {
		return value[:end] + "0" + value[end:]
	}
	return value
}
