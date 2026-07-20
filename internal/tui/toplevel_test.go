package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

func TestListenerTableHumanizesTerminatedHTTPS(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("3"))
	for _, e := range m.entries {
		if e.kind != entListener || e.listener.ID != "lsn-2" {
			continue
		}
		cells := m.rowCells(e)
		if cells[1] != "HTTPS (TLS termination)" || cells[2] != "443" {
			t.Fatalf("terminated HTTPS listener cells = %q / %q, want humanized protocol and separate port", cells[1], cells[2])
		}
		return
	}
	t.Fatal("terminated HTTPS listener row not found")
}

func TestAmphoraEntriesFilterThroughVisibleLoadBalancers(t *testing.T) {
	visible := model.NewNode(model.TypeAmphora, "amp-visible", "amp-visible")
	visible.OwningLBID = "lb-visible"
	hidden := model.NewNode(model.TypeAmphora, "amp-hidden", "amp-hidden")
	hidden.OwningLBID = "lb-hidden"
	nodes := []*model.Node{visible, hidden}

	filtered := amphoraEntries(nodes, map[string]string{"lb-visible": "frontend"}, true)
	if len(filtered) != 1 || filtered[0].node.ID != "amp-visible" {
		t.Fatalf("filtered amphorae = %+v", filtered)
	}
	if all := amphoraEntries(nodes, map[string]string{"lb-visible": "frontend"}, false); len(all) != 2 {
		t.Fatalf("global amphorae count = %d, want 2", len(all))
	}
}

