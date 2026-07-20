package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
	"github.com/krisiasty/olb/internal/telemetry"
)

// --- fake backend ---------------------------------------------------------

const sampleStatus = `
{"statuses":{"loadbalancer":{
  "name":"lb1","id":"lb-1","provisioning_status":"ACTIVE","operating_status":"DEGRADED",
  "listeners":[
    {"name":"http","id":"lsn-1","provisioning_status":"ACTIVE","operating_status":"DEGRADED",
     "pools":[{"name":"web","id":"pool-1","provisioning_status":"ACTIVE","operating_status":"DEGRADED",
       "healthmonitor":{"type":"HTTP","id":"hm-1","name":"hm","provisioning_status":"ACTIVE"},
       "members":[{"address":"10.0.0.5","protocol_port":80,"id":"mem-1","operating_status":"ERROR","provisioning_status":"ACTIVE"}]}],
     "l7policies":[{"action":"REDIRECT_TO_POOL","id":"pol-1","name":"api","provisioning_status":"ACTIVE",
       "rules":[{"type":"PATH","id":"rule-1","provisioning_status":"ACTIVE"}]}]}
  ],
  "pools":[{"name":"web","id":"pool-1","provisioning_status":"ACTIVE","operating_status":"DEGRADED",
     "healthmonitor":{"type":"HTTP","id":"hm-1","name":"hm","provisioning_status":"ACTIVE"},
     "members":[{"address":"10.0.0.5","protocol_port":80,"id":"mem-1","operating_status":"ERROR","provisioning_status":"ACTIVE"}]}]
}}}`

type fakeBackend struct {
	cap         osclient.SwitchCapability
	all         bool
	telemetry   *telemetry.Collector
	amphoraeErr error // when set, ListAllAmphorae returns it (e.g. ErrAdminRequired)
}

func newTree() *model.Tree {
	var w struct {
		Statuses model.StatusTree `json:"statuses"`
	}
	_ = json.Unmarshal([]byte(sampleStatus), &w.Statuses)
	// The wrapper key is "statuses"; unmarshal directly into StatusTree.
	var top struct {
		Statuses model.StatusTree `json:"statuses"`
	}
	_ = json.Unmarshal([]byte(sampleStatus), &top)
	return model.Build(&top.Statuses, model.LBMeta{
		VipAddress: "203.0.113.9", VipPortID: "port-9", VipSubnetID: "subnet-9", VipNetworkID: "network-9", Provider: "amphora",
		AdditionalVIPs: []model.AdditionalVIP{{Address: "203.0.114.9", SubnetID: "subnet-10"}},
		ProjectID:      "p1", ProjectName: "alpha",
	})
}

func newFloatingIP(address string) *model.Node {
	n := model.NewNode(model.TypeFloatingIP, "fip-"+address, address)
	n.SetAttr("floating_ip", address)
	n.DetailLoaded = true
	return n
}

func (f *fakeBackend) ListLoadBalancers(context.Context) ([]osclient.LB, error) {
	lbs := []osclient.LB{
		{ID: "lb-1", Name: "lb1", Provider: "amphora", VipAddress: "203.0.113.9", VipPortID: "port-9", AdditionalVIPs: []model.AdditionalVIP{{Address: "203.0.114.9", SubnetID: "subnet-10"}}, ProjectID: "p1", ProjectName: "alpha", ProvisioningStatus: "ACTIVE", OperatingStatus: "DEGRADED"},
		{ID: "lb-2", Name: "lb2", Provider: "ovn", ProjectID: "p1", ProjectName: "alpha", ProvisioningStatus: "ACTIVE", OperatingStatus: "ERROR"},
	}
	if f.all {
		lbs = append(lbs, osclient.LB{ID: "lb-3", Name: "lb3", Provider: "amphora", ProjectID: "p2", ProjectName: "beta", ProvisioningStatus: "ACTIVE", OperatingStatus: "ONLINE"})
	}
	return lbs, nil
}

func (f *fakeBackend) GetTree(_ context.Context, lbID string, _ *model.LBMeta) (*model.Tree, error) {
	return newTree(), nil
}

func (f *fakeBackend) FetchDetail(_ context.Context, n *model.Node) (osclient.DetailResult, error) {
	res := osclient.DetailResult{Raw: map[string]any{"id": n.ID, "name": n.Name}, Attrs: map[string]string{"probed": "yes"}}
	switch n.Type {
	case model.TypeLoadBalancer:
		res.Attrs["provider"] = "amphora"
		res.Attrs["vip_address"] = "203.0.113.9"
		res.Attrs["admin_state_up"] = "true"
		res.Attrs["flavor_id"] = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		res.Attrs["created_at"] = "2026-07-18T10:15:30Z"
		res.Attrs["updated_at"] = "2026-07-19T11:20:45Z"
	case model.TypeVIP:
		res.Attrs["port_name"] = "octavia-vip-port"
		res.Attrs["port_id"] = "port-9"
		res.Attrs["subnet_name"] = "public-subnet"
		res.Attrs["subnet_id"] = n.Attrs["subnet_id"]
		res.Attrs["network_name"] = "public-network"
		res.Attrs["network_id"] = "network-9"
		res.Attrs["security_group_ids"] = "sg-1, sg-2"
	case model.TypeListener:
		res.IsListener = true
		res.ListenerDefaultPoolID = "pool-1"
		res.Attrs["protocol"] = "TERMINATED_HTTPS"
		res.Attrs["port"] = "8443"
		res.Attrs["admin_state_up"] = "true"
		res.Attrs["connection_limit"] = "unlimited"
		res.Attrs["created_at"] = "2026-07-18T10:15:30Z"
		res.Attrs["updated_at"] = "2026-07-19T11:20:45Z"
		res.Attrs["certificate_name"] = "api.example.test"
		res.Attrs["certificate_subject"] = "api.example.test"
		res.Attrs["certificate_issuer"] = "Example CA"
		res.Attrs["certificate_not_before"] = "2026-06-01T00:00:00Z"
		res.Attrs["certificate_not_after"] = "2026-08-01T00:00:00Z"
		res.Attrs["sni_certificate_count"] = "0"
	case model.TypeL7Policy:
		res.IsL7Policy = true
		res.L7Action = "REDIRECT_TO_POOL"
		res.L7RedirectPoolID = "pool-1"
	}
	return res, nil
}

func (f *fakeBackend) LBStats(context.Context, string) (map[string]any, error) {
	return map[string]any{"active_connections": 3, "total_connections": 100, "bytes_in": 9, "bytes_out": 8, "request_errors": 1}, nil
}

func (f *fakeBackend) ListenerStats(context.Context, string, string) (map[string]any, error) {
	return map[string]any{"active_connections": 2, "total_connections": 80, "bytes_in": 7, "bytes_out": 6, "request_errors": 1}, nil
}

func (f *fakeBackend) ListListenerSummaries(context.Context, string) (map[string]osclient.ListenerSummary, error) {
	return map[string]osclient.ListenerSummary{
		"lsn-1": {ID: "lsn-1", Protocol: "TERMINATED_HTTPS", ProtocolPort: 8443},
	}, nil
}

func (f *fakeBackend) ListPoolSummaries(context.Context, string) (map[string]osclient.PoolSummary, error) {
	return map[string]osclient.PoolSummary{
		"pool-1": {
			ID: "pool-1", Name: "web", Protocol: "HTTP", LBMethod: "ROUND_ROBIN",
			MemberCount: 1, ProvisioningStatus: "ACTIVE", OperatingStatus: "DEGRADED",
			ListenerIDs: []string{"lsn-1"},
		},
		"pool-2": {
			ID: "pool-2", Name: "web", Protocol: "TCP", LBMethod: "LEAST_CONNECTIONS",
			MemberCount: 4, ProvisioningStatus: "ACTIVE", OperatingStatus: "ONLINE",
		},
	}, nil
}

func (f *fakeBackend) ResolveFloatingIPs(context.Context, string, string) (map[string]*model.Node, error) {
	return map[string]*model.Node{}, nil // internal LB: no floating IP
}

func (f *fakeBackend) ResolveInstance(_ context.Context, lbID, addr string) (*model.Node, error) {
	n := model.NewNode(model.TypeInstance, "srv-1", "web-server-1")
	n.SetAttr("address", addr)
	n.Raw = map[string]any{"id": "srv-1"}
	n.DetailLoaded = true
	return n, nil
}

