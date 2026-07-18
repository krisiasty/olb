package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
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
	cap osclient.SwitchCapability
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
	return model.Build(&top.Statuses, model.LBMeta{VipAddress: "203.0.113.9", VipPortID: "port-9", Provider: "amphora"})
}

func (f *fakeBackend) ListLoadBalancers(context.Context) ([]osclient.LB, error) {
	return []osclient.LB{
		{ID: "lb-1", Name: "lb1", Provider: "amphora", VipAddress: "203.0.113.9", VipPortID: "port-9", ProvisioningStatus: "ACTIVE", OperatingStatus: "DEGRADED"},
		{ID: "lb-2", Name: "lb2", Provider: "ovn", ProvisioningStatus: "ACTIVE", OperatingStatus: "ERROR"},
	}, nil
}

func (f *fakeBackend) GetTree(_ context.Context, lbID string, _ *model.LBMeta) (*model.Tree, error) {
	return newTree(), nil
}

func (f *fakeBackend) FetchDetail(_ context.Context, n *model.Node) (osclient.DetailResult, error) {
	res := osclient.DetailResult{Raw: map[string]any{"id": n.ID, "name": n.Name}, Attrs: map[string]string{"probed": "yes"}}
	switch n.Type {
	case model.TypeListener:
		res.IsListener = true
		res.ListenerDefaultPoolID = "pool-1"
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

func (f *fakeBackend) ResolveFloatingIP(context.Context, string) (*model.Node, error) {
	return nil, nil // internal LB: no floating IP
}

func (f *fakeBackend) ResolveInstance(_ context.Context, addr string) (*model.Node, error) {
	n := model.NewNode(model.TypeInstance, "srv-1", "web-server-1")
	n.SetAttr("address", addr)
	n.Raw = map[string]any{"id": "srv-1"}
	n.DetailLoaded = true
	return n, nil
}

func (f *fakeBackend) ListAmphorae(_ context.Context, lbID string) ([]*model.Node, error) {
	a := model.NewNode(model.TypeAmphora, "amp-1", "amp-1")
	a.OwningLBID = lbID
	a.DetailLoaded = true
	a.Raw = map[string]any{"id": "amp-1"}
	return []*model.Node{a}, nil
}

func (f *fakeBackend) ListProjects(context.Context) ([]osclient.ProjectInfo, error) {
	return []osclient.ProjectInfo{{ID: "p1", Name: "alpha"}, {ID: "p2", Name: "beta"}}, nil
}

func (f *fakeBackend) SwitchProject(context.Context, osclient.ProjectInfo) error { return nil }
func (f *fakeBackend) CurrentProject() osclient.ProjectInfo {
	return osclient.ProjectInfo{ID: "p1", Name: "alpha"}
}
func (f *fakeBackend) SwitchCapability() osclient.SwitchCapability { return f.cap }

// --- driver helpers -------------------------------------------------------

func start(t *testing.T, cap osclient.SwitchCapability) Model {
	t.Helper()
	m := New(&fakeBackend{cap: cap}, Config{PrintMode: true, HistoryCap: 50})
	m.Init() // populates history with the LB-list root
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
	// LB children include a VIP, pools, listeners, and the amphorae placeholder.
	for _, want := range []string{"vip:", "pool:web", "listener:http", "amphorae"} {
		if _, ok := m.selectLabel(want); !ok {
			t.Errorf("LB view missing %q; entries=%v", want, labels(m))
		}
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

	// The listener shows a reference edge to its pool ("→ pool:web").
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

func TestInspectCopyAndOverlays(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // into LB (loc.node = LB)

	// d -> detail overlay (fetches detail + stats).
	m = updExec(t, m, press("d"))
	if m.overlay != overlayDetail {
		t.Fatalf("d should open the detail overlay, got %v", m.overlay)
	}
	m = upd(t, m, statsMsg{lbID: "lb-1", stats: map[string]any{"active_connections": 1}})
	m = upd(t, m, press("esc"))

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

	// New navigation after a back must truncate the forward portion.
	m = updExec(t, m, press("esc")) // back to LB (cursor not at tip)
	m = updExec(t, m, tea.KeyMsg{Type: tea.KeyCtrlHome})
	if m.hist.canForward() {
		t.Errorf("new navigation should have truncated the forward history")
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

func TestAmphoraLazyLoad(t *testing.T) {
	m := start(t, osclient.SwitchCapability{CanSwitch: true})
	m = updExec(t, m, press("enter")) // LB
	i, ok := m.selectLabel("amphorae")
	if !ok {
		t.Fatal("amphora provider LB should show an amphorae placeholder")
	}
	m.cursor = i
	m = updExec(t, m, press("enter")) // loads amphorae, then navigates in
	if m.loc.node == nil || m.loc.node.Type != model.TypeAmphora {
		t.Fatalf("expected to land on the amphorae node, got %+v", m.loc.node)
	}
}

func labels(m Model) []string {
	out := make([]string, len(m.entries))
	for i, e := range m.entries {
		out[i] = e.label
	}
	return out
}
