package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

func TestRoleTreeExpandsSharedRolesOnEveryPath(t *testing.T) {
	graph := map[string][]osclient.Role{
		"admin": {
			{ID: "manager", Name: "manager"},
			{ID: "support", Name: "support"},
		},
		"manager": {{ID: "member", Name: "member"}},
		"support": {{ID: "member", Name: "member"}},
		"member":  {{ID: "reader", Name: "reader"}},
	}
	backend := &fakeBackend{impliedRoles: graph}
	m := New(backend, Config{})
	m.width, m.height = 100, 14
	m.home = false
	m.activeWorkspace = kindRole
	m.loc = location{id: model.RoleListIdentity}
	root := osclient.Role{ID: "admin", Name: "admin", ImpliesRoles: true}
	m.allEntries = roleEntries([]osclient.Role{root})
	m.entries = m.allEntries

	m = updExec(t, m, press("t"))
	if m.overlay != overlayRoleTree {
		t.Fatalf("t did not open role tree overlay: %v", m.overlay)
	}
	areaInnerWidth := lipgloss.Width(m.switcherModalBox()) - m.st.modalFrame.GetHorizontalFrameSize()
	if m.vp.Width < areaInnerWidth {
		t.Fatalf("role tree inner width = %d, want at least area switcher width %d", m.vp.Width, areaInnerWidth)
	}
	content := ansiRE.ReplaceAllString(m.roleTreeContent(), "")
	if got := strings.Count(content, "member"); got != 2 {
		t.Fatalf("shared member role rendered %d times, want 2:\n%s", got, content)
	}
	if got := strings.Count(content, "reader"); got != 2 {
		t.Fatalf("shared member descendants rendered %d times, want 2:\n%s", got, content)
	}
	plain := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(plain, "╭") || !strings.Contains(plain, "╯") {
		t.Fatalf("role tree is not rendered in a framed modal:\n%s", plain)
	}
	var border string
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "╭") {
			border = line
			break
		}
	}
	if at := strings.Index(border, "╭"); at < 1 {
		t.Fatalf("role tree modal is not centered over the underlying view:\n%s", plain)
	}
	boxPlain := ansiRE.ReplaceAllString(m.roleTreeModalBox(), "")
	header := lineContaining(boxPlain, "ROLE INHERITANCE")
	if strings.Contains(header, "admin") {
		t.Fatalf("role tree header repeats the root role name: %q", header)
	}
	lines := strings.Split(boxPlain, "\n")
	for i, line := range lines {
		if strings.Contains(line, "ROLE INHERITANCE") {
			if i+1 >= len(lines) || strings.Trim(lines[i+1], " │") != "" {
				t.Fatalf("role tree header has no fixed empty separator line:\n%s", boxPlain)
			}
			break
		}
	}
	footerFound := false
	for i, line := range lines {
		if strings.Contains(line, "↑/↓") {
			footerFound = true
			if i == 0 || strings.Trim(lines[i-1], " │") != "" {
				t.Fatalf("role tree footer has no empty separator line:\n%s", boxPlain)
			}
			break
		}
	}
	if !footerFound {
		t.Fatalf("role tree footer is missing:\n%s", boxPlain)
	}
	if !strings.Contains(plain, "▼ more") {
		t.Fatalf("scrollable tree has no bottom more indicator:\n%s", plain)
	}
	m.vp.GotoBottom()
	if plain = ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(plain, "▲ more") {
		t.Fatalf("scrolled tree has no top more indicator:\n%s", plain)
	}
	if !strings.Contains(plain, "esc/t/q close") || !strings.Contains(plain, "100%") {
		t.Fatalf("scroll status overlaps or clips the complete hotkey legend:\n%s", plain)
	}
	m = upd(t, m, press("t"))
	if m.overlay != overlayNone {
		t.Fatal("t did not close role tree overlay")
	}
}

func TestRoleTreeDepthLimitMarksExpandableNode(t *testing.T) {
	graph := make(map[string][]osclient.Role)
	for level := 1; level <= roleTreeMaxDepth; level++ {
		parent := fmt.Sprintf("r-%d", level)
		childLevel := level + 1
		graph[parent] = []osclient.Role{{
			ID:   fmt.Sprintf("r-%d", childLevel),
			Name: fmt.Sprintf("role-%d", childLevel),
		}}
	}
	m := New(&fakeBackend{}, Config{})
	m.roleTreeRoot = osclient.Role{ID: "r-1", Name: "role-1"}
	m.roleInferences = graph

	plain := ansiRE.ReplaceAllString(m.roleTreeContent(), "")
	if !strings.Contains(plain, "WARNING · depth limit 10 reached") {
		t.Fatalf("depth warning missing:\n%s", plain)
	}
	if !strings.Contains(plain, "role-10 (...)") {
		t.Fatalf("deep expandable node is not marked:\n%s", plain)
	}
	if strings.Contains(plain, "role-11") {
		t.Fatalf("tree expanded beyond the configured depth:\n%s", plain)
	}
}

func TestRoleTreeDetectsCyclesButExpandsSharedRoles(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.roleTreeRoot = osclient.Role{ID: "admin", Name: "admin"}
	m.roleInferences = map[string][]osclient.Role{
		"admin":  {{ID: "member", Name: "member"}},
		"member": {{ID: "admin", Name: "admin"}},
	}

	plain := ansiRE.ReplaceAllString(m.roleTreeContent(), "")
	if !strings.Contains(plain, "admin (↻ cycle)") ||
		!strings.Contains(plain, "WARNING · cyclic inference detected") {
		t.Fatalf("cycle was not stopped and explained:\n%s", plain)
	}
}

func TestRoleTreeIsAvailableFromRoleDetails(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	role := osclient.Role{ID: "admin", Name: "admin"}
	m.knownRoles[role.ID] = role
	m.loc = location{node: roleToNode(role)}
	m.roleRelationsLoaded[role.ID] = true
	m.roleRelations[role.ID] = roleRelations{
		implied: []osclient.Role{{ID: "member", Name: "member"}},
	}

	if !m.canOpenRoleTree() {
		t.Fatal("role details with implied roles did not expose the tree action")
	}
}