func (f *fakeBackend) ListAmphorae(_ context.Context, lbID string) ([]*model.Node, error) {
	makeAmphora := func(id, role, managementIP, computeID string) *model.Node {
		a := model.NewNode(model.TypeAmphora, id, id)
		a.OwningLBID = lbID
		a.ProvisioningStatus = "ALLOCATED"
		a.SetAttr("role", role)
		a.SetAttr("status", "ALLOCATED")
		a.SetAttr("lb_network_ip", managementIP)
		a.SetAttr("compute_id", computeID)
		a.DetailLoaded = true
		a.Raw = map[string]any{
			"id": id, "role": role, "status": "ALLOCATED",
			"lb_network_ip": managementIP, "compute_id": computeID,
		}
		return a
	}
	return []*model.Node{
		makeAmphora("11111111-1111-1111-1111-111111111111", "MASTER", "10.0.3.20", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		makeAmphora("22222222-2222-2222-2222-222222222222", "BACKUP", "10.0.3.21", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
	}, nil
}

func (f *fakeBackend) ListListeners(context.Context) ([]osclient.ListenerRow, error) {
	return []osclient.ListenerRow{
		{ID: "lsn-1", Name: "http", Protocol: "HTTP", ProtocolPort: 80, LBID: "lb-1", ProjectID: "p1", ProvisioningStatus: "ACTIVE", OperatingStatus: "ONLINE"},
		{ID: "lsn-2", Name: "https", Protocol: "TERMINATED_HTTPS", ProtocolPort: 443, LBID: "lb-1", ProjectID: "p1", ProvisioningStatus: "ACTIVE", OperatingStatus: "DEGRADED"},
	}, nil
}

func (f *fakeBackend) ListPools(context.Context) ([]osclient.PoolRow, error) {
	return []osclient.PoolRow{
		{ID: "pool-1", Name: "web", Protocol: "HTTP", LBMethod: "ROUND_ROBIN", MemberCount: 2, LBID: "lb-1", ProjectID: "p1", ProvisioningStatus: "ACTIVE", OperatingStatus: "ONLINE"},
	}, nil
}

func (f *fakeBackend) ListAllAmphorae(_ context.Context) ([]*model.Node, error) {
	if f.amphoraeErr != nil {
		return nil, f.amphoraeErr
	}
	a := model.NewNode(model.TypeAmphora, "amp-1", "amp-1")
	a.OwningLBID = "lb-1"
	a.ProvisioningStatus = "ALLOCATED"
	a.SetAttr("role", "MASTER")
	a.SetAttr("status", "ALLOCATED")
	a.SetAttr("lb_network_ip", "10.0.3.20")
	a.SetAttr("compute_id", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	a.DetailLoaded = true
	a.Raw = map[string]any{"id": "amp-1", "loadbalancer_id": "lb-1", "role": "MASTER", "status": "ALLOCATED"}
	return []*model.Node{a}, nil
}

func (f *fakeBackend) ListProjects(context.Context) ([]osclient.ProjectInfo, error) {
	return []osclient.ProjectInfo{{ID: "p1", Name: "alpha"}, {ID: "p2", Name: "beta"}}, nil
}

func (f *fakeBackend) SwitchProject(_ context.Context, p osclient.ProjectInfo) error {
	f.all = false
	return nil
}
func (f *fakeBackend) EnterAllProjects(context.Context) error {
	f.all = true
	return nil
}
func (f *fakeBackend) CurrentProject() osclient.ProjectInfo {
	return osclient.ProjectInfo{ID: "p1", Name: "alpha"}
}
func (f *fakeBackend) AllProjects() bool                           { return f.all }
func (f *fakeBackend) SwitchCapability() osclient.SwitchCapability { return f.cap }
func (f *fakeBackend) TelemetrySnapshot() telemetry.Snapshot {
	if f.telemetry == nil {
		return telemetry.Snapshot{SlowThreshold: telemetry.DefaultSlowThreshold}
	}
	return f.telemetry.Snapshot()
}
func (f *fakeBackend) ResetTelemetry() {
	if f.telemetry != nil {
		f.telemetry.Reset()
	}
}

// --- driver helpers -------------------------------------------------------

func start(t *testing.T, cap osclient.SwitchCapability) Model {
	t.Helper()
	m := New(&fakeBackend{cap: cap}, Config{PrintMode: true, HistoryCap: 50})
	m.Init() // schedules initial list loading; New initializes workspace histories
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	return m
}

func mustLBs(t *testing.T, m Model) []osclient.LB {
	lbs, err := m.backend.ListLoadBalancers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return lbs
}

// upd applies a message and asserts View never panics and is non-empty.
func upd(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	out := nm.(Model)
	if v := out.View(); v == "" && !out.quitting {
		t.Fatalf("empty view after %T", msg)
	}
	return out
}

// updExec applies a message, then runs the single returned command and feeds
// its message back (used for the fetch round-trips).
func updExec(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, cmd := m.Update(msg)
	m = nm.(Model)
	_ = m.View()
	if cmd != nil {
		if next := cmd(); next != nil {
			if _, isBatch := next.(tea.BatchMsg); !isBatch {
				m = upd(t, m, next)
			}
		}
	}
	return m
}

func press(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func (m Model) selectLabel(label string) (int, bool) {
	for i, e := range m.entries {
		if strings.Contains(e.label, label) {
			return i, true
		}
	}
	return 0, false
}

// --- tests ----------------------------------------------------------------

func TestListAndDrillDown(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	if len(m.entries) != 2 {
		t.Fatalf("want 2 LBs, got %d", len(m.entries))
	}
	// Open the first LB (cache miss -> tree fetch).
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeLoadBalancer {
		t.Fatalf("expected to stand on the LB, loc=%+v", m.loc)
	}
	// LB status children include a VIP, pools, and listeners. Amphora VMs arrive
	// independently from the background overview request.
	for _, want := range []string{"vip:", "pool:web", "listener:http"} {
		if _, ok := m.selectLabel(want); !ok {
			t.Errorf("LB view missing %q; entries=%v", want, labels(m))
		}
	}
}

func TestAutoRefreshControlsAndStaleTimerInvalidation(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	if m.statsSpinner.Spinner.FPS != time.Second {
		t.Fatalf("stats cadence spinner interval = %s, want 1s", m.statsSpinner.Spinner.FPS)
	}
	for _, frame := range m.statsSpinner.Spinner.Frames {
		if width := len([]rune(frame)); width != 4 {
			t.Fatalf("stats cadence frame %q has width %d, want four points", frame, width)
		}
	}
	if !m.autoRefreshEnabled || m.autoRefreshInterval() != 5*time.Second {
		t.Fatalf("auto-refresh defaults = enabled:%v interval:%s", m.autoRefreshEnabled, m.autoRefreshInterval())
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "refresh: auto (5s/30s)") {
		t.Fatalf("subtitle does not show the auto-refresh interval:\n%s", view)
	}
	autoStyle := m.styledAutoRefreshLabel()
	if !strings.Contains(m.View(), autoStyle) {
		t.Fatalf("subtitle does not use the automatic-refresh style: %q", autoStyle)
	}

	m = upd(t, m, press("+"))
	if m.autoRefreshInterval() != 10*time.Second {
		t.Fatalf("+ interval = %s, want 10s", m.autoRefreshInterval())
	}
	m = upd(t, m, press("-"))
	if m.autoRefreshInterval() != 5*time.Second {
		t.Fatalf("- interval = %s, want 5s", m.autoRefreshInterval())
	}
	m = upd(t, m, press("="))
	if m.autoRefreshInterval() != 10*time.Second {
		t.Fatalf("= interval = %s, want 10s", m.autoRefreshInterval())
	}
	m = upd(t, m, press("-"))

	staleGeneration := m.autoGeneration
	m = upd(t, m, press("a"))
	if m.autoRefreshEnabled || m.autoRefreshLabel() != "refresh: manual" {
		t.Fatalf("a did not disable auto-refresh: enabled=%v label=%q", m.autoRefreshEnabled, m.autoRefreshLabel())
	}
	manualStyle := m.styledAutoRefreshLabel()
	if autoStyle == manualStyle || !strings.Contains(m.View(), manualStyle) {
		t.Fatalf("manual and automatic refresh modes should have distinct subtitle styles: auto=%q manual=%q", autoStyle, manualStyle)
	}
	next, cmd := m.Update(autoFullTickMsg{generation: staleGeneration})
	m = next.(Model)
	if cmd != nil || m.refreshing {
		t.Fatal("a stale timer started or rescheduled refresh after auto-refresh was disabled")
	}

	m = upd(t, m, press("a"))
	if !m.autoRefreshEnabled || m.autoRefreshInterval() != 5*time.Second {
		t.Fatal("a did not resume auto-refresh at the selected interval")
	}
}

func TestAutoStatsRefreshAndInteractionPause(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})

	next, cmd := m.Update(autoStatsTickMsg{generation: m.autoGeneration})
	m = next.(Model)
	if cmd == nil || !m.autoStatsLoading["lb-1"] {
		t.Fatal("stats timer did not start a lightweight overview stats refresh")
	}
	m = upd(t, m, statsMsg{
		lbID: "lb-1", automatic: true,
		stats: map[string]any{"active_connections": 7},
	})
	if m.autoStatsLoading["lb-1"] || m.lbStats["lb-1"]["active_connections"] != 7 {
		t.Fatalf("automatic stats response was not applied: loading=%v stats=%v", m.autoStatsLoading["lb-1"], m.lbStats["lb-1"])
	}

	m.filter.SetValue("listener")
	next, cmd = m.Update(autoStatsTickMsg{generation: m.autoGeneration})
	m = next.(Model)
	if cmd == nil || m.autoStatsLoading["lb-1"] {
		t.Fatal("active list filter should pause requests while continuing the timer")
	}
	if label := m.autoRefreshLabel(); label != "refresh: auto (5s/30s, paused)" {
		t.Fatalf("paused auto-refresh label = %q", label)
	}

	m.filter.SetValue("")
	m.status = statusError
	next, cmd = m.Update(autoStatsTickMsg{generation: m.autoGeneration})
	m = next.(Model)
	if cmd == nil || !m.autoStatsLoading["lb-1"] {
		t.Fatal("status filtering should not pause automatic stats refresh")
	}
	if label := m.autoRefreshLabel(); label != "refresh: auto (5s/30s)" {
		t.Fatalf("status filter incorrectly paused auto-refresh: %q", label)
	}
}

func TestStatsAreHumanizedAndRatesUseElapsedSampleTime(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	firstAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	m = upd(t, m, statsMsg{
		lbID: "lb-1", sampledAt: firstAt,
		stats: map[string]any{
			"active_connections": 1234,
			"total_connections":  10000,
			"request_errors":     2,
			"bytes_in":           1536,
			"bytes_out":          1024 * 1024,
		},
	})

	fields := statFieldValues(m.lbStatFields())
	for label, want := range map[string]string{
		"Active connections": "1,234",
		"Total connections":  "10,000",
		"Request errors":     "2",
		"Bytes in":           "1.5 KiB",
		"Bytes out":          "1 MiB",
	} {
		if got := fields[label]; got != want {
			t.Errorf("first %s = %q, want %q", label, got, want)
		}
	}

	m = upd(t, m, statsMsg{
		lbID: "lb-1", sampledAt: firstAt.Add(5 * time.Second), automatic: true,
		stats: map[string]any{
			"active_connections": 1300,
			"total_connections":  10015,
			"request_errors":     7,
			"bytes_in":           1536 + 10*1024,
			"bytes_out":          6 * 1024 * 1024,
		},
	})
	fields = statFieldValues(m.lbStatFields())
	for label, want := range map[string]string{
		"Active connections": "1,300",
		"Total connections":  "10,015 (+3/s)",
		"Request errors":     "7 (+5)",
		"Bytes in":           "11.5 KiB (2 KiB/s)",
		"Bytes out":          "6 MiB (1 MiB/s)",
	} {
		if got := fields[label]; got != want {
			t.Errorf("second %s = %q, want %q", label, got, want)
		}
	}

	// A lower value is a counter reset, not negative traffic. This sample is
	// retained as the next baseline but does not display a rate itself.
	m = upd(t, m, statsMsg{
		lbID: "lb-1", sampledAt: firstAt.Add(10 * time.Second), automatic: true,
		stats: map[string]any{
			"total_connections": 2,
			"request_errors":    1,
			"bytes_in":          512,
			"bytes_out":         1024,
		},
	})
	fields = statFieldValues(m.lbStatFields())
	for _, label := range []string{"Total connections", "Request errors", "Bytes in", "Bytes out"} {
		if strings.Contains(fields[label], "(") {
			t.Errorf("reset %s should not show a rate: %q", label, fields[label])
		}
	}
}

func statFieldValues(fields []overviewField) map[string]string {
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		values[field.label] = field.value
	}
	return values
}

func TestReenablingAutoRefreshImmediatelyRefreshesStaleStats(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	m = updExec(t, m, press("enter"))
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})

	m = upd(t, m, press("a"))
	now = now.Add(10 * time.Second)
	if line := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS"); !strings.Contains(line, "updated 10s ago") || strings.Contains(line, "stale") {
		t.Fatalf("manual mode should show the old sample without an automatic-stale marker: %q", line)
	}

	next, cmd := m.Update(press("a"))
	m = next.(Model)
	if cmd == nil || !m.autoStatsLoading["lb-1"] {
		t.Fatal("re-enabling auto-refresh should immediately request stale stats")
	}
	m = upd(t, m, statsMsg{
		lbID: "lb-1", automatic: true,
		stats: map[string]any{"active_connections": 2},
	})
	line := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS")
	if !strings.ContainsAny(line, "●∙") || strings.Contains(line, "stale") {
		t.Fatalf("successful immediate refresh should restore the cadence indicator: %q", line)
	}
}

