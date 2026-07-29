package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func homeModel(t *testing.T) Model {
	t.Helper()
	m := New(&fakeBackend{cap: switchCapability{CanSwitch: true}}, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 90, Height: 20})
	return m
}

// olb opens on the overview landing: it shows the current scope, the
// authenticated identity, and the browsable areas.
func TestHomeLanding(t *testing.T) {
	m := homeModel(t)
	if !m.home {
		t.Fatal("olb should open on the overview landing")
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"OLB — OpenStack Live Browser", "project Default / alpha", "admin, member",
		"service catalog", "identity & access", "compute", "load balancers",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("home view should show %q:\n%s", want, view)
		}
	}
}

func TestHomeBrowseHeading(t *testing.T) {
	m := homeModel(t)
	// BROWSE is a plain section heading, followed by breathing room before the
	// area rows; it must not use the reversed panel-title chip style.
	lines := strings.Split(m.View(), "\n")
	for i, line := range lines {
		if ansiRE.ReplaceAllString(line, "") != "BROWSE" {
			continue
		}
		if line != m.st.groupHeading.Render("BROWSE") {
			t.Fatalf("BROWSE should use the non-reversed group-heading style: %q", line)
		}
		if i+1 >= len(lines) || lines[i+1] != "" {
			t.Fatalf("BROWSE should be followed by an empty line:\n%s", m.View())
		}
		return
	}
	t.Fatalf("home view should contain the BROWSE heading:\n%s", m.View())
}

// An area accelerator enters that area (leaving the landing), and ` returns.
func TestHomeAreaNavigation(t *testing.T) {
	m := homeModel(t)
	m = updExec(t, m, press("A"))
	if m.home || areaOf(m.activeWorkspace) != areaIdentity {
		t.Fatalf("A should enter the identity area and leave home; home=%v active=%v", m.home, m.activeWorkspace)
	}
	m = upd(t, m, press("`"))
	if !m.home {
		t.Fatal("` should return to the overview landing")
	}
	if v := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(v, "BROWSE") {
		t.Fatalf("should be back on the landing:\n%s", v)
	}

	// L from the fresh landing (the load-balancer workspace is already active, so
	// switchWorkspace early-returns) must still clear home and show the list.
	fresh := homeModel(t)
	fresh = updExec(t, fresh, press("L"))
	if fresh.home || areaOf(fresh.activeWorkspace) != areaLB {
		t.Fatalf("L from home should show the load-balancer list; home=%v active=%v", fresh.home, fresh.activeWorkspace)
	}
}

func TestHomeOpensScopeSelector(t *testing.T) {
	m := homeModel(t)
	m = updExec(t, m, press("tab"))
	if m.overlay != overlayScope || len(m.scopes) == 0 {
		t.Fatalf("tab from home should open the scope selector: overlay=%v scopes=%d", m.overlay, len(m.scopes))
	}
}