func TestTopLevelWorkspacesKeepIndependentHistory(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	lbHistory := m.hist
	m = updExec(t, m, press("enter")) // LB workspace: LB overview
	if idx, ok := m.selectLabel("listener:http"); ok {
		m.cursor = idx
	} else {
		t.Fatal("LB workspace listener row not found")
	}
	m = updExec(t, m, press("enter")) // LB workspace: listener detail
	if got := len(lbHistory.entries); got != 3 {
		t.Fatalf("LB workspace history length = %d, want 3", got)
	}

	m = updExec(t, m, press("3"))
	listenerHistory := m.hist
	if listenerHistory == lbHistory {
		t.Fatal("listener and LB views share one history")
	}
	if got := len(listenerHistory.entries); got != 1 {
		t.Fatalf("new listener workspace history length = %d, want root only", got)
	}
	if !m.loc.isTopLevelList() || m.loc.listKind() != kindListener {
		t.Fatalf("listener workspace opened at %+v", m.loc)
	}
	if idx, ok := m.selectLabel("listener:http"); ok {
		m.cursor = idx
	} else {
		t.Fatal("top-level listener row not found")
	}
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeListener {
		t.Fatalf("listener workspace drill-in landed on %+v", m.loc)
	}
	if got := len(listenerHistory.entries); got != 2 {
		t.Fatalf("listener workspace history length = %d, want 2", got)
	}

	// Switching restores the LB workspace at its listener without adding a root
	// entry; Back therefore returns to the LB overview, not the listener list.
	m = updExec(t, m, press("1"))
	if m.hist != lbHistory || len(m.hist.entries) != 3 || m.loc.node == nil || m.loc.node.Type != model.TypeListener {
		t.Fatalf("LB workspace was not restored: history=%p loc=%+v", m.hist, m.loc)
	}
	m = updExec(t, m, press("esc"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeLoadBalancer {
		t.Fatalf("LB workspace Back landed on %+v, want LB overview", m.loc)
	}

	// The listener workspace independently resumes at its own listener detail.
	m = updExec(t, m, press("3"))
	if m.hist != listenerHistory || len(m.hist.entries) != 2 || m.loc.node == nil || m.loc.node.Type != model.TypeListener {
		t.Fatalf("listener workspace was not restored: history=%p loc=%+v", m.hist, m.loc)
	}
	m = updExec(t, m, press("esc"))
	if !m.loc.isTopLevelList() || m.loc.listKind() != kindListener {
		t.Fatalf("listener workspace Back landed on %+v, want listener list", m.loc)
	}
}

func TestTopLevelWorkspacesRestoreSelectionAndFilters(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	if idx, ok := m.selectLabel("lb2"); ok {
		m.cursor = idx
	} else {
		t.Fatal("lb2 row not found")
	}
	m.filter.SetValue("lb")
	m.status = statusError
	m.applyFilters()
	lbSelection := m.entries[m.cursor].selection()

	m = updExec(t, m, press("2"))
	m.filter.SetValue("203.0.114")
	m.applyFilters()
	if len(m.entries) != 1 {
		t.Fatalf("VIP filter left %d rows, want 1", len(m.entries))
	}
	vipSelection := m.entries[m.cursor].selection()

	m = updExec(t, m, press("1"))
	if m.filter.Value() != "lb" || m.status != statusError || !m.entries[m.cursor].selection().equal(lbSelection) {
		t.Fatalf("LB workspace state not restored: filter=%q status=%s cursor=%d", m.filter.Value(), m.status, m.cursor)
	}
	m = updExec(t, m, press("2"))
	if m.filter.Value() != "203.0.114" || len(m.entries) != 1 || !m.entries[m.cursor].selection().equal(vipSelection) {
		t.Fatalf("VIP workspace state not restored: filter=%q entries=%d cursor=%d", m.filter.Value(), len(m.entries), m.cursor)
	}
}

func TestActiveTopLevelKeyIsNoOpAndCtrlHomeUsesWorkspaceRoot(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("3"))
	if idx, ok := m.selectLabel("listener:http"); ok {
		m.cursor = idx
	} else {
		t.Fatal("listener row not found")
	}
	m = updExec(t, m, press("enter"))
	beforeLen := len(m.hist.entries)
	m = updExec(t, m, press("3"))
	if len(m.hist.entries) != beforeLen || m.loc.node == nil || m.loc.node.Type != model.TypeListener {
		t.Fatalf("active workspace key changed navigation: len=%d loc=%+v", len(m.hist.entries), m.loc)
	}
	m = updExec(t, m, tea.KeyMsg{Type: tea.KeyCtrlHome})
	if m.activeWorkspace != kindListener || !m.loc.isTopLevelList() || m.loc.listKind() != kindListener {
		t.Fatalf("ctrl+home left the active workspace: active=%v loc=%+v", m.activeWorkspace, m.loc)
	}
	if current, ok := m.hist.current(); !ok || !current.id.Equal(model.ListenerListIdentity) {
		t.Fatalf("listener workspace root history entry = %+v, ok=%v", current, ok)
	}
	if len(m.hist.entries) != beforeLen || m.hist.cursor != 0 || !m.hist.canForward() {
		t.Fatalf("ctrl+home should preserve listener forward history: %+v", m.hist)
	}

	m = upd(t, m, press("h"))
	if m.pickCursor != m.hist.cursor {
		t.Fatalf("history picker cursor = %d, want current history entry %d", m.pickCursor, m.hist.cursor)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "History · listeners") {
		t.Fatalf("history picker does not identify active workspace:\n%s", view)
	} else if !strings.Contains(view, "(here)") {
		t.Fatalf("selected current history row is not marked as here:\n%s", view)
	}
}

func TestHistoryPickerPageAndBoundaryNavigation(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m.height = 12 // five history rows are visible in the picker
	for i := 0; i < 11; i++ {
		m.hist.navigate(histEntry{id: model.Identity{
			Type:       model.TypeListener,
			ID:         fmt.Sprintf("listener-%02d", i),
			OwningLBID: "lb-1",
			Label:      fmt.Sprintf("listener:%02d", i),
		}})
	}

	m = upd(t, m, press("h"))
	last := len(m.pickerItems()) - 1
	if m.pickCursor != last {
		t.Fatalf("initial picker cursor = %d, want current entry %d", m.pickCursor, last)
	}
	if m.search.Focused() {
		t.Fatal("history search should be inactive until / is pressed")
	}
	m = upd(t, m, press("ignored"))
	if m.search.Value() != "" || m.pickCursor != last {
		t.Fatalf("ordinary key changed inactive history search or cursor: query=%q cursor=%d", m.search.Value(), m.pickCursor)
	}

	m = upd(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.pickCursor != 0 {
		t.Fatalf("ctrl+a/home cursor = %d, want 0", m.pickCursor)
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.pickCursor != 5 {
		t.Fatalf("page down cursor = %d, want 5", m.pickCursor)
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.pickCursor != last {
		t.Fatalf("page down past end cursor = %d, want %d", m.pickCursor, last)
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if want := last - 5; m.pickCursor != want {
		t.Fatalf("page up cursor = %d, want %d", m.pickCursor, want)
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if m.pickCursor != last {
		t.Fatalf("ctrl+e/end cursor = %d, want %d", m.pickCursor, last)
	}
	m = upd(t, m, press("/"))
	if !m.search.Focused() {
		t.Fatal("/ should activate history search")
	}
	m = upd(t, m, press("listener:10"))
	if got := len(m.pickerItems()); got != 1 {
		t.Fatalf("filtered history has %d items, want 1", got)
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if m.search.Position() != 0 {
		t.Fatalf("ctrl+a/home should move the active search cursor to the beginning, got %d", m.search.Position())
	}
	m = upd(t, m, press("enter"))
	if m.search.Focused() || m.search.Value() != "listener:10" {
		t.Fatalf("enter should retain and leave history filter: focused=%v query=%q", m.search.Focused(), m.search.Value())
	}
	m = upd(t, m, press("esc"))
	if m.overlay != overlayPicker || m.search.Value() != "" {
		t.Fatalf("first esc should clear retained filter and keep picker open: overlay=%v query=%q", m.overlay, m.search.Value())
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.pickCursor != last {
		t.Fatalf("physical end cursor = %d, want %d", m.pickCursor, last)
	}
}

func TestWorkspaceHistoryRetainsRootIdentityAfterCapEviction(t *testing.T) {
	state := newWorkspaceState(kindPool, 2)
	state.hist.navigate(histEntry{id: model.Identity{Type: model.TypePool, ID: "pool-1", OwningLBID: "lb-1"}})
	state.hist.navigate(histEntry{id: model.Identity{Type: model.TypeMember, ID: "member-1", OwningLBID: "lb-1"}})
	if len(state.hist.entries) != 2 || !state.hist.entries[0].id.Equal(model.PoolListIdentity) ||
		state.hist.entries[1].id.ID != "member-1" {
		t.Fatalf("capped pool history did not pin root and retain newest entry: %+v", state.hist.entries)
	}
	if got := state.hist.rootIdentity(); !got.Equal(model.PoolListIdentity) {
		t.Fatalf("capped pool workspace root = %+v, want %+v", got, model.PoolListIdentity)
	}
}

func TestProjectScopeChangeResetsEveryWorkspaceHistoryAndKeepsActiveView(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	m = updExec(t, m, press("3"))
	if idx, ok := m.selectLabel("listener:http"); ok {
		m.cursor = idx
	}
	m = updExec(t, m, press("enter"))
	m = upd(t, m, switchedMsg{project: osclient.ProjectInfo{ID: "new-project", Name: "new-project"}})

	if m.activeWorkspace != kindListener || m.hist != m.workspaces[kindListener].hist || !m.loc.id.Equal(model.ListenerListIdentity) {
		t.Fatalf("scope change did not return to the active listener root: active=%v loc=%+v", m.activeWorkspace, m.loc)
	}
	if !m.loading || m.loadingWhat != "listeners" {
		t.Fatalf("scope change started %q loading=%v, want listeners", m.loadingWhat, m.loading)
	}
	for _, kind := range topLevelKinds {
		state := m.workspaces[kind]
		if len(state.hist.entries) != 1 || state.hist.cursor != 0 {
			t.Errorf("%s workspace history was not reset: %+v", kind.rootLabel(), state.hist)
		}
		current, ok := state.hist.current()
		if !ok || !current.id.Equal(kind.identity()) {
			t.Errorf("%s workspace root = %+v, ok=%v", kind.rootLabel(), current.id, ok)
		}
	}
}

func TestProjectScopeChangeKeepsEachTopLevelView(t *testing.T) {
	tests := []struct {
		key     string
		kind    listKind
		loading string
	}{
		{"1", kindLB, "load balancers"},
		{"2", kindVIP, "load balancers"},
		{"3", kindListener, "listeners"},
		{"4", kindPool, "pools"},
		{"5", kindAmphora, "amphorae"},
	}
	for _, tt := range tests {
		t.Run(tt.kind.rootLabel(), func(t *testing.T) {
			m := start(t, osclient.SwitchCapability{CanSwitch: true})
			if tt.kind != kindLB {
				m = updExec(t, m, press(tt.key))
			}
			m = upd(t, m, switchedMsg{project: osclient.ProjectInfo{ID: "p2", Name: "beta"}})
			if m.activeWorkspace != tt.kind || !m.loc.id.Equal(tt.kind.identity()) {
				t.Fatalf("scope change landed at active=%v loc=%+v, want %v root", m.activeWorkspace, m.loc, tt.kind)
			}
			if !m.loading || m.loadingWhat != tt.loading {
				t.Fatalf("scope change started %q loading=%v, want %q", m.loadingWhat, m.loading, tt.loading)
			}
		})
	}
}

func TestAsyncReferenceCompletionStaysInOriginatingWorkspace(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // cache lb-1 tree in the LB workspace
	lbHistory := m.hist
	listenerCount := len(m.workspaces[kindListener].hist.entries)

	m = updExec(t, m, press("3"))
	resolved := model.NewNode(model.TypeInstance, "instance-async", "async-instance")
	m = upd(t, m, refResolveMsg{
		sourceID: "lb-1", lbID: "lb-1", workspace: kindLB,
		label: "instance", node: resolved,
	})
	if m.activeWorkspace != kindListener || m.hist != m.workspaces[kindListener].hist ||
		len(m.hist.entries) != listenerCount {
		t.Fatalf("inactive reference completion disturbed listener workspace: active=%v history=%+v", m.activeWorkspace, m.hist)
	}
	if current, ok := lbHistory.current(); !ok || current.id.ID != "instance-async" {
		t.Fatalf("LB workspace did not receive resolved reference: current=%+v ok=%v", current, ok)
	}

	m = updExec(t, m, press("1"))
	if m.loc.node == nil || m.loc.node.ID != "instance-async" || m.activeWorkspace != kindLB {
		t.Fatalf("LB workspace did not resume at resolved instance: active=%v loc=%+v", m.activeWorkspace, m.loc)
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

func TestVIPDetailOverviewShowsNetworkFactsAndOwningLoadBalancer(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("2"))
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeVIP {
		t.Fatalf("expected VIP detail location, got %+v", m.loc.node)
	}
	if !m.lbDetailLoading[m.loc.node.ID] || !m.lbFIPLoading["lb-1"] {
		t.Fatal("opening a VIP should load Neutron details and its floating IP")
	}
	m = upd(t, m, detailMsg{
		nodeID: m.loc.node.ID, lbID: "lb-1", intent: intentOverview,
		res: osclient.DetailResult{Attrs: map[string]string{
			"port_name": "octavia-vip-port", "port_id": "port-9",
			"subnet_name": "public-subnet", "subnet_id": "subnet-9",
			"network_name": "public-network", "network_id": "network-9",
			"security_group_ids": "sg-1, sg-2",
		}},
	})
	m = upd(t, m, lbFloatingIPMsg{lbID: "lb-1", nodes: map[string]*model.Node{
		"203.0.113.9": newFloatingIP("198.51.100.7"),
	}})

	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"DETAILS", "Primary VIP", "203.0.113.9", "198.51.100.7",
		"octavia-vip-port", "port-9", "public-subnet", "subnet-9",
		"public-network", "network-9", "sg-1, sg-2", "RELATED OBJECTS 1",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("VIP detail missing %q:\n%s", want, view)
		}
	}
	var firstGroupRow, secondGroupRow string
	for _, line := range strings.Split(view, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "VIP") && strings.Contains(trimmed, "PORT"):
			firstGroupRow = trimmed
		case strings.HasPrefix(trimmed, "SUBNET") && strings.Contains(trimmed, "NETWORK"):
			secondGroupRow = trimmed
		}
	}
	if firstGroupRow == "" || secondGroupRow == "" {
		t.Fatalf("wide VIP details should use paired grouped columns:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	floatingAt, projectAt := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "Floating IP") {
			floatingAt = i
		}
		if strings.Contains(line, "Project name") {
			projectAt = i
		}
	}
	if floatingAt < 0 || projectAt != floatingAt+2 || strings.TrimSpace(lines[floatingAt+1]) != "" {
		t.Fatalf("project ownership should be separated from VIP addresses by a blank line:\n%s", view)
	}
	if len(m.entries) != 1 || m.entries[0].kind != entRelated || m.entries[0].node.ID != "lb-1" {
		t.Fatalf("VIP related rows = %+v, want only owning load balancer", m.entries)
	}
	line := navigationLineContaining(view, "lb1")
	for _, want := range []string{
		"Load balancer", "lb1 (lb-1)", "amphora", "1 listener", "1 pool",
		"1 pool · DEGRADED, ACTIVE",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("owning load balancer row missing %q: %q", want, line)
		}
	}
	if strings.Contains(line, "203.0.113.9") || strings.Contains(line, "198.51.100.7") {
		t.Errorf("owning load balancer row repeats VIP details: %q", line)
	}
	selectedStatusMarker := m.st.refMarker.Render("▶ ") +
		lipgloss.NewStyle().Foreground(statusColor("DEGRADED")).Render("●")
	if !strings.Contains(m.View(), selectedStatusMarker) {
		t.Errorf("selected owning load balancer row does not retain its status-colored dot:\n%s", m.View())
	}

	m.width = 80
	m.loc.node.Parent.Name = "load-balancer-with-an-excessively-long-diagnostic-name-that-must-not-hide-status"
	narrowView := ansiRE.ReplaceAllString(m.View(), "")
	line = navigationLineContaining(narrowView, "Load balancer")
	for _, want := range []string{"…", "(lb-1)", "1 pool · DEGRADED, ACTIVE"} {
		if !strings.Contains(line, want) {
			t.Errorf("long-name LB row should preserve %q: %q", want, line)
		}
	}
	if strings.Contains(line, m.loc.node.Parent.Name) {
		t.Errorf("long LB name was not truncated: %q", line)
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