func TestAutomaticFullRefreshAvoidsOverlapAndCompletesSilently(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m.autoStatsLoading["lb-1"] = true
	next, cmd := m.Update(autoFullTickMsg{generation: m.autoGeneration})
	m = next.(Model)
	if cmd == nil || m.refreshing {
		t.Fatal("full timer should reschedule without overlapping an in-flight stats request")
	}

	delete(m.autoStatsLoading, "lb-1")
	next, cmd = m.Update(autoFullTickMsg{generation: m.autoGeneration})
	m = next.(Model)
	if cmd == nil || !m.refreshing || !m.refreshAutomatic {
		t.Fatal("full timer did not start an automatic list refresh")
	}
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	if m.refreshing || m.refreshAutomatic {
		t.Fatal("automatic list refresh did not finish")
	}
	if m.flash == "refreshed" {
		t.Fatal("successful automatic refresh should not show the manual completion flash")
	}
}

func TestLBRelatedObjectsAreGroupedAndSorted(t *testing.T) {
	root := model.NewNode(model.TypeLoadBalancer, "lb-1", "lb")
	root.Children = []*model.Node{
		model.NewNode(model.TypePool, "pool-z", "Zulu"),
		model.NewNode(model.TypeAmphora, "amphora-b", ""),
		model.NewNode(model.TypeListener, "listener-b", "beta"),
		model.NewNode(model.TypePool, "pool-a", "alpha"),
		func() *model.Node {
			n := model.NewNode(model.TypeVIP, "vip-additional", "10.0.0.1")
			n.SetAttr("vip_kind", "additional")
			return n
		}(),
		func() *model.Node {
			n := model.NewNode(model.TypeVIP, "vip-primary", "10.0.0.2")
			n.SetAttr("vip_kind", "primary")
			return n
		}(),
		model.NewNode(model.TypeListener, "listener-a2", "Alpha"),
		model.NewNode(model.TypeAmphora, "amphora-a", ""),
		model.NewNode(model.TypeListener, "listener-a1", "Alpha"),
	}

	entries := nodeEntries(root)
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, string(entry.node.Type)+":"+entry.node.ID)
	}
	want := []string{
		"vip:vip-primary", "vip:vip-additional",
		"listener:listener-a1", "listener:listener-a2", "listener:listener-b",
		"pool:pool-a", "pool:pool-z",
		"amphora:amphora-a", "amphora:amphora-b",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("related object order = %v, want %v", got, want)
	}
}

func TestLBRelatedObjectHeadingsAreCountedFilteredAndSkipped(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))

	pools, err := m.backend.ListPoolSummaries(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, poolSummariesMsg{lbID: "lb-1", items: pools})
	amphorae, err := m.backend.ListAmphorae(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, amphoraeMsg{lbID: "lb-1", nodes: amphorae})

	var headings []string
	for _, e := range m.entries {
		if e.kind == entGroup {
			headings = append(headings, e.label)
		}
	}
	wantHeadings := []string{"VIPS 2", "LISTENERS 1", "POOLS 2", "AMPHORAE 2"}
	if strings.Join(headings, ",") != strings.Join(wantHeadings, ",") {
		t.Fatalf("related-object headings = %v, want %v", headings, wantHeadings)
	}
	if got := selectableEntryCount(m.entries); got != 7 {
		t.Fatalf("selectable related objects = %d, want 7", got)
	}
	for _, e := range m.entries {
		switch e.label {
		case "LISTENERS 1", "POOLS 2":
			if e.issueErrors != 0 || e.issueDegraded != 1 {
				t.Errorf("%s issue counts = ERROR %d, DEGRADED %d; want ERROR 0, DEGRADED 1", e.label, e.issueErrors, e.issueDegraded)
			}
		case "VIPS 2", "AMPHORAE 2":
			if e.issueErrors != 0 || e.issueDegraded != 0 {
				t.Errorf("%s should have no issue counts", e.label)
			}
		}
	}
	if m.entries[m.cursor].kind == entGroup {
		t.Fatal("initial cursor landed on a group heading")
	}

	first := m.cursor
	m = upd(t, m, press("up"))
	if m.cursor != first {
		t.Fatalf("up from first object moved cursor to %d, want %d", m.cursor, first)
	}
	m = upd(t, m, press("down"))
	if m.entries[m.cursor].node == nil || m.entries[m.cursor].node.Attrs["vip_kind"] != "additional" {
		t.Fatalf("first down should select the additional VIP, got %+v", m.entries[m.cursor])
	}
	m = upd(t, m, press("down"))
	if m.entries[m.cursor].node == nil || m.entries[m.cursor].node.Type != model.TypeListener {
		t.Fatalf("second down should skip the listener heading, got %+v", m.entries[m.cursor])
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.entries[m.cursor].kind == entGroup || m.entries[m.cursor].node.Type != model.TypeAmphora {
		t.Fatalf("end should select the final Amphora, got %+v", m.entries[m.cursor])
	}
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if m.cursor != first {
		t.Fatalf("home selected row %d, want first object at %d", m.cursor, first)
	}

	m.filter.SetValue("listener:http")
	m.cursor = 0
	m.applyFilters()
	if len(m.entries) != 2 || m.entries[0].kind != entGroup || m.entries[0].label != "LISTENERS 1" ||
		m.entries[1].node == nil || m.entries[1].node.Type != model.TypeListener {
		t.Fatalf("filtered related rows = %v, want listener heading and one listener", labels(m))
	}
	if m.cursor != 1 {
		t.Fatalf("filtered cursor = %d, want selectable row 1", m.cursor)
	}
	plain := ansiRE.ReplaceAllString(m.View(), "")
	if strings.Contains(plain, "VIPS ") || strings.Contains(plain, "POOLS ") || strings.Contains(plain, "AMPHORAE ") {
		t.Fatalf("empty filtered groups should be omitted:\n%s", plain)
	}
	if heading := lineContaining(plain, "LISTENERS 1"); !strings.HasPrefix(heading, "── ") || !strings.Contains(heading, "DEGRADED 1") {
		t.Fatalf("group heading should render as a panel-aligned divider: %q", heading)
	}
	if listener := navigationLineContaining(plain, "http"); !strings.HasPrefix(listener, "▶ ●") {
		t.Fatalf("selected related object should show cursor and status markers: %q", listener)
	}
	if line := lineContaining(plain, "RELATED OBJECTS"); !strings.Contains(line, "RELATED OBJECTS 1/7") {
		t.Fatalf("filtered related-object count should exclude headings: %q", line)
	} else if !strings.Contains(line, "DEGRADED 1") {
		t.Fatalf("filtered issue count should describe only visible related objects: %q", line)
	}
}

func TestRelatedIssueCountsUseHighestSeverityAndSkipHeadings(t *testing.T) {
	entries := []entry{
		{kind: entGroup, label: "POOLS 4"},
		{kind: entChild, oper: "ONLINE", prov: "ACTIVE"},
		{kind: entChild, oper: "DEGRADED", prov: "ACTIVE"},
		{kind: entChild, oper: "DEGRADED", prov: "ERROR"},
		{kind: entChild, oper: "ERROR", prov: "ERROR"},
	}
	errors, degraded := relatedIssueCounts(entries)
	if errors != 2 || degraded != 1 {
		t.Fatalf("related issue counts = ERROR %d, DEGRADED %d; want ERROR 2, DEGRADED 1", errors, degraded)
	}
}

