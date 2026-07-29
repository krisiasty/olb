package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// Uppercase accelerators switch areas; the header chip and active workspace
// follow, and the target area loads its first view.
func TestUppercaseKeySwitchesArea(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	if areaOf(m.activeWorkspace) != areaLB {
		t.Fatalf("startup area = %v, want areaLB", areaOf(m.activeWorkspace))
	}

	m = updExec(t, m, press("A"))
	if m.activeWorkspace != kindDomain || areaOf(m.activeWorkspace) != areaIdentity {
		t.Fatalf("A did not switch to the identity area's first view (domains): active=%v", m.activeWorkspace)
	}
	if !m.loc.id.Equal(model.DomainListIdentity) {
		t.Fatalf("identity area root = %+v, want the domains list", m.loc.id)
	}
	if chip := ansiRE.ReplaceAllString(m.breadcrumbLine(), ""); !strings.Contains(chip, "A-1") || !strings.Contains(chip, "domains") {
		t.Fatalf("breadcrumb should show the A-1 area chip and domains label: %q", chip)
	}

	m = updExec(t, m, press("L"))
	if areaOf(m.activeWorkspace) != areaLB {
		t.Fatalf("L did not switch back to the load-balancer area: active=%v", m.activeWorkspace)
	}
}

// Number keys are relative to the active area: they index that area's views, so
// out-of-range digits are no-ops and do not leak across areas.
func TestNumberKeysAreaRelative(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("3"))
	if m.activeWorkspace != kindListener {
		t.Fatalf("3 in the LB area should select listeners; active=%v", m.activeWorkspace)
	}

	m = updExec(t, m, press("A")) // identity area: 1 domains, 2 projects, …
	m = updExec(t, m, press("2"))
	if m.activeWorkspace != kindProject {
		t.Fatalf("2 in the identity area should select projects; active=%v", m.activeWorkspace)
	}
	m = updExec(t, m, press("9")) // out of range for identity (5 views)
	if m.activeWorkspace != kindProject {
		t.Fatalf("out-of-range digit in the identity area should be a no-op; active=%v", m.activeWorkspace)
	}
	m = updExec(t, m, press("1"))
	if m.activeWorkspace != kindDomain {
		t.Fatalf("1 in the identity area should select domains; active=%v", m.activeWorkspace)
	}
}

// Returning to an area restores the view last active there rather than its first.
func TestAreaLastViewRestored(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("4")) // pools within the LB area
	if m.activeWorkspace != kindPool {
		t.Fatalf("setup: 4 should select pools; active=%v", m.activeWorkspace)
	}
	m = updExec(t, m, press("A")) // leave for auth
	m = updExec(t, m, press("L")) // return to LB area
	if m.activeWorkspace != kindPool {
		t.Fatalf("returning to the LB area should restore pools; active=%v", m.activeWorkspace)
	}
}

// The switcher opens on space, filters live, and enter jumps to the highlighted
// area+view; esc cancels without navigating.
func TestAreaSwitcherFilterAndJump(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press(" "))
	if m.overlay != overlaySwitcher {
		t.Fatalf("space should open the switcher; overlay=%v", m.overlay)
	}

	m = upd(t, m, press("/"))
	if !m.search.Focused() {
		t.Fatal("/ should focus the switcher filter")
	}
	for _, r := range "users" {
		m = upd(t, m, press(string(r)))
	}
	rows := m.filteredSwitcherRows()
	if len(rows) != 1 || rows[0].view != kindUser {
		t.Fatalf("filter \"users\" should match only the users view; got %d rows", len(rows))
	}
	m = upd(t, m, press("enter"))     // apply the filter (blur input)
	m = updExec(t, m, press("enter")) // select the highlighted row
	if m.overlay != overlayNone || m.activeWorkspace != kindUser {
		t.Fatalf("switcher enter should jump to users and close; overlay=%v active=%v", m.overlay, m.activeWorkspace)
	}
}

// The switcher groups views under non-selectable area headers (with counts),
// formatted like the related-objects list.
func TestAreaSwitcherGroupsByArea(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press(" "))
	view := ansiRE.ReplaceAllString(m.View(), "")
	box := ansiRE.ReplaceAllString(m.switcherModalBox(), "")
	if !strings.Contains(box, "SWITCH AREA / VIEW") || !strings.Contains(box, "╭") || !strings.Contains(box, "╯") {
		t.Fatalf("area switcher should use an uppercase, framed modal:\n%s", box)
	}
	if border := lineContaining(view, "╭"); strings.Index(border, "╭") < 1 {
		t.Fatalf("area switcher frame should be centered over the current view:\n%s", view)
	}
	if !strings.Contains(view, "LOAD BALANCERS 5") {
		t.Fatalf("switcher should show the load-balancer area heading with its view count:\n%s", view)
	}
	if !strings.Contains(view, "IDENTITY & ACCESS 5") {
		t.Fatalf("switcher should show the identity area heading with its view count:\n%s", view)
	}
	if !strings.Contains(view, "SERVICE CATALOG 3") {
		t.Fatalf("switcher should show the catalog area heading with its view count:\n%s", view)
	}
	if !strings.Contains(view, "COMPUTE 1") {
		t.Fatalf("switcher should show the compute area heading with its view count:\n%s", view)
	}
	// Headers are not selectable: the cursor space is the view rows only.
	if got := len(m.filteredSwitcherRows()); got != 14 {
		t.Fatalf("switcher selectable rows = %d, want 14 views across four areas", got)
	}
}

