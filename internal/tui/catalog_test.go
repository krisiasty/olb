package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/osclient"
)

// The service catalog is browsable in the auth area (services 6, endpoints 7,
// regions 8) and the three cross-link: a service lists its endpoints, an endpoint
// opens its service and region, a region lists its endpoints.
func TestServiceCatalogViews(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("S"))
	m = updExec(t, m, press("2")) // services
	if !m.loc.isTopLevelList() || m.loc.listKind() != kindService {
		t.Fatalf("catalog area view 2 should open the services list; loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"compute", "identity", "image"} {
		if !strings.Contains(view, want) {
			t.Fatalf("services list should contain %q:\n%s", want, view)
		}
	}

	// A service lists its endpoints (derived from the shared endpoints list).
	if idx, ok := m.selectLabel("service:compute"); ok {
		m.cursor = idx
	} else {
		t.Fatal("services should contain compute")
	}
	m = updExec(t, m, press("enter")) // updExec runs the endpoints load
	if !m.isServiceOverview() {
		t.Fatalf("enter on a service should open its overview; loc=%+v", m.loc)
	}
	// A service lists the regions it is present in (above) and its endpoints.
	sv := ansiRE.ReplaceAllString(m.View(), "")
	if iReg, iEp := strings.Index(sv, "REGIONS 1"), strings.Index(sv, "ENDPOINTS 2"); iReg < 0 || iEp < 0 || iReg > iEp {
		t.Fatalf("service detail should list REGIONS above ENDPOINTS:\n%s", sv)
	}
	if got := countEntryKind(m, entRegion); got != 1 {
		t.Fatalf("compute spans 1 region; got %d", got)
	}
	if got := countEntryKind(m, entEndpoint); got != 2 {
		t.Fatalf("compute should list 2 endpoints; got %d", got)
	}
	if !strings.Contains(sv, "compute/public@RegionOne · https://nova.example.com/v2.1") {
		t.Fatalf("catalog related-object attributes should use middle-dot separators:\n%s", sv)
	}

	// An endpoint opens to its service and region.
	if idx, ok := m.selectLabel("endpoint:compute/public@RegionOne"); ok {
		m.cursor = idx
	} else {
		t.Fatal("compute's endpoints should include the public one")
	}
	m = updExec(t, m, press("enter"))
	if !m.isEndpointOverview() {
		t.Fatalf("enter on an endpoint should open its overview; loc=%+v", m.loc)
	}
	ev := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"SERVICE 1", "REGION 1", "RegionOne"} {
		if !strings.Contains(ev, want) {
			t.Fatalf("endpoint detail should show %q:\n%s", want, ev)
		}
	}
	if idx, ok := m.selectLabel("region:RegionOne"); ok {
		m.cursor = idx
	}
	m = updExec(t, m, press("enter"))
	if !m.isRegionOverview() || m.loc.node.ID != "RegionOne" {
		t.Fatalf("opening an endpoint's region should show the region; loc=%+v", m.loc)
	}
	// A region lists the services present in it (above) and its endpoints.
	rv := ansiRE.ReplaceAllString(m.View(), "")
	if iSvc, iEp := strings.Index(rv, "SERVICES 2"), strings.Index(rv, "ENDPOINTS 3"); iSvc < 0 || iEp < 0 || iSvc > iEp {
		t.Fatalf("region detail should list SERVICES above ENDPOINTS:\n%s", rv)
	}
	if got := countEntryKind(m, entService); got != 2 {
		t.Fatalf("RegionOne hosts 2 services; got %d", got)
	}

	// RBAC denial degrades to an explanatory empty list.
	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).servicesErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("S"))
	denied = updExec(t, denied, press("2")) // services
	if denied.servicesErr == "" || len(denied.entries) != 0 {
		t.Fatalf("RBAC denial should degrade to an empty list; err=%q entries=%d", denied.servicesErr, len(denied.entries))
	}
}

// The endpoints list is browsable on its own (auth view 7).
func TestEndpointsListView(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("S"))
	m = updExec(t, m, press("3")) // endpoints
	if m.loc.listKind() != kindEndpoint {
		t.Fatalf("catalog area view 3 should open the endpoints list; loc=%+v", m.loc)
	}
	if got := countEntryKind(m, entEndpoint); got != 4 {
		t.Fatalf("endpoints list should have 4 rows; got %d", got)
	}
	if v := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(v, "public") || !strings.Contains(v, "RegionOne") {
		t.Fatalf("endpoints list should show interface and region:\n%s", v)
	}
}

// The * key opens the current-token pop-up (a local read of the auth result),
// and esc closes it.
func TestCurrentTokenOverlay(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = upd(t, m, press("*"))
	if m.overlay != overlayToken {
		t.Fatalf("* should open the token overlay; overlay=%d", m.overlay)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"CURRENT TOKEN",
		"User      admin  (domain: Default)",
		"Scope     project: alpha  (domain: Default)",
		"admin, member",
		"Expires",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("token overlay should show %q:\n%s", want, view)
		}
	}
	m = upd(t, m, press("esc"))
	if m.overlay != overlayNone {
		t.Fatalf("esc should close the token overlay; overlay=%d", m.overlay)
	}

	// An unavailable token degrades rather than erroring.
	m2 := start(t, switchCapability{CanSwitch: true})
	m2.backend.(*fakeBackend).token = &osclient.TokenInfo{Available: false}
	m2 = upd(t, m2, press("*"))
	if v := ansiRE.ReplaceAllString(m2.View(), ""); !strings.Contains(v, "unavailable") {
		t.Fatalf("an unavailable token should say so:\n%s", v)
	}
}

func TestCurrentTokenDomainScopeFormatting(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).token = &osclient.TokenInfo{
		Available:  true,
		UserName:   "test-kc",
		UserDomain: "Default",
		ScopeType:  "domain",
		ScopeName:  "Default",
	}
	m = upd(t, m, press("*"))

	plain := ansiRE.ReplaceAllString(m.tokenModalBox(), "")
	for _, want := range []string{
		"User      test-kc  (domain: Default)",
		"Scope     domain: Default",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("domain-scoped token modal should show %q:\n%s", want, plain)
		}
	}
}

func TestCurrentTokenRolesWrapWithinModal(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.width, m.height = 42, 24
	m.backend.(*fakeBackend).token = &osclient.TokenInfo{
		Available: true,
		UserName:  "alice",
		ScopeType: "project",
		ScopeName: "alpha",
		Roles: []string{
			"administrator",
			"load-balancer_observer",
			"member",
			"reader",
		},
	}
	m = upd(t, m, press("*"))

	box := m.tokenModalBox()
	if width := lipgloss.Width(box); width >= m.width {
		t.Fatalf("token modal width = %d, terminal width = %d", width, m.width)
	}
	plain := ansiRE.ReplaceAllString(box, "")
	lines := strings.Split(plain, "\n")
	roleLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Roles") {
			roleLine = i
			break
		}
	}
	if roleLine < 0 || roleLine+1 >= len(lines) || !strings.Contains(lines[roleLine+1], "load-balancer") {
		t.Fatalf("roles should continue on another aligned line:\n%s", plain)
	}
	for _, role := range m.tokenInfo.Roles {
		if !strings.Contains(plain, role) {
			t.Fatalf("wrapped modal lost role %q:\n%s", role, plain)
		}
	}
}