func TestReferenceAndBackReferenceNavigation(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // into LB

	// Drill into the listener.
	i, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("no listener row")
	}
	m.cursor = i
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeListener {
		t.Fatalf("expected listener location, got %+v", m.loc.node)
	}

	// The listener presents its pool like an LB-related pool row even though the
	// underlying graph connection remains a reference edge.
	i, ok = m.selectLabel("pool:web")
	if !ok {
		t.Fatalf("listener should reference a pool; entries=%v", labels(m))
	}
	m.cursor = i
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypePool {
		t.Fatalf("expected pool location, got %+v", m.loc.node)
	}

	// Standing on the pool, a back-reference answers "who points at me?".
	var hasBackref bool
	for _, e := range m.entries {
		if e.kind == entBackRef {
			hasBackref = true
		}
	}
	if !hasBackref {
		t.Errorf("pool should show a back-reference; entries=%v", labels(m))
	}
}

func TestHistoryNavigationRestoresSelectedRelatedObject(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // LB overview

	listenerIndex, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("missing listener row")
	}
	m.cursor = listenerIndex
	listenerSelection := m.entries[m.cursor].selection()
	m = updExec(t, m, press("enter")) // listener overview

	poolIndex, ok := m.selectLabel("pool:web")
	if !ok {
		t.Fatal("missing listener pool row")
	}
	m.cursor = poolIndex
	poolSelection := m.entries[m.cursor].selection()
	m = updExec(t, m, press("enter")) // pool

	m = updExec(t, m, press("esc")) // listener
	if m.loc.node == nil || m.loc.node.Type != model.TypeListener ||
		!m.entries[m.cursor].selection().equal(poolSelection) {
		t.Fatalf("Back did not restore listener selection: loc=%+v cursor=%d entries=%v", m.loc.node, m.cursor, labels(m))
	}

	m = updExec(t, m, press("esc")) // LB
	if m.loc.node == nil || m.loc.node.Type != model.TypeLoadBalancer ||
		!m.entries[m.cursor].selection().equal(listenerSelection) {
		t.Fatalf("Back did not restore LB selection: loc=%+v cursor=%d entries=%v", m.loc.node, m.cursor, labels(m))
	}

	m = updExec(t, m, press("right")) // listener again
	if m.loc.node == nil || m.loc.node.Type != model.TypeListener ||
		!m.entries[m.cursor].selection().equal(poolSelection) {
		t.Fatalf("Forward did not restore listener selection: loc=%+v cursor=%d entries=%v", m.loc.node, m.cursor, labels(m))
	}
}

func TestListenerRefreshCompletesAfterNavigatingBackToLoadBalancer(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	listenerIndex, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("missing listener row")
	}
	m.cursor = listenerIndex
	m = updExec(t, m, press("enter"))
	listener := m.loc.node
	if listener == nil || listener.Type != model.TypeListener {
		t.Fatalf("listener location = %+v", listener)
	}
	m = updExec(t, m, press("esc")) // navigate away before responses arrive

	m.refreshing = true
	m.refreshLBID = listener.OwningLBID
	m.loading = true
	m.lbDetailLoading[listener.ID] = true
	m.lbStatsLoading[listener.ID] = true
	detail, err := m.backend.FetchDetail(context.Background(), listener)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := m.backend.ListenerStats(context.Background(), listener.OwningLBID, listener.ID)
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, detailMsg{
		nodeID: listener.ID, lbID: listener.OwningLBID, res: detail,
		intent: intentOverview, refresh: true,
	})
	if !m.refreshing {
		t.Fatal("refresh completed before listener stats arrived")
	}
	m = upd(t, m, listenerStatsMsg{
		lbID: listener.OwningLBID, listenerID: listener.ID,
		stats: stats, sampledAt: time.Now(), refresh: true,
	})
	if m.refreshing || m.loading || m.lbDetailLoading[listener.ID] || m.lbStatsLoading[listener.ID] {
		t.Fatalf("listener refresh remained stuck after navigation: refreshing=%v loading=%v detail=%v stats=%v",
			m.refreshing, m.loading, m.lbDetailLoading[listener.ID], m.lbStatsLoading[listener.ID])
	}
}

func TestBreadcrumbTruncationPreservesCurrentObject(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m.width = 38
	m.hist = newHistory(50)
	m.hist.root = model.LBListIdentity
	m.hist.navigate(histEntry{id: model.LBListIdentity})
	m.hist.navigate(histEntry{id: model.Identity{
		Type: model.TypeLoadBalancer, ID: "lb-1", OwningLBID: "lb-1",
		Label: "lb:a-very-long-load-balancer-name",
	}})
	m.hist.navigate(histEntry{id: model.Identity{
		Type: model.TypeListener, ID: "listener-1", OwningLBID: "lb-1",
		Label: "listener:https",
	}})

	line := ansiRE.ReplaceAllString(m.breadcrumbLine(), "")
	if !strings.HasPrefix(line, "… › ") || !strings.HasSuffix(line, "listener:https") {
		t.Fatalf("breadcrumb did not preserve current object: %q", line)
	}
	if lipgloss.Width(m.breadcrumbLine()) > m.width {
		t.Fatalf("breadcrumb width exceeds terminal: %q", line)
	}
}

func TestListenerOverviewShowsStatsCertificateAndRelatedObjects(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m.clock = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	m = updExec(t, m, press("enter"))
	i, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("no listener row")
	}
	m.cursor = i
	m = updExec(t, m, press("enter"))
	n := m.loc.node
	result, err := m.backend.FetchDetail(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, detailMsg{nodeID: n.ID, lbID: n.OwningLBID, res: result, intent: intentOverview})
	stats, err := m.backend.ListenerStats(context.Background(), n.OwningLBID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, listenerStatsMsg{lbID: n.OwningLBID, listenerID: n.ID, stats: stats, sampledAt: m.clock()})
	plain := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"DETAILS", "STATS", "HTTPS (TLS termination)", "Total connections", "80",
		"Certificate", "api.example.test", "Expires", "13d remaining",
		"RELATED OBJECTS", "LOAD BALANCER 1", "POOLS 1", "● Pool", "L7 POLICIES 1",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("listener overview missing %q:\n%s", want, plain)
		}
	}
}

func TestCertificateExpiryColors(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		until time.Duration
		color lipgloss.Color
	}{
		{"healthy", 31 * 24 * time.Hour, lipgloss.Color("42")},
		{"under a month", 29 * 24 * time.Hour, lipgloss.Color("226")},
		{"under two weeks", 13 * 24 * time.Hour, lipgloss.Color("214")},
		{"expired", -time.Second, lipgloss.Color("196")},
	} {
		t.Run(test.name, func(t *testing.T) {
			label, color := certificateExpiryDisplay(now.Add(test.until).Format(time.RFC3339), now)
			if color != test.color {
				t.Fatalf("color = %q, want %q", color, test.color)
			}
			if label == "" {
				t.Fatal("empty expiry label")
			}
		})
	}
}

func TestListenerCertificateErrorUsesConciseLabel(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	i, ok := m.selectLabel("listener:http")
	if !ok {
		t.Fatal("no listener row")
	}
	m.cursor = i
	m = updExec(t, m, press("enter"))
	m.loc.node.SetAttr("protocol", "TERMINATED_HTTPS")
	m.loc.node.SetAttr("certificate_error", "read certificate payload: sensitive backend response")

	plain := ansiRE.ReplaceAllString(m.View(), "")
	certificateLine := lineContaining(plain, "Certificate")
	if !strings.Contains(certificateLine, "— information unavailable —") {
		t.Fatalf("certificate error was not summarized: %q\n%s", certificateLine, plain)
	}
	if strings.Contains(plain, "sensitive backend response") {
		t.Fatalf("backend certificate error leaked into view:\n%s", plain)
	}
	fields := m.listenerCertificateFields()
	if len(fields) == 0 {
		t.Fatal("certificate fields are empty")
	}
	if fields[0].value != m.st.disabled.Render("— information unavailable —") {
		t.Fatalf("unavailable certificate style differs from disabled style: %q", fields[0].value)
	}
}