// Uppercase accelerators work inside the switcher too: pressing an area's key
// jumps straight to it and closes the overlay.
func TestAreaSwitcherAcceleratorJumps(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press(" "))
	m = updExec(t, m, press("A"))
	if m.overlay != overlayNone || m.activeWorkspace != kindDomain {
		t.Fatalf("A in the switcher should jump to the identity area and close; overlay=%v active=%v", m.overlay, m.activeWorkspace)
	}
}

func TestAreaSwitcherEscCancels(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	before := m.activeWorkspace
	m = updExec(t, m, press(" "))
	m = upd(t, m, press("esc"))
	if m.overlay != overlayNone || m.activeWorkspace != before {
		t.Fatalf("esc should close the switcher without navigating; overlay=%v active=%v", m.overlay, m.activeWorkspace)
	}
}

// The users list loads through the backend and, when RBAC denies it, degrades to
// an explanatory empty list rather than an error.
func TestUsersListLoadsAndDegrades(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users
	if len(m.entries) != 3 || m.entries[0].kind != entUser {
		t.Fatalf("users list should hold the three fake users; got %d entries", len(m.entries))
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "alice") || !strings.Contains(view, "EMAIL") {
		t.Fatalf("users table should show a user and the EMAIL column:\n%s", view)
	}
	// Columns are NAME, DESCRIPTION, EMAIL, DOMAIN, ENABLED in that order.
	header := headerLine(ansiRE.ReplaceAllString(m.View(), ""), "NAME")
	iName := strings.Index(header, "NAME")
	iDesc := strings.Index(header, "DESCRIPTION")
	iEmail := strings.Index(header, "EMAIL")
	iDomain := strings.Index(header, "DOMAIN")
	iEnabled := strings.Index(header, "ENABLED")
	if iName >= iDesc || iDesc >= iEmail || iEmail >= iDomain || iDomain >= iEnabled {
		t.Fatalf("users columns out of order (want NAME<DESCRIPTION<EMAIL<DOMAIN<ENABLED): %q", header)
	}

	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).usersErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("4")) // users
	if denied.usersErr == "" || len(denied.entries) != 0 {
		t.Fatalf("RBAC denial should degrade to an empty list with a reason; err=%q entries=%d", denied.usersErr, len(denied.entries))
	}
	if view := ansiRE.ReplaceAllString(denied.View(), ""); !strings.Contains(view, "admin RBAC") {
		t.Fatalf("denied users list should explain the admin requirement:\n%s", view)
	}
}

// The users list is sortable by every field, including the resolved/derived
// ones (description, email, domain name, enabled).
func TestUsersListSortableByAnyField(t *testing.T) {
	labels := func(m Model) []string {
		out := make([]string, 0, len(m.entries))
		for _, e := range m.entries {
			out = append(out, e.label)
		}
		return out
	}
	apply := func(m Model, key string) []string {
		m.sortKey = key
		(&m).applyFilters()
		return labels(m)
	}

	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users

	// email ascending: bob (no email) sorts before admin@ / alice@.
	if got := apply(m, "email"); got[0] != "user:bob" {
		t.Fatalf("sort by email should put the empty-email user first: %v", got)
	}
	// enabled ascending: disabled (bob) before the enabled users.
	if got := apply(m, "enabled"); got[0] != "user:bob" {
		t.Fatalf("sort by enabled should put disabled users first: %v", got)
	}
	// description ascending: only admin has one, so it sorts last (empty first).
	if got := apply(m, "description"); got[len(got)-1] != "user:admin" {
		t.Fatalf("sort by description should put the only described user last: %v", got)
	}
	// domain name is an offered, resolvable sort column.
	found := false
	for _, c := range m.sortColumns() {
		if c.key == "domain_name" && c.value != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("users list should offer a domain-name sort column")
	}
}

// The auth area's second view lists Keystone domains, loads through the backend,
// degrades on RBAC denial, and opens a per-domain detail overview.
func TestDomainsListAndDetail(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("1")) // domains
	if m.activeWorkspace != kindDomain || len(m.entries) != 3 || m.entries[0].kind != entDomain {
		t.Fatalf("domains view should load three domains; active=%v entries=%d", m.activeWorkspace, len(m.entries))
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "Default") || !strings.Contains(view, "the default domain") {
		t.Fatalf("domains table should show a domain and its description:\n%s", view)
	}

	// Open the Default domain's detail.
	idx, ok := m.selectLabel("domain:Default")
	if !ok {
		t.Fatal("domains list should contain Default")
	}
	m.cursor = idx
	m = updExec(t, m, press("enter"))
	if !m.isDomainOverview() {
		t.Fatalf("enter on a domain should open its detail overview; loc=%+v", m.loc)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "DOMAIN DETAILS") || !strings.Contains(view, "the default domain") {
		t.Fatalf("domain overview should show details:\n%s", view)
	}

	// RBAC denial degrades to an explanatory empty list.
	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).domainsErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("1")) // domains
	if denied.domainsErr == "" || len(denied.entries) != 0 {
		t.Fatalf("RBAC denial should degrade to an empty list; err=%q entries=%d", denied.domainsErr, len(denied.entries))
	}
}

