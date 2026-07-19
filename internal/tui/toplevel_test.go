package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// headerLine returns the table header row (the line carrying a known column).
func headerLine(view, marker string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, marker) {
			return l
		}
	}
	return ""
}

func TestTopLevelViewsSwitchByNumberKey(t *testing.T) {
	tests := []struct {
		key       string
		root      string   // breadcrumb root label
		marker    string   // a column that identifies the header row
		columns   []string // headers that must be present
		rowSample string   // a value that must appear in the body
	}{
		{"2", "virtual IPs", "ADDRESS", []string{"ADDRESS", "PORT ID", "SUBNET", "NETWORK", "LOAD BALANCER"}, "203.0.113.9"},
		{"3", "listeners", "PROTOCOL", []string{"NAME", "PROTOCOL", "PORT", "LOAD BALANCER", "PROVISIONING", "OPERATING"}, "http"},
		{"4", "pools", "ALGORITHM", []string{"NAME", "PROTOCOL", "ALGORITHM", "MEMBERS", "LOAD BALANCER"}, "web"},
		{"5", "amphorae", "AMPHORA ID", []string{"AMPHORA ID", "ROLE", "STATUS", "LB NETWORK IP", "HA IP", "COMPUTE ID"}, "MASTER"},
		{"1", "load balancers", "PROVIDER", []string{"NAME", "PROVIDER", "VIP"}, "lb1"},
	}
	for _, tt := range tests {
		m := start(t, osclient.SwitchCapability{CanSwitch: true})
		m = updExec(t, m, press(tt.key))
		view := ansiRE.ReplaceAllString(m.View(), "")

		if root := ansiRE.ReplaceAllString(m.breadcrumbLine(), ""); !strings.HasPrefix(strings.TrimSpace(root), tt.root) {
			t.Errorf("key %s breadcrumb root = %q, want prefix %q", tt.key, root, tt.root)
		}
		header := headerLine(view, tt.marker)
		if header == "" {
			t.Fatalf("key %s: no header row with %q in view:\n%s", tt.key, tt.marker, view)
		}
		for _, col := range tt.columns {
			if !strings.Contains(header, col) {
				t.Errorf("key %s header missing %q: %q", tt.key, col, header)
			}
		}
		if !strings.Contains(view, tt.rowSample) {
			t.Errorf("key %s: expected %q in body:\n%s", tt.key, tt.rowSample, view)
		}
	}
}

func TestVIPViewExpandsAdditionalVIPs(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("2"))
	view := ansiRE.ReplaceAllString(m.View(), "")
	// lb-1 contributes its primary VIP and one additional VIP.
	for _, addr := range []string{"203.0.113.9", "203.0.114.9"} {
		if !strings.Contains(view, addr) {
			t.Errorf("VIP view missing address %q:\n%s", addr, view)
		}
	}
}

func TestTopLevelDrillIn(t *testing.T) {
	// Listener drills into its own node; the breadcrumb keeps the listeners root.
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("3"))
	if idx, ok := m.selectLabel("listener:http"); ok {
		m.cursor = idx
	} else {
		t.Fatal("listener row not found")
	}
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.ID != "lsn-1" {
		t.Fatalf("listener drill-in landed on %+v, want node lsn-1", m.loc.id)
	}
	if root := ansiRE.ReplaceAllString(m.breadcrumbLine(), ""); !strings.Contains(root, "listeners") {
		t.Errorf("breadcrumb root after listener drill-in = %q, want listeners", root)
	}

	// Amphora drills into its owning load balancer (amphorae aren't tree nodes).
	m = start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("5"))
	m.cursor = firstSelectableIndex(m.entries)
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeLoadBalancer || m.loc.node.ID != "lb-1" {
		t.Fatalf("amphora drill-in landed on %+v, want owning LB lb-1", m.loc.id)
	}
}

func TestAmphoraeAdminRequiredMessage(t *testing.T) {
	m := New(&fakeBackend{cap: osclient.SwitchCapability{CanSwitch: true}, amphoraeErr: osclient.ErrAdminRequired}, Config{HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	m = updExec(t, m, press("5"))
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "admin") {
		t.Errorf("amphorae view should explain the admin requirement; got:\n%s", view)
	}
}
