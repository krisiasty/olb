package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/osclient"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func lbListModel(t *testing.T, all bool) Model {
	t.Helper()
	m := New(&fakeBackend{cap: osclient.SwitchCapability{CanSwitch: true}, all: all}, Config{HistoryCap: 50, AllProjects: all})
	m.Init()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 16})
	m = nm.(Model)
	m.lbs = []osclient.LB{
		{ID: "a1b2c3d4e5", Name: "web-prod", Provider: "amphora", VipAddress: "10.30.176.47", ProjectID: "p1", ProjectName: "payments", ProvisioningStatus: "ACTIVE", OperatingStatus: "ONLINE"},
		{ID: "f6a7b8c9d0", Name: "db-lb", Provider: "amphora", VipAddress: "10.30.176.48", ProjectID: "p2", ProjectName: "platform", ProvisioningStatus: "PENDING_UPDATE", OperatingStatus: "DEGRADED"},
	}
	m.lbsLoaded = true
	(&m).setTopLevelEntries()
	return m
}

func TestLBTableHeaderAndRows(t *testing.T) {
	m := lbListModel(t, true)
	view := ansiRE.ReplaceAllString(m.View(), "")
	lines := strings.Split(view, "\n")

	// The header row carries the column titles, including PROJECT in all-projects mode.
	var header string
	for _, l := range lines {
		if strings.Contains(l, "NAME") && strings.Contains(l, "OPERATING") {
			header = l
		}
	}
	if header == "" {
		t.Fatalf("no table header found in view:\n%s", view)
	}
	for _, col := range []string{"NAME", "PROJECT", "PROVIDER", "VIP", "PROVISIONING", "OPERATING"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q: %q", col, header)
		}
	}
	// Column order: NAME before PROJECT before OPERATING.
	if strings.Index(header, "NAME") > strings.Index(header, "PROJECT") ||
		strings.Index(header, "PROJECT") > strings.Index(header, "OPERATING") {
		t.Errorf("unexpected column order: %q", header)
	}
	// Rows carry the data, aligned to the same width as the header.
	if !strings.Contains(view, "web-prod") || !strings.Contains(view, "payments") {
		t.Errorf("expected LB rows with project column; view:\n%s", view)
	}
	// The table lines (header + data rows) are padded to the terminal width so
	// the selected-row highlight spans the full row.
	if w := len([]rune(header)); w != 96 {
		t.Errorf("header not width-96 (%d): %q", w, header)
	}
	for _, l := range lines {
		if strings.Contains(l, "web-prod") || strings.Contains(l, "db-lb") {
			if w := len([]rune(l)); w != 96 {
				t.Errorf("data row not width-96 (%d): %q", w, l)
			}
		}
	}
}

func TestEmptyTopLevelListKeepsScopeSeparator(t *testing.T) {
	m := lbListModel(t, false)
	m.lbs = nil
	m.setTopLevelEntries()
	lines := m.bodyLines()
	if len(lines) < 2 || lines[0] != "" {
		t.Fatalf("empty top-level list should start with a blank separator: %q", lines)
	}
	if plain := ansiRE.ReplaceAllString(lines[1], ""); !strings.Contains(plain, "— empty —") {
		t.Fatalf("empty-state message should follow the separator: %q", lines)
	}
}

func TestLBTableNoProjectColumnSingleMode(t *testing.T) {
	m := lbListModel(t, false)
	view := ansiRE.ReplaceAllString(m.View(), "")
	var header string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "NAME") && strings.Contains(l, "OPERATING") {
			header = l
		}
	}
	if header == "" {
		t.Fatalf("no table header found")
	}
	if strings.Contains(header, "PROJECT") {
		t.Errorf("single-project mode should not show a PROJECT column: %q", header)
	}
}