// The auth area's third view lists Keystone groups, loads through the backend,
// degrades on RBAC denial, and opens a per-group detail overview.
func TestGroupsListAndDetail(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A")) // users
	m = updExec(t, m, press("3")) // groups
	if m.activeWorkspace != kindGroup || len(m.entries) != 2 || m.entries[0].kind != entUserGroup {
		t.Fatalf("groups view should load two groups; active=%v entries=%d", m.activeWorkspace, len(m.entries))
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "admins") || !strings.Contains(view, "Default") {
		t.Fatalf("groups table should show a group and its resolved domain:\n%s", view)
	}

	idx, ok := m.selectLabel("group:admins")
	if !ok {
		t.Fatal("groups list should contain admins")
	}
	m.cursor = idx
	m = updExec(t, m, press("enter"))
	if !m.isGroupOverview() {
		t.Fatalf("enter on a group should open its detail overview; loc=%+v", m.loc)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "GROUP DETAILS") || !strings.Contains(view, "cloud administrators") {
		t.Fatalf("group overview should show details:\n%s", view)
	}

	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).groupsErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("3"))
	if denied.groupsErr == "" || len(denied.entries) != 0 {
		t.Fatalf("RBAC denial should degrade to an empty list; err=%q entries=%d", denied.groupsErr, len(denied.entries))
	}
}

// The auth area's fourth view is a browsable projects list (distinct from the
// re-scoping selector), with a per-project detail overview.
func TestProjectsListAndDetail(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("2")) // projects
	if m.activeWorkspace != kindProject || len(m.entries) != 2 || m.entries[0].kind != entProject {
		t.Fatalf("projects view should load two projects; active=%v entries=%d", m.activeWorkspace, len(m.entries))
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "alpha") || !strings.Contains(view, "payments") {
		t.Fatalf("projects table should show a project and its description:\n%s", view)
	}

	idx, ok := m.selectLabel("project:alpha")
	if !ok {
		t.Fatal("projects list should contain alpha")
	}
	m.cursor = idx
	m = updExec(t, m, press("enter"))
	if !m.isProjectOverview() {
		t.Fatalf("enter on a project should open its detail overview; loc=%+v", m.loc)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "PROJECT DETAILS") || !strings.Contains(view, "Default") {
		t.Fatalf("project overview should show details and resolved domain:\n%s", view)
	}

	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).projectListEr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("2")) // projects
	if denied.projectListErr == "" || len(denied.entries) != 0 {
		t.Fatalf("RBAC denial should degrade to an empty list; err=%q entries=%d", denied.projectListErr, len(denied.entries))
	}
}

// The auth area's fifth view lists Keystone roles, with a per-role detail that
// distinguishes global from domain-scoped roles.
func TestRolesListAndDetail(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A")) // users
	m = updExec(t, m, press("5")) // roles
	if m.activeWorkspace != kindRole || len(m.entries) != 3 || m.entries[0].kind != entRole {
		t.Fatalf("roles view should load three roles; active=%v entries=%d", m.activeWorkspace, len(m.entries))
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "admin") || !strings.Contains(view, "cloud administrator") {
		t.Fatalf("roles table should show a role and its description:\n%s", view)
	} else if header := headerLine(view, "DESCRIPTION"); !strings.Contains(header, "DOMAIN") || strings.Contains(header, "SOURCE") {
		t.Fatalf("full role catalog should retain DESCRIPTION and DOMAIN columns: %q", header)
	}

	// A global role.
	idx, ok := m.selectLabel("role:admin")
	if !ok {
		t.Fatal("roles list should contain admin")
	}
	m.cursor = idx
	m = updExec(t, m, press("enter"))
	if !m.isRoleOverview() {
		t.Fatalf("enter on a role should open its detail overview; loc=%+v", m.loc)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "ROLE DETAILS") || !strings.Contains(view, "global") {
		t.Fatalf("role overview should mark a global role:\n%s", view)
	}

	// A domain-scoped role shows its domain.
	m = updExec(t, m, press("esc"))
	if idx, ok := m.selectLabel("role:reader"); ok {
		m.cursor = idx
	}
	m = updExec(t, m, press("enter"))
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "DOMAIN") || !strings.Contains(view, "Default") {
		t.Fatalf("domain-scoped role overview should show its domain:\n%s", view)
	}

	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).rolesErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("5"))
	if denied.rolesErr == "" || len(denied.entries) != 0 {
		t.Fatalf("RBAC denial should degrade to an empty list; err=%q entries=%d", denied.rolesErr, len(denied.entries))
	}
}