func TestInspectCopyAndOverlays(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // into LB (loc.node = LB)

	// The LB view embeds independently-loaded details and stats above the
	// selectable related-object list.
	view := m.View()
	for _, section := range []string{"DETAILS", "STATS", "RELATED OBJECTS"} {
		if !strings.Contains(view, section) {
			t.Errorf("LB overview missing %q:\n%s", section, view)
		}
	}
	if !m.lbDetailLoading["lb-1"] || !m.lbStatsLoading["lb-1"] {
		t.Fatal("opening an LB should start detail and stats requests")
	}
	m = upd(t, m, detailMsg{
		nodeID: "lb-1", lbID: "lb-1", intent: intentOverview,
		res: osclient.DetailResult{Raw: map[string]any{"id": "lb-1"}, Attrs: map[string]string{
			"provider": "amphora", "vip_address": "203.0.113.9", "admin_state_up": "true",
			"flavor_id":  "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			"created_at": "2026-07-18T10:15:30Z", "updated_at": "2026-07-19T11:20:45Z",
			"description": "production ingress",
		}},
	})
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})
	m = upd(t, m, lbFloatingIPMsg{lbID: "lb-1", nodes: map[string]*model.Node{
		"203.0.113.9": newFloatingIP("198.51.100.7"),
		"203.0.114.9": newFloatingIP("198.51.100.17"),
	}})
	view = m.View()
	for _, value := range []string{"Admin state", "ENABLED", "203.0.113.9 (198.51.100.7)", "Active connections", "1"} {
		if !strings.Contains(view, value) {
			t.Errorf("loaded LB overview missing %q:\n%s", value, view)
		}
	}
	plainView := ansiRE.ReplaceAllString(view, "")
	vipRow := navigationLineContaining(plainView, "203.0.113.9")
	if !strings.Contains(vipRow, "203.0.113.9 (198.51.100.7)") || strings.Count(vipRow, "203.0.113.9") != 1 {
		t.Fatalf("related VIP row should use fixed-and-floating form without repetition: %q", vipRow)
	}
	additionalVIPRow := navigationLineContaining(plainView, "203.0.114.9")
	if !strings.Contains(additionalVIPRow, "Additional VIP") || !strings.Contains(additionalVIPRow, "203.0.114.9 (198.51.100.17)") {
		t.Fatalf("additional VIP row should identify the relation and its own floating IP: %q", additionalVIPRow)
	}
	fields := m.lbDetailFields()
	values := map[string]string{}
	for _, field := range fields {
		values[field.label] = field.value
	}
	for label, want := range map[string]string{
		"Description": "production ingress",
		"Flavor ID":   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"Created":     "2026-07-18 10:15:30 UTC",
		"Updated":     "2026-07-19 11:20:45 UTC",
	} {
		if got := values[label]; got != want {
			t.Errorf("LB detail %s = %q, want %q", label, got, want)
		}
	}
	var admin overviewField
	for _, field := range fields {
		if field.label == "Admin state" {
			admin = field
			break
		}
	}
	if admin.label != "Admin state" || admin.value != "ENABLED" || !admin.status {
		t.Fatalf("admin state should use status formatting, got %+v", admin)
	}
	delete(m.loc.node.Attrs, "description")
	for _, field := range m.lbDetailFields() {
		if field.label == "Description" {
			t.Fatal("empty descriptions should be omitted from LB details")
		}
	}
	if got := string(statusColor("ENABLED")); got != "42" {
		t.Fatalf("ENABLED color = %q, want green (42)", got)
	}
	if got := string(statusColor("DISABLED")); got != "244" {
		t.Fatalf("DISABLED color = %q, want grey (244)", got)
	}
	if m.overlay != overlayNone {
		t.Fatalf("inline overview should not open an overlay, got %v", m.overlay)
	}
	m = upd(t, m, press("d"))
	if m.overlay != overlayNone {
		t.Fatalf("d should no longer open a detail overlay, got %v", m.overlay)
	}
	if m.showIDs {
		t.Fatal("d should not toggle name/ID mode in a detail view")
	}
	if strings.Contains(m.hintLine(), "d names/ids") || strings.Contains(helpContent(false), "toggle top-level tables") {
		t.Fatal("detail views should not advertise the name/ID toggle")
	}

	// y -> raw YAML overlay; then o copies (print mode shows it).
	m = updExec(t, m, press("y"))
	if m.overlay != overlayRaw || m.rawFormat != "yaml" {
		t.Fatalf("y should show raw YAML, overlay=%v fmt=%q", m.overlay, m.rawFormat)
	}
	m = upd(t, m, press("o")) // copy displayed raw (print mode)
	m = upd(t, m, press("esc"))

	// j -> raw JSON.
	m = updExec(t, m, press("j"))
	if m.rawFormat != "json" {
		t.Fatalf("j should show raw JSON, got %q", m.rawFormat)
	}
	m = upd(t, m, press("esc"))

	// i / n copy the standing object's id / name (print mode, no stdout write).
	m = upd(t, m, press("i"))
	m = upd(t, m, press("n"))
}

func TestDisplayTimestampIsHumanReadableUTC(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "UTC", value: "2026-07-19T11:20:45Z", want: "2026-07-19 11:20:45 UTC"},
		{name: "offset", value: "2026-07-19T13:20:45+02:00", want: "2026-07-19 11:20:45 UTC"},
		{name: "empty", want: "—"},
		{name: "unknown format", value: "recently", want: "recently"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayTimestamp(tc.value); got != tc.want {
				t.Fatalf("displayTimestamp(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestHelpIncludesStatusColoredLegend(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = upd(t, m, press("?"))
	if m.overlay != overlayHelp {
		t.Fatalf("? should open help, overlay=%v", m.overlay)
	}
	if view := m.View(); view == "" {
		t.Fatal("help overlay rendered an empty view")
	}
	content := helpContent(true)
	plain := ansiRE.ReplaceAllString(content, "")
	for _, want := range []string{
		"Status colors",
		"● healthy / ready",
		"ONLINE · ACTIVE · ENABLED · ALLOCATED · READY",
		"● degraded / changing",
		"DEGRADED · DRAINING · BOOTING · PENDING_*",
		"● error",
		"ERROR · FAILOVER_STOPPED",
		"● inactive / unmonitored",
		"OFFLINE · NO_MONITOR · DISABLED · DELETED",
		"● no health status",
		"VIP / not applicable",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("help status legend missing %q:\n%s", want, plain)
		}
	}
	if got := listenerProtocolLabel("TERMINATED_HTTPS"); got != "HTTPS (TLS termination)" {
		t.Errorf("listenerProtocolLabel(TERMINATED_HTTPS) = %q", got)
	}
	for _, item := range statusLegendEntries {
		styledDot := lipgloss.NewStyle().Foreground(statusColor(item.status)).Render("●")
		if !strings.Contains(content, styledDot) {
			t.Errorf("help legend missing status-colored dot for %q", item.description)
		}
	}
}

func TestTelemetryOverlayRefreshControlsAndReset(t *testing.T) {
	collector := telemetry.NewCollector(time.Second)
	collector.Observe("GET octavia /v2/lbaas/loadbalancers", 100*time.Millisecond, telemetry.Success)
	collector.Observe("GET octavia /v2/lbaas/loadbalancers", 2*time.Second, telemetry.Success)
	collector.Observe("GET neutron /v2.0/floatingips?port_id", 30*time.Second, telemetry.Timeout)
	backend := &fakeBackend{
		cap: osclient.SwitchCapability{CanSwitch: true}, telemetry: collector,
	}
	m := New(backend, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})

	next, cmd := m.Update(press("t"))
	m = next.(Model)
	if m.overlay != overlayTelemetry || cmd == nil {
		t.Fatalf("t should open auto-refreshing telemetry overlay; overlay=%v cmd=%v", m.overlay, cmd)
	}
	plain := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"API telemetry", "refresh: auto (5s)", "slow ≥1s",
		"TOTAL 3", "SUCCESS 2", "SLOW 2", "TIMEOUT 1", "ERROR 0",
		"GET octavia /v2/lbaas/loadbalancers",
		"calls 2 · success 2 · slow 1 · timeout 0 · error 0",
		"latency min 100ms · avg 1.1s · median 1.1s · p95 2s · p99 2s · max 2s",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("telemetry overlay missing %q:\n%s", want, plain)
		}
	}
	content := m.telemetryContent(true)
	for description, want := range map[string]string{
		"slow endpoint heading": m.st.groupHeading.Foreground(statusColor("DEGRADED")).Render("GET octavia /v2/lbaas/loadbalancers"),
		"slow endpoint count":   telemetryMetric("slow", 1, statusColor("DEGRADED")),
		"timeout heading":       m.st.groupHeading.Foreground(telemetryTimeoutColor()).Render("GET neutron /v2.0/floatingips?port_id"),
		"timeout count":         telemetryMetric("timeout", 1, telemetryTimeoutColor()),
	} {
		if !strings.Contains(content, want) {
			t.Errorf("telemetry overlay missing colored %s", description)
		}
	}
	if got := string(telemetryTimeoutColor()); got != "135" {
		t.Fatalf("telemetry timeout color = %q, want violet (135)", got)
	}

	// Manual mode freezes the displayed snapshot until r is pressed.
	m = upd(t, m, press("a"))
	if m.telemetryAutoEnabled || !strings.Contains(ansiRE.ReplaceAllString(m.View(), ""), "refresh: manual") {
		t.Fatal("a should switch telemetry display refresh to manual")
	}
	if m.autoRefreshPaused() {
		t.Fatal("telemetry overlay should not pause normal API auto-refresh")
	}
	collector.Observe("GET nova /v2.1/:id/servers", 50*time.Millisecond, telemetry.Failure)
	if m.telemetrySnapshot.Calls != 3 {
		t.Fatal("manual telemetry snapshot changed without an explicit refresh")
	}
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	if m.telemetrySnapshot.Calls != 3 {
		t.Fatal("resizing should not refresh a manual telemetry snapshot")
	}
	m = upd(t, m, press("r"))
	if m.telemetrySnapshot.Calls != 4 || m.telemetrySnapshot.Errors != 1 {
		t.Fatalf("manual telemetry refresh = %+v", m.telemetrySnapshot)
	}
	content = m.telemetryContent(true)
	for description, want := range map[string]string{
		"error endpoint heading": m.st.groupHeading.Foreground(statusColor("ERROR")).Render("GET nova /v2.1/:id/servers"),
		"error endpoint count":   telemetryMetric("error", 1, statusColor("ERROR")),
	} {
		if !strings.Contains(content, want) {
			t.Errorf("telemetry overlay missing colored %s", description)
		}
	}

	// '=' uses the same interval-increase action as '+', even in manual mode.
	m = upd(t, m, press("="))
	if m.telemetryInterval() != 10*time.Second {
		t.Fatalf("telemetry interval = %s, want 10s", m.telemetryInterval())
	}
	m = upd(t, m, press("z"))
	if m.telemetrySnapshot.Calls != 0 || collector.Snapshot().Calls != 0 {
		t.Fatalf("z did not reset telemetry: view=%+v collector=%+v", m.telemetrySnapshot, collector.Snapshot())
	}

	// Re-enable auto mode and verify the generation-owned timer snapshots new
	// collector data; closing the overlay invalidates that timer.
	next, cmd = m.Update(press("a"))
	m = next.(Model)
	if !m.telemetryAutoEnabled || cmd == nil {
		t.Fatal("a should re-enable and schedule telemetry auto-refresh")
	}
	generation := m.telemetryGeneration
	collector.Observe("GET keystone /v3/projects", 20*time.Millisecond, telemetry.Success)
	next, cmd = m.Update(telemetryTickMsg{generation: generation})
	m = next.(Model)
	if m.telemetrySnapshot.Calls != 1 || cmd == nil {
		t.Fatal("telemetry timer did not refresh and reschedule")
	}
	m = upd(t, m, press("t"))
	if m.overlay != overlayNone {
		t.Fatal("t should close the telemetry overlay")
	}
	next, cmd = m.Update(telemetryTickMsg{generation: generation})
	if cmd != nil || next.(Model).overlay != overlayNone {
		t.Fatal("stale telemetry timer remained active after closing overlay")
	}
}