func TestLBNameCellSwitchesBetweenNameAndID(t *testing.T) {
	if got := lbNameCell("web-prod", "a1b2c3d4e5f6", true); got != "a1b2c3d4e5f6" {
		t.Errorf("ID mode = %q, want the full id", got)
	}
	if got := lbNameCell("web-prod", "a1b2c3d4e5f6", false); got != "web-prod" {
		t.Errorf("name mode = %q, want the name", got)
	}
	if got := lbNameCell("", "a1b2c3d4e5f6", false); got != "a1b2c3d4" {
		t.Errorf("name mode with no name = %q, want the short id", got)
	}
}

func TestLBTableToggleShowsIDs(t *testing.T) {
	m := lbListModel(t, true)
	if !strings.Contains(m.hintLine(), "d names/ids") || !strings.Contains(helpContent(true, true, true, false), "toggle top-level tables") {
		t.Fatal("top-level tables should advertise the name/ID toggle")
	}

	headerOf := func(view string) string {
		for _, l := range strings.Split(view, "\n") {
			if strings.Contains(l, "OPERATING") {
				return l
			}
		}
		return ""
	}

	// Names by default.
	view := ansiRE.ReplaceAllString(m.View(), "")
	if h := headerOf(view); !strings.Contains(h, "NAME") || !strings.Contains(h, "PROJECT") || strings.Contains(h, "PROJECT ID") {
		t.Fatalf("default header = %q, want NAME/PROJECT", h)
	}
	if !strings.Contains(view, "web-prod") || !strings.Contains(view, "payments") {
		t.Fatalf("default view should show names; got:\n%s", view)
	}

	// Press d: columns switch to IDs, headers relabel.
	m = upd(t, m, press("d"))
	if !m.showIDs {
		t.Fatal("pressing d should enable ID mode")
	}
	view = ansiRE.ReplaceAllString(m.View(), "")
	if h := headerOf(view); !strings.Contains(h, "LOAD BALANCER ID") || !strings.Contains(h, "PROJECT ID") || strings.Contains(h, "NAME") {
		t.Fatalf("ID-mode header = %q, want LOAD BALANCER ID/PROJECT ID and no NAME", h)
	}
	if !strings.Contains(view, "a1b2c3d4e5") {
		t.Errorf("ID mode should show the full LB id; got:\n%s", view)
	}
	if strings.Contains(view, "web-prod") || strings.Contains(view, "payments") {
		t.Errorf("ID mode should not show names; got:\n%s", view)
	}

	// Press d again: back to names.
	m = upd(t, m, press("d"))
	if m.showIDs {
		t.Fatal("pressing d again should return to name mode")
	}
	view = ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "web-prod") || !strings.Contains(view, "payments") {
		t.Errorf("name mode should restore names; got:\n%s", view)
	}
}

func TestLayoutColumnWidthsKeepsNarrowColumnsReadable(t *testing.T) {
	titles := []string{"NAME", "PROTOCOL", "PORT", "LOAD BALANCER", "PROVISIONING", "OPERATING"}
	rows := [][]string{
		{strings.Repeat("x", 120), "TERMINATED_HTTPS", "443", "web-prod", "ACTIVE", "ONLINE"},
		{"http", "HTTP", "80", "web-prod", "ACTIVE", "ONLINE"},
	}
	for _, width := range []int{170, 120, 100, 80, 60} {
		w := layoutColumnWidths(titles, rows, width, tableColumnGap)
		if len(w) != len(titles) {
			t.Fatalf("width %d: got %d columns, want %d", width, len(w), len(titles))
		}
		// The row (columns + a gap per column) must exactly fill the terminal.
		sum := 0
		for _, cw := range w {
			if cw < 1 {
				t.Errorf("width %d: column collapsed to %d", width, cw)
			}
			sum += cw
		}
		if got := sum + tableColumnGap*len(titles); got != width {
			t.Errorf("width %d: row fills %d, want %d", width, got, width)
		}
		// PORT (col 2) and PROTOCOL (col 1) must never be starved by the long NAME.
		if w[2] < 4 {
			t.Errorf("width %d: PORT column starved to %d", width, w[2])
		}
		if w[1] < 4 {
			t.Errorf("width %d: PROTOCOL column starved to %d", width, w[1])
		}
	}
}