func TestRestrictedIdentityListsExplainTheirSelfServiceSource(t *testing.T) {
	t.Run("current user's domain", func(t *testing.T) {
		m := start(t, switchCapability{CanSwitch: true})
		backend := m.backend.(*fakeBackend)
		backend.domains = []osclient.Domain{{
			ID: "default", Name: "Default", Partial: true,
		}}
		backend.domainsRestriction = "current user's domain"
		m = updExec(t, m, press("A"))
		m = updExec(t, m, press("1"))

		view := ansiRE.ReplaceAllString(m.View(), "")
		if !strings.Contains(view, "showing current user's domain") || !strings.Contains(view, "Default") {
			t.Fatalf("restricted domains view should show its source and current domain:\n%s", view)
		}
		if len(m.entries) != 1 || !m.entries[0].domain.Partial {
			t.Fatalf("token-only domain should remain marked partial: %+v", m.entries)
		}
	})

	t.Run("current user", func(t *testing.T) {
		m := start(t, switchCapability{CanSwitch: true})
		backend := m.backend.(*fakeBackend)
		backend.users = []osclient.User{{
			ID: "u-1", Name: "admin", DomainID: "default", DomainName: "Default", Partial: true,
		}}
		backend.usersRestriction = "current user only"
		m = updExec(t, m, press("A"))
		m = updExec(t, m, press("4"))

		view := ansiRE.ReplaceAllString(m.View(), "")
		if !strings.Contains(view, "showing current user only") || !strings.Contains(view, "admin") {
			t.Fatalf("restricted users view should show its source and current user:\n%s", view)
		}
		if row := lineContaining(view, "admin"); !strings.Contains(row, "—") {
			t.Fatalf("token-only user should render unknown account state: %q", row)
		}
	})

	t.Run("current user's groups, including empty", func(t *testing.T) {
		m := start(t, switchCapability{CanSwitch: true})
		backend := m.backend.(*fakeBackend)
		backend.groups = []osclient.Group{}
		backend.groupsRestriction = "current user's groups"
		m = updExec(t, m, press("A"))
		m = updExec(t, m, press("3"))

		view := ansiRE.ReplaceAllString(m.View(), "")
		if !strings.Contains(view, "showing current user's groups") || !strings.Contains(view, "— empty —") {
			t.Fatalf("empty self-service groups view should retain its source label:\n%s", view)
		}
	})

	t.Run("active-token roles", func(t *testing.T) {
		m := start(t, switchCapability{CanSwitch: true})
		backend := m.backend.(*fakeBackend)
		backend.roles = []osclient.Role{{
			ID: "r-2", Name: "member", TokenScoped: true,
			ScopeType: "project", ScopeName: "alpha", ScopeID: "p1",
		}}
		backend.rolesRestriction = "roles in active token"
		m = updExec(t, m, press("A"))
		m = updExec(t, m, press("5"))

		view := ansiRE.ReplaceAllString(m.View(), "")
		if !strings.Contains(view, "showing roles in active token") ||
			!strings.Contains(view, "active token") ||
			!strings.Contains(view, "project:alpha") {
			t.Fatalf("restricted roles view should identify token source and scope:\n%s", view)
		}
		header := headerLine(view, "SOURCE")
		if !strings.Contains(header, "SCOPE") || strings.Contains(header, "DESCRIPTION") || strings.Contains(header, "DOMAIN") {
			t.Fatalf("token role columns should be NAME, SOURCE, SCOPE: %q", header)
		}

		m = updExec(t, m, press("enter"))
		detail := ansiRE.ReplaceAllString(m.View(), "")
		for _, unwanted := range []string{"IMPLIED ROLES", "ROLE ASSIGNMENTS", "— no related objects —"} {
			if strings.Contains(detail, unwanted) {
				t.Fatalf("token role detail should not show unavailable %q section:\n%s", unwanted, detail)
			}
		}
		if !strings.Contains(detail, "Source") || !strings.Contains(detail, "TOKEN SCOPE") || !strings.Contains(detail, "alpha") {
			t.Fatalf("token role detail should explain its source and effective scope:\n%s", detail)
		}
	})
}

// A role detail lists the roles it implies and the assignments granting it;
// opening an assignment jumps to its actor.
func TestRoleRelations(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("5")) // roles
	idx, ok := m.selectLabel("role:admin")
	if !ok {
		t.Fatal("roles list should contain admin")
	}
	m.cursor = idx
	m = updExec(t, m, press("enter")) // updExec runs the relations-load cmd
	if !m.isRoleOverview() {
		t.Fatalf("enter on a role should open its overview; loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"IMPLIED ROLES 1", "member", "ROLE ASSIGNMENTS 2", "user:alice", "group:admins", "project:alpha", "domain:Default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("role overview should show %q:\n%s", want, view)
		}
	}
	if got := countEntryKind(m, entRole); got != 1 {
		t.Fatalf("admin should imply 1 role; got %d", got)
	}
	if got := countEntryKind(m, entAssignment); got != 2 {
		t.Fatalf("admin should have 2 assignments; got %d", got)
	}

	// Opening an assignment jumps to its actor (the user).
	ai, ok := m.selectLabel("user:alice")
	if !ok {
		t.Fatal("assignments should include alice")
	}
	m.cursor = ai
	m = updExec(t, m, press("enter"))
	if !m.isUserOverview() || m.loc.node.ID != "u-2" {
		t.Fatalf("opening an assignment should jump to its actor (alice); loc=%+v", m.loc)
	}
}

func TestRolesListMarksRolesWithImplications(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("5"))

	plain := ansiRE.ReplaceAllString(m.View(), "")
	if strings.Count(plain, "⧉") != 1 {
		t.Fatalf("roles list should mark exactly one implying role:\n%s", plain)
	}
	if row := lineContaining(plain, "admin"); !strings.Contains(row, "⧉") {
		t.Fatalf("admin role should carry the implication marker: %q", row)
	}
	if row := lineContaining(plain, "member"); strings.Contains(row, "⧉") {
		t.Fatalf("non-implying member role should have an empty marker gutter: %q", row)
	}

	// Move selection away from admin so the marker uses its ordinary foreground.
	if idx, ok := m.selectLabel("role:member"); ok {
		m.cursor = idx
	}
	marker := m.st.attrs.Bold(true).Render("⧉")
	if !strings.Contains(m.View(), marker) {
		t.Fatalf("implication marker does not use the service/system-user color:\n%s", m.View())
	}
	sample := "⧉ admin"
	entry := entry{kind: entRole, role: osclient.Role{ImpliesRoles: true}}
	wantStyled := m.st.attrs.Bold(true).Render("⧉") + m.st.attrs.Render(" admin")
	if got := m.styleRoleImplicationMarker(entry, sample, m.st.attrs); got != wantStyled {
		t.Fatalf("implying role row does not use the service/system-user style:\ngot  %q\nwant %q", got, wantStyled)
	}

	m = upd(t, m, press("d"))
	plain = ansiRE.ReplaceAllString(m.View(), "")
	if row := lineContaining(plain, "r-1"); !strings.Contains(row, "⧉ r-1") {
		t.Fatalf("ID mode should retain the marker beside the role ID: %q", row)
	}

	help := helpContent(true, true, false, false)
	if !strings.Contains(help, "⧉  role includes one or more implied roles") {
		t.Fatalf("help does not explain the implication marker:\n%s", help)
	}
}

