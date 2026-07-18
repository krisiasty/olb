package tui

import (
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
