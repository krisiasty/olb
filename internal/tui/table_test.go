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
	(&m).setLBLocation()
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

func TestResourceNavigationRowsShowGraphDirection(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // load balancer
	i, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("missing listener")
	}
	m.cursor = i
	m = updExec(t, m, press("enter")) // listener

	view := ansiRE.ReplaceAllString(m.View(), "")
	refLine := lineContaining(view, "pool:web")
	if !strings.Contains(refLine, "→") || !strings.Contains(refLine, "Pool") {
		t.Errorf("outgoing resource link should show its direction and relationship: %q", refLine)
	}

	i, ok = m.selectLabel("pool:web")
	if !ok {
		t.Fatal("missing pool reference")
	}
	m.cursor = i
	m = updExec(t, m, press("enter")) // pool
	view = ansiRE.ReplaceAllString(m.View(), "")
	backRefLine := lineContaining(view, "←")
	if !strings.Contains(backRefLine, "listener:http") || !strings.Contains(backRefLine, "Pool") {
		t.Errorf("incoming resource link should show its direction and relationship: %q", backRefLine)
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