func TestRoleAssignmentScopeColors(t *testing.T) {
	m := Model{st: newStyles(), width: 120}
	tests := []struct {
		name   string
		target string
		extra  string
		style  lipgloss.Style
	}{
		{"system", "system", "on system (cluster-wide)", lipgloss.NewStyle().Foreground(lipgloss.Color("214"))},
		{"domain", "domain", "on domain:Default", lipgloss.NewStyle().Foreground(lipgloss.Color("226"))},
		{"project", "project", "on project:alpha", m.st.attrs},
	}
	origins := []struct {
		name      string
		inherited bool
		token     bool
	}{
		{name: "direct"},
		{name: "inherited effective", inherited: true},
		{name: "active-token effective", token: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := m.st.attrs.Render(" · ") + test.style.Render(test.extra)
			for _, origin := range origins {
				e := entry{
					kind:  entAssignment,
					label: "user:alice",
					extra: test.extra,
					assignment: osclient.RoleAssignment{
						TargetType:  test.target,
						Inherited:   origin.inherited,
						TokenScoped: origin.token,
					},
					assignmentPivot: pivotRole,
				}
				for _, selected := range []bool{false, true} {
					got := m.renderIdentityRow(e, selected)
					if !strings.Contains(got, want) {
						t.Fatalf("origin=%s selected=%v assignment target is not styled correctly:\nwant segment %q\nrow          %q", origin.name, selected, want, got)
					}
				}
			}
		})
	}

	// A domain detail already supplies the assignment target as context. Its
	// trailing "as role:…" fact must stay neutral rather than turning yellow.
	domainDetail := entry{
		kind:            entAssignment,
		label:           "user:alice",
		extra:           "as role:admin",
		assignment:      osclient.RoleAssignment{TargetType: "domain"},
		assignmentPivot: pivotTarget,
	}
	got := m.renderIdentityRow(domainDetail, false)
	want := m.st.attrs.Render(" · ") + m.st.attrs.Render(domainDetail.extra)
	if !strings.Contains(got, want) {
		t.Fatalf("domain-detail assignment fact should remain neutral:\nwant segment %q\nrow          %q", want, got)
	}
}