func TestRefreshKeepsAndAtomicallyReplacesLBOverview(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	m = upd(t, m, detailMsg{
		nodeID: "lb-1", lbID: "lb-1", intent: intentOverview,
		res: osclient.DetailResult{Attrs: map[string]string{"admin_state_up": "true"}},
	})
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})
	m = upd(t, m, lbFloatingIPMsg{lbID: "lb-1", nodes: map[string]*model.Node{
		"203.0.113.9": newFloatingIP("198.51.100.7"),
		"203.0.114.9": newFloatingIP("198.51.100.17"),
	}})
	selected, ok := m.selectLabel("203.0.114.9")
	if !ok {
		t.Fatal("test LB has no additional VIP row")
	}
	m.cursor = selected

	next, cmd := m.Update(press("r"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("refresh should request a new status graph")
	}
	view := m.View()
	for _, want := range []string{"refreshing…", "ENABLED", "203.0.113.9 (198.51.100.7)", "Active connections", "1"} {
		if !strings.Contains(view, want) {
			t.Errorf("refresh should retain %q while loading:\n%s", want, view)
		}
	}
	if strings.Contains(view, "loading…") {
		t.Errorf("full refresh should not repeat its state in the panel headings:\n%s", view)
	}
	if strings.Contains(view, " tree") {
		t.Errorf("refresh status should not expose the internal tree request:\n%s", view)
	}
	if m.flash == "refreshed" {
		t.Fatal("refresh must not report completion before the responses arrive")
	}

	// The graph response starts forced detail and stats requests, but the old
	// overview stays visible until both of those responses are ready.
	fresh := newTree()
	for left, right := 0, len(fresh.Root.Children)-1; left < right; left, right = left+1, right-1 {
		fresh.Root.Children[left], fresh.Root.Children[right] = fresh.Root.Children[right], fresh.Root.Children[left]
	}
	next, cmd = m.Update(treeMsg{lbID: "lb-1", tree: fresh})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("graph refresh should start detail and stats requests")
	}
	selectedID, _, _ := m.entries[m.cursor].identity()
	if selectedID.ID != "lb-1/additional-vip/subnet-10" {
		t.Fatalf("refresh selected %q, want retained additional VIP", selectedID.ID)
	}
	m = upd(t, m, detailMsg{
		nodeID: "lb-1", lbID: "lb-1", intent: intentOverview, refresh: true,
		res: osclient.DetailResult{Attrs: map[string]string{"admin_state_up": "false"}},
	})
	view = m.View()
	for _, want := range []string{"refreshing…", "ENABLED", "203.0.113.9 (198.51.100.7)", "Active connections", "1"} {
		if !strings.Contains(view, want) {
			t.Errorf("a partial refresh should retain %q:\n%s", want, view)
		}
	}
	partialVIPRow := navigationLineContaining(ansiRE.ReplaceAllString(view, ""), "203.0.113.9")
	if !strings.Contains(partialVIPRow, "203.0.113.9 (198.51.100.7)") {
		t.Fatalf("related VIP row lost its floating IP during refresh: %q", partialVIPRow)
	}
	partialAdditionalVIPRow := navigationLineContaining(ansiRE.ReplaceAllString(view, ""), "203.0.114.9")
	if !strings.Contains(partialAdditionalVIPRow, "203.0.114.9 (198.51.100.17)") {
		t.Fatalf("additional VIP row lost its floating IP during refresh: %q", partialAdditionalVIPRow)
	}

	m = upd(t, m, statsMsg{
		lbID: "lb-1", refresh: true,
		stats: map[string]any{"active_connections": 9},
	})
	if !m.refreshing {
		t.Fatal("refresh should wait for the floating-IP lookup")
	}
	m = upd(t, m, lbFloatingIPMsg{
		lbID: "lb-1", refresh: true,
		nodes: map[string]*model.Node{
			"203.0.113.9": newFloatingIP("198.51.100.8"),
			"203.0.114.9": newFloatingIP("198.51.100.18"),
		},
	})
	if !m.refreshing {
		t.Fatal("refresh should wait for the amphora VM listing")
	}
	amphorae, err := m.backend.ListAmphorae(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, amphoraeMsg{lbID: "lb-1", nodes: amphorae, refresh: true})
	if !m.refreshing {
		t.Fatal("refresh should wait for listener endpoint details")
	}
	listeners, err := m.backend.ListListenerSummaries(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, listenerSummariesMsg{lbID: "lb-1", items: listeners, refresh: true})
	if !m.refreshing {
		t.Fatal("refresh should wait for pool summaries")
	}
	pools, err := m.backend.ListPoolSummaries(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, poolSummariesMsg{lbID: "lb-1", items: pools, refresh: true})
	view = m.View()
	for _, want := range []string{"DISABLED", "203.0.113.9 (198.51.100.8)", "203.0.114.9 (198.51.100.18)", "Active connections", "9"} {
		if !strings.Contains(view, want) {
			t.Errorf("completed refresh missing %q:\n%s", want, view)
		}
	}
	if m.refreshing || m.loading {
		t.Fatal("refresh should finish only after detail and stats are committed")
	}
	if m.flash != "refreshed" {
		t.Fatalf("completion flash = %q, want refreshed", m.flash)
	}
}

func TestLBOverviewResponsiveLayout(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))

	wide := ansiRE.ReplaceAllString(m.View(), "")
	wideLines := strings.Split(wide, "\n")
	if wideLines[2] != "" {
		t.Fatalf("LB overview should have a blank line below project status: %q", wideLines[2])
	}
	if strings.Contains(wideLines[1], "items") {
		t.Fatalf("LB overview subtitle should not repeat the related-object count: %q", wideLines[1])
	}
	if !strings.Contains(m.View(), m.st.title.Render(projectLabel(m.project))) {
		t.Fatalf("project name should use emphasized styling: %q", wideLines[1])
	}
	if !strings.Contains(wideLines[1], "scope: project alpha") {
		t.Fatalf("single-project subtitle should use consistent scope wording: %q", wideLines[1])
	}
	lastField := -1
	for _, field := range []string{"Name", "ID", "Project name", "Project ID", "Primary VIP", "Provider", "Operating"} {
		at := strings.Index(wide, field)
		if at <= lastField {
			t.Fatalf("detail field %q is missing or out of order:\n%s", field, wide)
		}
		lastField = at
	}
	for _, owner := range []string{"Project name  alpha", "Project ID    p1"} {
		if !strings.Contains(wide, owner) {
			t.Fatalf("LB details should identify owner %q:\n%s", owner, wide)
		}
	}
	wideHeading := lineContaining(wide, "DETAILS")
	if !strings.Contains(wideHeading, "STATS") {
		t.Fatalf("wide overview should place details and stats side-by-side: %q", wideHeading)
	}
	wideRelatedAt := -1
	for i, line := range wideLines {
		if strings.Contains(line, "RELATED OBJECTS") {
			wideRelatedAt = i
			break
		}
	}
	if wideRelatedAt <= 0 || wideLines[wideRelatedAt-1] != "" {
		t.Fatalf("related objects should have permanent leading space:\n%s", wide)
	}

	m = upd(t, m, tea.WindowSizeMsg{Width: 60, Height: 18})
	narrow := ansiRE.ReplaceAllString(m.View(), "")
	lines := strings.Split(narrow, "\n")
	detailsAt, statsAt, relatedAt := -1, -1, -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, "DETAILS"):
			detailsAt = i
		case strings.Contains(line, "STATS"):
			statsAt = i
		case strings.Contains(line, "RELATED OBJECTS"):
			relatedAt = i
		}
	}
	if detailsAt < 0 || statsAt <= detailsAt || relatedAt <= statsAt {
		t.Fatalf("narrow overview should stack details, stats, then related objects; indexes %d/%d/%d:\n%s", detailsAt, statsAt, relatedAt, narrow)
	}
	if lines[detailsAt-1] != "" || lines[statsAt-1] != "" || lines[relatedAt-1] != "" {
		t.Fatalf("stacked overview sections should retain blank separators:\n%s", narrow)
	}
	if got := len(lines); got != m.height {
		t.Fatalf("responsive overview rendered %d lines, want terminal height %d", got, m.height)
	}
	if navigationLineContaining(narrow, "http") == "" {
		t.Fatalf("narrow overview should retain selectable related-object rows:\n%s", narrow)
	}
}

func TestLBOverviewPanelsFailIndependently(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	m = upd(t, m, detailMsg{nodeID: "lb-1", lbID: "lb-1", intent: intentOverview, err: errors.New("detail denied")})
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 7}})

	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(lineContaining(view, "DETAILS"), "unavailable") {
		t.Fatalf("detail failure should be isolated in its panel:\n%s", view)
	}
	if !strings.Contains(view, "Active connections") || !strings.Contains(view, "7") {
		t.Fatalf("stats should still render after detail failure:\n%s", view)
	}
	if navigationLineContaining(view, "http") == "" {
		t.Fatalf("related objects should remain navigable after detail failure:\n%s", view)
	}
}