func TestResourceNavigationRows(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // open first load balancer
	amphorae, err := m.backend.ListAmphorae(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, amphoraeMsg{lbID: "lb-1", nodes: amphorae})
	view := ansiRE.ReplaceAllString(m.View(), "")

	tests := []struct {
		relation string
		target   string
	}{
		{relation: "Primary VIP", target: "203.0.113.9"},
		{relation: "Pool", target: "web"},
		{relation: "Listener", target: "http"},
		{relation: "Amphora", target: "11111111-1111-1111-1111-111111111111 (MASTER)"},
	}
	for _, tt := range tests {
		line := navigationLineContaining(view, tt.target)
		if line == "" {
			t.Errorf("missing navigation target %q in view:\n%s", tt.target, view)
			continue
		}
		if relationAt, targetAt := strings.Index(line, tt.relation), strings.Index(line, tt.target); relationAt < 0 || relationAt >= targetAt {
			t.Errorf("row should place relation %q before target %q: %q", tt.relation, tt.target, line)
		}
		trimmed := strings.TrimRight(line, " ")
		if !strings.HasSuffix(trimmed, "›") {
			t.Errorf("row should end with a navigation chevron: %q", line)
		}
		if width := len([]rune(line)); width > m.width {
			t.Errorf("row width = %d, exceeds terminal width %d: %q", width, m.width, line)
		}
		beforeChevron := strings.TrimSuffix(trimmed, "›")
		if !strings.HasSuffix(beforeChevron, "  ") {
			t.Errorf("navigation chevron should follow the content directly: %q", line)
		}
	}

	vipLine := navigationLineContaining(view, "203.0.113.9")
	if strings.Contains(vipLine, "vip:") || strings.Count(vipLine, "203.0.113.9") != 1 {
		t.Errorf("VIP row should not repeat its type or address: %q", vipLine)
	}
}

func TestListenerPoolsAndPoolListenersUseRelatedRepresentations(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // load balancer
	i, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("missing listener")
	}
	m.cursor = i
	m = updExec(t, m, press("enter")) // listener

	view := ansiRE.ReplaceAllString(m.View(), "")
	poolLine := lineContaining(view, "web")
	if !strings.Contains(poolLine, "●") || !strings.Contains(poolLine, "Pool") || strings.Contains(poolLine, "→") {
		t.Errorf("listener pool should use the normal status-dot pool representation: %q\n%s", poolLine, view)
	}

	i, ok = m.selectLabel("pool:web")
	if !ok {
		t.Fatal("missing pool reference")
	}
	m.cursor = i
	m = updExec(t, m, press("enter")) // pool
	items, err := m.backend.ListListenerSummaries(context.Background(), m.loc.node.OwningLBID)
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, listenerSummariesMsg{lbID: m.loc.node.OwningLBID, items: items})
	view = ansiRE.ReplaceAllString(m.View(), "")
	listenerLine := lineContaining(view, "● Listener")
	if !strings.Contains(listenerLine, "http") || !strings.Contains(listenerLine, "HTTPS/8443") ||
		strings.Contains(listenerLine, "listener:") || strings.Contains(listenerLine, "←") {
		t.Errorf("pool listener should use the load-balancer overview format: %q", listenerLine)
	}
}

func TestResourceNavigationRowsHandleNarrowWidths(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	for width := 1; width <= 12; width++ {
		m.width = width
		view := m.View() // must not panic while clipping styled and selected rows
		if view == "" {
			t.Fatalf("width %d produced an empty view", width)
		}
	}
}

func lineContaining(view, needle string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func navigationLineContaining(view, needle string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, needle) && strings.HasSuffix(strings.TrimRight(line, " "), "›") {
			return line
		}
	}
	return ""
}