// A user's and a project's role assignments are shown from their own side — the
// mirror of the role→assignments view. A user's list is effective (includes roles
// inherited via group membership); opening an actor-side row jumps to the role,
// and a target-side row jumps to the actor.
func TestOwnerAssignments(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users
	idx, ok := m.selectLabel("user:alice")
	if !ok {
		t.Fatal("users list should contain alice")
	}
	m.cursor = idx
	m = updExecAll(t, m, press("enter")) // open alice; loads groups + assignments
	if !m.isUserOverview() {
		t.Fatalf("enter on a user should open its overview; loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	// alice holds admin on alpha directly and member on beta inherited via her
	// group — the beta row proves the listing is effective.
	for _, want := range []string{"EFFECTIVE ROLE ASSIGNMENTS 2", "role:admin", "on project:alpha", "role:member", "on project:beta"} {
		if !strings.Contains(view, want) {
			t.Fatalf("user overview should show %q:\n%s", want, view)
		}
	}
	if got := countEntryKind(m, entAssignment); got != 2 {
		t.Fatalf("alice should have 2 effective assignments; got %d", got)
	}
	// The marker distinguishes a directly-held grant (● admin on alpha) from an
	// inherited one (○ member on beta, via the admins group). Only the inherited
	// assignment row is hollow; every other identity row keeps the solid dot.
	if !strings.Contains(view, "● role:admin") {
		t.Fatalf("a directly-held assignment should show the solid ● marker:\n%s", view)
	}
	if !strings.Contains(view, "○ role:member") {
		t.Fatalf("an inherited assignment should show the hollow ○ marker:\n%s", view)
	}
	if n := strings.Count(view, "○"); n != 1 {
		t.Fatalf("only the inherited assignment should be hollow; found %d:\n%s", n, view)
	}
	// The distinct target projects are also listed as navigable related objects.
	if !strings.Contains(view, "PROJECTS 2") {
		t.Fatalf("user overview should list its access projects:\n%s", view)
	}
	if got := countEntryKind(m, entProject); got != 2 {
		t.Fatalf("alice should list her 2 access projects; got %d", got)
	}

	// An actor-side assignment row opens the role it grants.
	ri, ok := m.selectLabel("role:admin")
	if !ok {
		t.Fatal("alice's assignments should include the admin role")
	}
	m.cursor = ri
	m = updExecAll(t, m, press("enter"))
	if !m.isRoleOverview() || m.loc.node.ID != "r-1" {
		t.Fatalf("opening a user's assignment should jump to the role (admin); loc=%+v", m.loc)
	}

	// The project side lists who holds a role there, and opens that actor.
	m = start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("2")) // projects
	if idx, ok := m.selectLabel("project:alpha"); ok {
		m.cursor = idx
	}
	m = updExecAll(t, m, press("enter")) // open alpha; loads its assignments
	if !m.isProjectOverview() {
		t.Fatalf("enter on a project should open its overview; loc=%+v", m.loc)
	}
	view = ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"ROLE ASSIGNMENTS 1", "user:alice", "as role:admin"} {
		if !strings.Contains(view, want) {
			t.Fatalf("project overview should show %q:\n%s", want, view)
		}
	}
	if got := countEntryKind(m, entAssignment); got != 1 {
		t.Fatalf("project alpha should have 1 assignment; got %d", got)
	}
	ai, ok := m.selectLabel("user:alice")
	if !ok {
		t.Fatal("project alpha's assignments should include alice")
	}
	m.cursor = ai
	m = updExecAll(t, m, press("enter"))
	if !m.isUserOverview() || m.loc.node.ID != "u-2" {
		t.Fatalf("opening a project's assignment should jump to its actor (alice); loc=%+v", m.loc)
	}

	// A project reached only through an assignment (never listed) still opens.
	bare := start(t, switchCapability{CanSwitch: true})
	bare = updExec(t, bare, press("A"))
	bare = updExec(t, bare, press("4")) // users
	if bi, ok := bare.selectLabel("user:alice"); ok {
		bare.cursor = bi
	}
	bare = updExecAll(t, bare, press("enter"))
	if pi, ok := bare.selectLabel("project:beta"); ok {
		bare.cursor = pi
	} else {
		t.Fatal("alice's access projects should include beta")
	}
	bare = updExecAll(t, bare, press("enter"))
	if !bare.isProjectOverview() || bare.loc.node.ID != "p2" {
		t.Fatalf("an assignment-only project should open its detail; loc=%+v", bare.loc)
	}

	// An RBAC denial degrades to an empty section recorded per object.
	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).assignmentErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("4")) // users
	if di, ok := denied.selectLabel("user:alice"); ok {
		denied.cursor = di
	}
	denied = updExecAll(t, denied, press("enter"))
	if denied.assignmentsErr[assignmentKey{ownerUser, "u-2"}] == "" || countEntryKind(denied, entAssignment) != 0 {
		t.Fatalf("RBAC denial should record a per-object error and list no assignments; err=%q count=%d",
			denied.assignmentsErr[assignmentKey{ownerUser, "u-2"}], countEntryKind(denied, entAssignment))
	}
	if view := ansiRE.ReplaceAllString(denied.View(), ""); !strings.Contains(view, "EFFECTIVE ROLE ASSIGNMENTS 0") {
		t.Fatalf("a denied user should still show the empty EFFECTIVE ROLE ASSIGNMENTS 0 header:\n%s", view)
	}

	// The authenticated user's accessible projects remain available through
	// Keystone's self-service project endpoint even when assignments are denied.
	current := start(t, switchCapability{CanSwitch: true})
	current.backend.(*fakeBackend).assignmentErr = osclient.ErrAdminRequired
	current = updExec(t, current, press("A"))
	current = updExec(t, current, press("4")) // users
	if ci, ok := current.selectLabel("user:admin"); ok {
		current.cursor = ci
	} else {
		t.Fatal("users list should contain the authenticated user")
	}
	current = updExecAll(t, current, press("enter"))
	currentView := ansiRE.ReplaceAllString(current.View(), "")
	if !strings.Contains(currentView, "PROJECTS 2") || countEntryKind(current, entProject) != 2 {
		t.Fatalf("an unprivileged current user should list token-accessible projects:\n%s", currentView)
	}
	if !strings.Contains(currentView, "EFFECTIVE ROLE ASSIGNMENTS 2") ||
		!strings.Contains(currentView, "◆ role:admin · on project:alpha") {
		t.Fatalf("an unprivileged current user should show effective active-token roles:\n%s", currentView)
	}

	// The same token roles are visible from the active project's target side.
	activeProject := start(t, switchCapability{CanSwitch: true})
	activeProject.backend.(*fakeBackend).assignmentErr = osclient.ErrAdminRequired
	activeProject = updExec(t, activeProject, press("A"))
	activeProject = updExec(t, activeProject, press("2")) // projects
	if pi, ok := activeProject.selectLabel("project:alpha"); ok {
		activeProject.cursor = pi
	}
	activeProject = updExecAll(t, activeProject, press("enter"))
	scopeView := ansiRE.ReplaceAllString(activeProject.View(), "")
	if !strings.Contains(scopeView, "EFFECTIVE ROLE ASSIGNMENTS 2") ||
		!strings.Contains(scopeView, "◆ user:admin · as role:member") {
		t.Fatalf("the active project should show effective active-token roles:\n%s", scopeView)
	}

	// Some Keystone policies conceal assignments with HTTP 200 + an empty list
	// rather than 403. The token fallback must cover that response as well.
	emptyProject := start(t, switchCapability{CanSwitch: true})
	emptyProject.backend.(*fakeBackend).projectAssignments = map[string][]osclient.RoleAssignment{"p1": nil}
	emptyProject = updExec(t, emptyProject, press("A"))
	emptyProject = updExec(t, emptyProject, press("2"))
	if pi, ok := emptyProject.selectLabel("project:alpha"); ok {
		emptyProject.cursor = pi
	}
	emptyProject = updExecAll(t, emptyProject, press("enter"))
	emptyProjectView := ansiRE.ReplaceAllString(emptyProject.View(), "")
	if !strings.Contains(emptyProjectView, "EFFECTIVE ROLE ASSIGNMENTS 2") {
		t.Fatalf("an empty concealed assignment list should fall back to active-token roles:\n%s", emptyProjectView)
	}
}