func TestLBOverviewFreshnessAgesIndependently(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	m = updExec(t, m, press("enter"))
	m = upd(t, m, detailMsg{
		nodeID: "lb-1", lbID: "lb-1", intent: intentOverview,
		res: osclient.DetailResult{Attrs: map[string]string{"admin_state_up": "true"}},
	})
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})

	now = now.Add(2 * time.Second)
	m = upd(t, m, statsMsg{
		lbID: "lb-1", automatic: true,
		stats: map[string]any{"active_connections": 2},
	})
	freshStatsLine := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS")
	if !strings.ContainsAny(freshStatsLine, "●∙") || strings.Contains(freshStatsLine, "STATS · updated") {
		t.Fatalf("fresh automatic stats should show the Points cadence indicator: %q", freshStatsLine)
	}
	beforeFrame := m.statsSpinner.View()
	next, animationCmd := m.Update(m.statsSpinner.Tick())
	m = next.(Model)
	if animationCmd == nil || m.statsSpinner.View() == beforeFrame {
		t.Fatal("Points cadence indicator did not advance or reschedule")
	}

	now = now.Add(4 * time.Second)
	if line := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS"); !strings.ContainsAny(line, "●∙") {
		t.Fatalf("stats should retain the cadence indicator inside the five-second interval: %q", line)
	}
	now = now.Add(time.Second)
	if line := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS"); !strings.ContainsAny(line, "●∙") || strings.Contains(line, "stale") {
		t.Fatalf("stats should retain the cadence indicator during the grace window: %q", line)
	}
	now = now.Add(time.Second)
	if line := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS"); !strings.Contains(line, "updated 6s ago") || !strings.Contains(line, "stale") {
		t.Fatalf("stats should switch to stale timing after the grace window: %q", line)
	}
	now = now.Add(4 * time.Second)
	m = upd(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})

	view := ansiRE.ReplaceAllString(m.View(), "")
	for title, want := range map[string]string{
		"DETAILS":         "updated 12s ago",
		"STATS":           "updated 10s ago",
		"RELATED OBJECTS": "updated 12s ago",
	} {
		if line := lineContaining(view, title); !strings.Contains(line, want) {
			t.Errorf("%s freshness line = %q, want %q", title, line, want)
		}
	}
	if line := lineContaining(view, "STATS"); !strings.Contains(line, "stale") {
		t.Errorf("overdue automatic stats should be marked stale: %q", line)
	}

	// The local freshness tick only redraws elapsed labels; it does not start a
	// refresh or mutate any completion timestamp.
	now = now.Add(time.Second)
	next, cmd := m.Update(freshnessTickMsg{})
	m = next.(Model)
	if cmd == nil || m.loading || m.refreshing {
		t.Fatal("freshness tick should reschedule only its local redraw timer")
	}
	view = ansiRE.ReplaceAllString(m.View(), "")
	for title, want := range map[string]string{
		"DETAILS":         "updated 13s ago",
		"STATS":           "updated 11s ago",
		"RELATED OBJECTS": "updated 13s ago",
	} {
		if line := lineContaining(view, title); !strings.Contains(line, want) {
			t.Errorf("aged %s freshness line = %q, want %q", title, line, want)
		}
	}

	if listener, ok := m.selectLabel("listener:http"); ok {
		m.cursor = listener
	} else {
		t.Fatal("test LB has no listener row")
	}
	m = updExec(t, m, press("enter"))
	now = now.Add(2 * time.Second)
	m = updExec(t, m, press("esc"))
	view = ansiRE.ReplaceAllString(m.View(), "")
	if line := lineContaining(view, "DETAILS"); !strings.Contains(line, "updated 15s ago") {
		t.Errorf("cached navigation reset details freshness: %q", line)
	}
	if line := lineContaining(view, "STATS"); !strings.Contains(line, "updated 13s ago") {
		t.Errorf("cached navigation reset stats freshness: %q", line)
	}
	m.autoRefreshEnabled = false
	manualStatsLine := lineContaining(ansiRE.ReplaceAllString(m.View(), ""), "STATS")
	if !strings.Contains(manualStatsLine, "updated 13s ago") || strings.Contains(manualStatsLine, "stale") {
		t.Errorf("manual stats should show age without an overdue marker: %q", manualStatsLine)
	}
}

func TestFailedRefreshRetainsTimestampsAndMarksSectionsStale(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	m = updExec(t, m, press("enter"))
	m = upd(t, m, detailMsg{
		nodeID: "lb-1", lbID: "lb-1", intent: intentOverview,
		res: osclient.DetailResult{Attrs: map[string]string{"admin_state_up": "true"}},
	})
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})

	now = now.Add(12 * time.Second)
	m.refreshing = true
	m.refreshLBID = "lb-1"
	m.refreshPoolsExpected = true
	m = upd(t, m, detailMsg{
		nodeID: "lb-1", lbID: "lb-1", intent: intentOverview, refresh: true,
		err: errors.New("detail refresh failed"),
	})
	m = upd(t, m, statsMsg{
		lbID: "lb-1", refresh: true,
		stats: map[string]any{"active_connections": 9},
	})
	m = upd(t, m, poolSummariesMsg{
		lbID: "lb-1", refresh: true, err: errors.New("pool refresh failed"),
	})
	m = upd(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})

	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, title := range []string{"DETAILS", "RELATED OBJECTS"} {
		line := lineContaining(view, title)
		if !strings.Contains(line, "updated 12s ago") || !strings.Contains(line, "stale") || strings.Contains(line, "unavailable") {
			t.Errorf("failed %s refresh should retain age and mark stale: %q", title, line)
		}
	}
	statsLine := lineContaining(view, "STATS")
	if !strings.ContainsAny(statsLine, "●∙") || strings.Contains(statsLine, "updated") || strings.Contains(statsLine, "stale") {
		t.Errorf("successful stats refresh should have independent freshness: %q", statsLine)
	}
	if !strings.Contains(view, "Active connections") || !strings.Contains(view, "9") {
		t.Fatalf("successful stats value was not committed with partial refresh:\n%s", view)
	}
}

func TestHistoryBackForwardAndTruncation(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // LB
	if i, ok := m.selectLabel("listener:http"); ok {
		m.cursor = i
	}
	m = updExec(t, m, press("enter")) // listener
	// Back to LB, then forward to listener again.
	beforeLen := len(m.hist.entries)
	m = updExec(t, m, press("esc")) // back
	if m.loc.node == nil || m.loc.node.Type != model.TypeLoadBalancer {
		t.Fatalf("back should return to the LB, got %+v", m.loc.node)
	}
	m = updExec(t, m, press("right")) // forward
	if m.loc.node == nil || m.loc.node.Type != model.TypeListener {
		t.Fatalf("forward should return to the listener, got %+v", m.loc.node)
	}
	if len(m.hist.entries) != beforeLen {
		t.Errorf("back/forward must not change history length: %d != %d", len(m.hist.entries), beforeLen)
	}

	// Ctrl+Home is history navigation: it jumps to the pinned root and preserves
	// the forward path. A subsequent new navigation truncates that path.
	m = updExec(t, m, press("esc")) // back to LB (cursor not at tip)
	beforeLen = len(m.hist.entries)
	m = updExec(t, m, tea.KeyMsg{Type: tea.KeyCtrlHome})
	if len(m.hist.entries) != beforeLen || m.hist.cursor != 0 || !m.hist.canForward() {
		t.Errorf("ctrl+home changed history instead of moving to its root: %+v", m.hist)
	}
	m = updExec(t, m, press("enter"))
	if m.hist.canForward() {
		t.Errorf("new navigation after ctrl+home should truncate forward history")
	}
}

func TestFilterAndStatus(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	// Substring filter on the LB list.
	m = upd(t, m, press("/"))
	if !m.filtering {
		t.Fatal("/ should focus the filter")
	}
	m = upd(t, m, press("lb2"))
	if len(m.entries) != 1 || m.entries[0].lb.ID != "lb-2" {
		t.Errorf("filter lb2 should leave one match; got %v", labels(m))
	}
	m = upd(t, m, press("esc")) // esc clears the filter
	if m.filtering || m.filter.Value() != "" {
		t.Errorf("esc should clear the filter")
	}
	if len(m.entries) != 2 {
		t.Errorf("clearing the filter should restore all LBs")
	}
	// Status filter cycles all -> error.
	m = upd(t, m, press("s"))
	if m.status != statusError {
		t.Fatalf("s should cycle to error, got %v", m.status)
	}
	if len(m.entries) != 1 || m.entries[0].lb.ID != "lb-2" {
		t.Errorf("error filter should show only the ERROR LB; got %v", labels(m))
	}
}

func TestProjectSwitcherDisabled(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: false, Reason: "bare token", Suggest: "use creds"})
	m = upd(t, m, press("p"))
	if m.overlay != overlayProject {
		t.Fatal("p should open the project overlay")
	}
	if !strings.Contains(m.View(), "bare token") {
		t.Errorf("disabled switcher should show the reason; view=%q", m.View())
	}
	m = upd(t, m, press("esc"))
}

func TestProjectSwitcherEnabled(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("p")) // loads projects
	if m.overlay != overlayProject || len(m.projects) != 2 {
		t.Fatalf("p should load projects, got overlay=%v projects=%d", m.overlay, len(m.projects))
	}
	m = upd(t, m, press("down"))
	m = upd(t, m, press("esc"))
}