// Service/system accounts are visually distinguished in the users list (a marker
// on a dimmed row), can be filtered by the word "service", and are labelled in
// the detail. Detection itself is the backend's job (tested there); the fake sets
// the flag directly.
func TestServiceAccountsFlagged(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).users = []osclient.User{
		{ID: "u-1", Name: "admin", DomainID: "default", DomainName: "Default", Enabled: true, Description: "cloud administrator"},
		{ID: "u-9", Name: "glance", DomainID: "default", DomainName: "Default", Enabled: true, Description: "image store", Service: true},
	}
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users list

	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "⚙ glance") {
		t.Fatalf("a service account should carry a marker in the list:\n%s", view)
	}
	if n := strings.Count(view, "⚙"); n != 1 {
		t.Fatalf("only the service account should be marked; found %d markers:\n%s", n, view)
	}

	// Filtering by "service" narrows to the service account (the tag rides in the
	// row's filter text; the human account's description has no such word).
	m.filter.SetValue("service")
	m.applyFilters()
	if got := countEntryKind(m, entUser); got != 1 {
		t.Fatalf("filtering by \"service\" should leave only the service account; got %d", got)
	}
	m.filter.SetValue("")
	m.applyFilters()

	// The detail view labels it explicitly.
	if idx, ok := m.selectLabel("user:glance"); ok {
		m.cursor = idx
	} else {
		t.Fatal("users list should contain glance")
	}
	m = updExecAll(t, m, press("enter"))
	if dv := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(dv, "service account") {
		t.Fatalf("a service account's detail should be labelled 'service account':\n%s", dv)
	}
}

// Identity details show every expected related section even when a category is
// empty (e.g. a group with no members shows "USERS 0").
func TestIdentityEmptySectionHeadersShown(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("3")) // groups
	// The fake's "operators" group (g-2) has no members.
	idx, ok := m.selectLabel("group:operators")
	if !ok {
		t.Fatal("groups list should contain operators")
	}
	m.cursor = idx
	m = updExecAll(t, m, press("enter"))
	if !m.isGroupOverview() {
		t.Fatalf("enter on a group should open its overview; loc=%+v", m.loc)
	}
	if got := countEntryKind(m, entUser); got != 0 {
		t.Fatalf("operators group should have no members; got %d", got)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "USERS 0") {
		t.Fatalf("a memberless group should still show the USERS 0 header:\n%s", view)
	}
	if !strings.Contains(view, "DOMAIN 1") {
		t.Fatalf("group overview should still show its domain section:\n%s", view)
	}
}

// A domain lists its projects, groups, and users as related objects, loaded
// lazily; opening one drills into that object's detail.
func TestDomainRelatedObjects(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("1")) // domains
	if idx, ok := m.selectLabel("domain:Default"); ok {
		m.cursor = idx
	}
	m = updExecAll(t, m, press("enter")) // open domain; loads contents + assignments
	if !m.isDomainOverview() {
		t.Fatalf("enter on a domain should open its overview; loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"DOMAIN DETAILS", "PROJECTS 2", "GROUPS 2", "USERS 3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("domain overview should show %q:\n%s", want, view)
		}
	}
	// Related rows drop the "type:" prefix (the group heading names the type).
	if strings.Contains(view, "project:alpha") {
		t.Fatalf("related rows should not carry the type: prefix:\n%s", view)
	}
	if !strings.Contains(view, "alpha · payments · Default") {
		t.Fatalf("related project attributes should be separated clearly:\n%s", view)
	}
	if got := countEntryKind(m, entProject); got != 2 {
		t.Fatalf("domain should list 2 projects; got %d", got)
	}
	if got := countEntryKind(m, entUserGroup); got != 2 {
		t.Fatalf("domain should list 2 groups; got %d", got)
	}
	if got := countEntryKind(m, entUser); got != 3 {
		t.Fatalf("domain should list 3 users; got %d", got)
	}

	// Drill into one of the domain's projects → its detail.
	if idx, ok := m.selectLabel("project:alpha"); ok {
		m.cursor = idx
	}
	m = updExec(t, m, press("enter"))
	if !m.isProjectOverview() {
		t.Fatalf("opening a domain's project should show its detail; loc=%+v", m.loc)
	}
}

// User, group, and project details each list their domain as a related object,
// resolvable even when the domains list was never opened (a bare reference).
func TestIdentityObjectsShowDomainRelated(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("2")) // projects
	if idx, ok := m.selectLabel("project:alpha"); ok {
		m.cursor = idx
	}
	m = updExec(t, m, press("enter"))
	if !m.isProjectOverview() {
		t.Fatalf("enter on a project should open its overview; loc=%+v", m.loc)
	}
	if got := countEntryKind(m, entDomain); got != 1 {
		t.Fatalf("project should show its domain as a related object; got %d", got)
	}

	// Open the bare domain reference → the domain detail resolves from the cache.
	if idx, ok := m.selectLabel("domain:Default"); ok {
		m.cursor = idx
	}
	m = updExec(t, m, press("enter"))
	if !m.isDomainOverview() {
		t.Fatalf("opening a project's domain ref should show the domain; loc=%+v", m.loc)
	}
}

// A user's groups load lazily as related objects (the inverse of group
// membership); opening one drills into that group, whose members then load.
func TestUserGroupsAsRelatedObjects(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users
	idx, ok := m.selectLabel("user:admin")
	if !ok {
		t.Fatal("users list should contain admin")
	}
	m.cursor = idx
	m = updExecAll(t, m, press("enter")) // open user; loads groups + assignments
	if !m.isUserOverview() {
		t.Fatalf("enter on a user should open its overview; loc=%+v", m.loc)
	}
	if got := countEntryKind(m, entUserGroup); got != 1 {
		t.Fatalf("admin should list its 1 group as a related object; got %d", got)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "GROUPS 1") {
		t.Fatalf("user overview should show a GROUPS section:\n%s", view)
	}
	// The user's domain is the first related object.
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "DOMAIN 1") {
		t.Fatalf("user overview should show its domain as a related object:\n%s", view)
	}
	if got := countEntryKind(m, entDomain); got != 1 {
		t.Fatalf("user overview should list its domain; got %d", got)
	}

	// Drill into the group → its detail, whose members load in turn.
	gi, ok := m.selectLabel("group:admins")
	if !ok {
		t.Fatal("user groups should include admins")
	}
	m.cursor = gi
	m = updExec(t, m, press("enter"))
	if !m.isGroupOverview() {
		t.Fatalf("opening a group from a user should show the group detail; loc=%+v", m.loc)
	}

	// RBAC denial on a user's groups is recorded per user and degrades to empty.
	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).userGroupsErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("4")) // users
	if di, ok := denied.selectLabel("user:admin"); ok {
		denied.cursor = di
	}
	denied = updExecAll(t, denied, press("enter"))
	if denied.userGroupsErr["u-1"] == "" || countEntryKind(denied, entUserGroup) != 0 {
		t.Fatalf("RBAC denial should record a per-user error and list no groups; err=%q groups=%d",
			denied.userGroupsErr["u-1"], countEntryKind(denied, entUserGroup))
	}
}

// countEntryKind counts the visible rows of a given entry kind.
func countEntryKind(m Model, k entryKind) int {
	n := 0
	for _, e := range m.entries {
		if e.kind == k {
			n++
		}
	}
	return n
}

// A group's member users load lazily as related objects; opening one drills into
// that user's detail, and an RBAC denial is recorded per group.
func TestGroupMembersAsRelatedObjects(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("3"))
	idx, ok := m.selectLabel("group:admins")
	if !ok {
		t.Fatal("groups list should contain admins")
	}
	m.cursor = idx
	m = updExecAll(t, m, press("enter")) // open group; loads members + assignments
	if !m.isGroupOverview() {
		t.Fatalf("enter on a group should open its overview; loc=%+v", m.loc)
	}
	if got := countEntryKind(m, entUser); got != 2 {
		t.Fatalf("group admins should list its 2 members as related objects; got %d", got)
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "USERS 2") {
		t.Fatalf("group overview should show its members under a USERS section:\n%s", view)
	}
	// The group's domain is the first related object.
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "DOMAIN 1") {
		t.Fatalf("group overview should show its domain as a related object:\n%s", view)
	}
	if got := countEntryKind(m, entDomain); got != 1 {
		t.Fatalf("group overview should list its domain; got %d", got)
	}

	// Drill into a member → that user's detail.
	mi, ok := m.selectLabel("user:alice")
	if !ok {
		t.Fatal("members should include alice")
	}
	m.cursor = mi
	m = updExec(t, m, press("enter"))
	if !m.isUserOverview() {
		t.Fatalf("opening a member should show the user detail; loc=%+v", m.loc)
	}

	// RBAC denial on members is recorded per group and degrades to an empty list.
	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).groupMemberEr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("A"))
	denied = updExec(t, denied, press("3"))
	if di, ok := denied.selectLabel("group:admins"); ok {
		denied.cursor = di
	}
	denied = updExecAll(t, denied, press("enter"))
	if denied.groupMembersErr["g-1"] == "" || countEntryKind(denied, entUser) != 0 {
		t.Fatalf("RBAC denial should record a per-group error and list no members; err=%q members=%d",
			denied.groupMembersErr["g-1"], countEntryKind(denied, entUser))
	}
}

// Opening a user reparents to its detail overview, sourced from the list data
// (identity objects have no load-balancer tree).
func TestUserDetailOpens(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users
	idx, ok := m.selectLabel("user:admin")
	if !ok {
		t.Fatal("users list should contain admin")
	}
	m.cursor = idx
	m = updExec(t, m, press("enter"))
	if !m.isUserOverview() {
		t.Fatalf("enter on a user should open its detail overview; loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "USER DETAILS") || !strings.Contains(view, "admin@example.com") {
		t.Fatalf("user overview should show details and the email:\n%s", view)
	}
	// Domain and default-project names resolve through the shared name maps.
	for _, want := range []string{"IDENTITY", "DOMAIN", "DEFAULT PROJECT", "alpha", "Default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("user overview should show %q:\n%s", want, view)
		}
	}
}