func TestProjectSwitcherHidesAllProjectsWithoutGlobalAdmin(t *testing.T) {
	capability := osclient.SwitchCapability{
		CanSwitch: true, AllProjectsChecked: true,
		AllProjectsReason: "start olb with --global-admin",
	}
	m := start(t, capability)
	m = updExec(t, m, press("p"))
	if m.projCursor != 0 {
		t.Fatalf("project cursor = %d, want first accessible project", m.projCursor)
	}
	plain := ansiRE.ReplaceAllString(m.View(), "")
	if strings.Contains(plain, "⟨ all projects ⟩") {
		t.Fatalf("all-projects row should be hidden without --global-admin:\n%s", plain)
	}
	if !strings.Contains(plain, "global view: restart with --global-admin") {
		t.Fatalf("global-admin hint missing:\n%s", plain)
	}

	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if cmd == nil || m.overlay != overlayNone || m.allProjects {
		t.Fatal("first visible project was not selectable at row zero")
	}
}

func TestFollowUnresolvedInstanceEdge(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // LB
	if i, ok := m.selectLabel("pool:web"); ok {
		m.cursor = i
	}
	m = updExec(t, m, press("enter")) // pool
	if i, ok := m.selectLabel("10.0.0.5"); ok {
		m.cursor = i
	} else {
		t.Fatalf("pool should list a member; entries=%v", labels(m))
	}
	m = updExec(t, m, press("enter")) // member
	if m.loc.node == nil || m.loc.node.Type != model.TypeMember {
		t.Fatalf("expected member location, got %+v", m.loc.node)
	}
	// Follow the unresolved instance edge -> resolves to a Nova server.
	if i, ok := m.selectLabel("instance"); ok {
		m.cursor = i
	} else {
		t.Fatalf("member should offer an instance edge; entries=%v", labels(m))
	}
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeInstance {
		t.Fatalf("following the instance edge should land on the Nova server, got %+v", m.loc.node)
	}
}

func TestLBOverviewListsAmphoraVMsDirectly(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // LB
	if !m.lbAmphoraLoading["lb-1"] {
		t.Fatal("amphora-provider overview should start a background VM listing")
	}
	nodes, err := m.backend.ListAmphorae(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, amphoraeMsg{lbID: "lb-1", nodes: nodes})
	m = upd(t, m, tea.WindowSizeMsg{Width: 140, Height: 30})
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, row := range []struct {
		target  string
		summary string
	}{
		{target: "11111111-1111-1111-1111-111111111111 (MASTER)", summary: "mgmt 10.0.3.20 · vm aaaaaaaa"},
		{target: "22222222-2222-2222-2222-222222222222 (BACKUP)", summary: "mgmt 10.0.3.21 · vm bbbbbbbb"},
	} {
		line := navigationLineContaining(view, row.target)
		if line == "" || !strings.Contains(line, row.summary) {
			t.Fatalf("LB overview missing Amphora target %q with summary %q:\n%s", row.target, row.summary, view)
		}
	}
	if strings.Contains(view, "Amphorae") || strings.Contains(view, "instances") {
		t.Fatalf("LB overview should not retain an amphora aggregate row:\n%s", view)
	}
	if strings.Contains(view, "[ALLOCATED]") {
		t.Fatalf("normal Amphora allocation state should not be called out:\n%s", view)
	}
	if got := string(statusColor("ALLOCATED")); got != "42" {
		t.Fatalf("ALLOCATED color = %q, want green (42)", got)
	}
	partial := model.NewNode(model.TypeAmphora, "amphora-partial", "")
	partial.SetAttr("compute_id", "cccccccc-cccc-cccc-cccc-cccccccccccc")
	if got := amphoraSummary(partial); got != "vm cccccccc" {
		t.Fatalf("Amphora summary should omit a missing management IP, got %q", got)
	}
	partial.Attrs = map[string]string{"lb_network_ip": "10.0.3.22"}
	if got := amphoraSummary(partial); got != "mgmt 10.0.3.22" {
		t.Fatalf("Amphora summary should omit a missing compute ID, got %q", got)
	}
	i, ok := m.selectLabel("11111111-1111-1111-1111-111111111111")
	if !ok {
		t.Fatal("direct amphora row is not selectable")
	}
	m.cursor = i
	m = updExec(t, m, press("enter"))
	if m.loc.node == nil || m.loc.node.Type != model.TypeAmphora || m.loc.node.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("expected to land directly on the amphora VM, got %+v", m.loc.node)
	}
}

func TestListenerRowsShowNormalizedProtocolEndpoint(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	items, err := m.backend.ListListenerSummaries(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, listenerSummariesMsg{lbID: "lb-1", items: items})
	view := ansiRE.ReplaceAllString(m.View(), "")
	line := navigationLineContaining(view, "HTTPS/8443 (TLS termination) · 1 pool")
	if line == "" || !strings.Contains(line, "http") {
		t.Fatalf("listener row should combine name, normalized endpoint, and pool count:\n%s", view)
	}

	for _, tc := range []struct {
		protocol string
		port     string
		want     string
	}{
		{protocol: "TCP", port: "443", want: "TCP/443"},
		{protocol: "HTTP", port: "80", want: "HTTP/80"},
		{protocol: "TERMINATED_HTTPS", port: "8443", want: "HTTPS/8443 (TLS termination)"},
	} {
		if got := listenerEndpoint(tc.protocol, tc.port); got != tc.want {
			t.Errorf("listenerEndpoint(%q, %q) = %q, want %q", tc.protocol, tc.port, got, tc.want)
		}
	}

	listener := m.loc.tree.Node("lsn-1")
	listener.AddRef("alternate pool", model.NewNode(model.TypePool, "pool-2", "alternate"))
	if got := listenerSummary(listener); got != "HTTPS/8443 (TLS termination) · 2 pools" {
		t.Fatalf("listenerSummary with two associated pools = %q", got)
	}
}

func TestPoolRowsShowProtocolAlgorithmMemberAndListenerCounts(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter"))
	items, err := m.backend.ListPoolSummaries(context.Background(), "lb-1")
	if err != nil {
		t.Fatal(err)
	}
	m = upd(t, m, poolSummariesMsg{lbID: "lb-1", items: items})
	view := ansiRE.ReplaceAllString(m.View(), "")
	line := navigationLineContaining(view, "HTTP · round robin · 1 member · 1 listener")
	if line == "" || !strings.Contains(line, "web (pool-1)") {
		t.Fatalf("pool row should combine name and diagnostic summary:\n%s", view)
	}
	duplicate := navigationLineContaining(view, "TCP · least connections · 4 members · 0 listeners")
	if duplicate == "" || !strings.Contains(duplicate, "web (pool-2)") {
		t.Fatalf("duplicate pool names should include a short ID:\n%s", view)
	}

	if got := poolSummary("TCP", "LEAST_CONNECTIONS", "4", "2"); got != "TCP · least connections · 4 members · 2 listeners" {
		t.Fatalf("poolSummary = %q", got)
	}

	shared := items["pool-1"]
	shared.ListenerIDs = []string{"lsn-1", "lsn-2", "lsn-2"}
	m = upd(t, m, poolSummariesMsg{lbID: "lb-1", items: map[string]osclient.PoolSummary{"pool-1": shared}})
	if got := m.loc.tree.Node("pool-1").Attrs["listener_count"]; got != "2" {
		t.Fatalf("pool listener count = %q, want 2 distinct attachments", got)
	}
}

func TestAllProjectsMode(t *testing.T) {
	m := start(t, osclient.SwitchCapability{
		CanSwitch: true, GlobalAdmin: true, AllProjectsChecked: true, CanAllProjects: true,
	})
	m = updExec(t, m, press("p")) // open switcher, load projects (cursor on ALL row)

	// Select the "all projects" row (index 0).
	nm, cmd := m.Update(press("enter"))
	m = nm.(Model)
	if cmd == nil {
		t.Fatal("selecting ALL should return a command")
	}
	m = upd(t, m, cmd()) // enterAllProjects -> switchedMsg -> onSwitched
	if !m.allProjects {
		t.Fatal("should be in all-projects mode after selecting ALL")
	}

	// The aggregated list now spans projects and tags each row with its project.
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	if len(m.entries) != 3 {
		t.Fatalf("all-projects list should aggregate across projects; got %d", len(m.entries))
	}
	var tagged bool
	for _, e := range m.entries {
		if e.lb.ID == "lb-3" && strings.Contains(e.extra, "beta") {
			tagged = true
		}
	}
	if !tagged {
		t.Errorf("cross-project LB should be tagged with its project; entries=%v", labels(m))
	}
	if !strings.Contains(m.View(), "global admin · all projects") {
		t.Errorf("subtitle should indicate all-projects scope")
	}

	// The global scope does not identify the selected LB's owner, so the inline
	// details must carry both the friendly project name and authoritative ID.
	m = updExec(t, m, press("enter"))
	overview := ansiRE.ReplaceAllString(m.View(), "")
	for _, owner := range []string{"Project name  alpha", "Project ID    p1"} {
		if !strings.Contains(overview, owner) {
			t.Fatalf("all-projects LB overview missing owner %q:\n%s", owner, overview)
		}
	}
	if !strings.Contains(strings.Split(overview, "\n")[1], "scope: global admin · all projects") {
		t.Fatalf("LB overview should retain the global scope subtitle:\n%s", overview)
	}
	m = upd(t, m, press("esc"))

	// Selecting a concrete project exits all-projects mode.
	m = updExec(t, m, press("p"))
	m = upd(t, m, press("down")) // move off the ALL row onto the first project
	nm, cmd = m.Update(press("enter"))
	m = nm.(Model)
	m = upd(t, m, cmd())
	if m.allProjects {
		t.Errorf("selecting a concrete project should exit all-projects mode")
	}
}

func labels(m Model) []string {
	out := make([]string, len(m.entries))
	for i, e := range m.entries {
		out[i] = e.label
	}
	return out
}
