package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/osclient"
)

// The main list shows "▲ more"/"▼ more" scroll hints when it overflows the
// visible region, and neither when it fits.
func TestMainListScrollMarkers(t *testing.T) {
	fb := &fakeBackend{cap: switchCapability{CanSwitch: true}}
	for i := 0; i < 30; i++ {
		fb.users = append(fb.users, osclient.User{
			ID: fmt.Sprintf("u-%02d", i), Name: fmt.Sprintf("user%02d", i),
			DomainID: "default", DomainName: "Default", Enabled: true,
		})
	}
	m := New(fb, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 90, Height: 14})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users table, 30 rows > visible window

	// At the top: only the "below" marker.
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "▼ more") || strings.Contains(view, "▲ more") {
		t.Fatalf("top of a long list should show only the ▼ marker:\n%s", view)
	}

	// Scroll to the bottom: only the "above" marker.
	for i := 0; i < 35; i++ {
		m = upd(t, m, press("down"))
	}
	if view := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(view, "▲ more") || strings.Contains(view, "▼ more") {
		t.Fatalf("bottom of a long list should show only the ▲ marker:\n%s", view)
	}
}

// Refresh keeps the selected object at the same visual row instead of rebuilding
// from top=0 and merely making a far-down cursor visible at the bottom edge.
func TestMainListRefreshPreservesSelectedRowOffset(t *testing.T) {
	fb := &fakeBackend{cap: switchCapability{CanSwitch: true}}
	for i := 0; i < 30; i++ {
		fb.users = append(fb.users, osclient.User{
			ID: fmt.Sprintf("u-%02d", i), Name: fmt.Sprintf("user%02d", i),
			DomainID: "default", DomainName: "Default", Enabled: true,
		})
	}
	m := New(fb, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 90, Height: 14})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("4")) // users table

	for i := 0; i < 35; i++ {
		m = upd(t, m, press("down"))
	}
	for i := 0; i < 5; i++ {
		m = upd(t, m, press("up"))
	}
	if m.top == 0 {
		t.Fatal("setup: users list did not scroll")
	}
	beforeOffset := m.cursor - m.top
	beforeTop := m.top
	beforeSelection := m.entries[m.cursor].selection()

	m = updExec(t, m, press("r"))

	if !m.entries[m.cursor].selection().equal(beforeSelection) {
		t.Fatalf("refresh changed selection: got %+v, want %+v", m.entries[m.cursor].selection(), beforeSelection)
	}
	if got := m.cursor - m.top; got != beforeOffset {
		t.Fatalf("selected row offset after refresh = %d, want %d (cursor=%d top=%d)", got, beforeOffset, m.cursor, m.top)
	}
	if m.top != beforeTop {
		t.Fatalf("unchanged list top after refresh = %d, want %d", m.top, beforeTop)
	}
}

// An identity overview's group heading stays pinned when its related list is
// scrolled past that heading, so the visible rows are always labelled.
func TestIdentityOverviewStickyHeading(t *testing.T) {
	fb := &fakeBackend{cap: switchCapability{CanSwitch: true}}
	for i := 0; i < 15; i++ {
		fb.projectList = append(fb.projectList, osclient.Project{
			ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("proj%02d", i),
			DomainID: "default", DomainName: "Default", Enabled: true,
		})
	}
	m := New(fb, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 22})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("1")) // domains
	if idx, ok := m.selectLabel("domain:Default"); ok {
		m.cursor = idx
	}
	m = updExecAll(t, m, press("enter"))

	// Scroll well past the PROJECTS heading (index 0).
	for i := 0; i < 12; i++ {
		m = upd(t, m, press("down"))
	}
	if m.top == 0 {
		t.Fatalf("setup: expected the list to have scrolled; top=%d", m.top)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	// The scrolled-away PROJECTS heading is still shown (pinned), above rows whose
	// natural heading is off-screen.
	if !strings.Contains(view, "PROJECTS 15") {
		t.Fatalf("PROJECTS heading should stay pinned while scrolled:\n%s", view)
	}
	if !strings.Contains(view, "proj12") {
		t.Fatalf("the selected row should be visible while scrolled:\n%s", view)
	}
}

func TestGroupedRefreshPreservesSelectedRowOffsetAfterInsertion(t *testing.T) {
	fb := &fakeBackend{cap: switchCapability{CanSwitch: true}}
	for i := 0; i < 15; i++ {
		fb.projectList = append(fb.projectList, osclient.Project{
			ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("proj%02d", i),
			DomainID: "default", DomainName: "Default", Enabled: true,
		})
	}
	m := New(fb, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 22})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("1"))
	if idx, ok := m.selectLabel("domain:Default"); ok {
		m.cursor = idx
	}
	m = updExecAll(t, m, press("enter"))
	for i := 0; i < 12; i++ {
		m = upd(t, m, press("down"))
	}

	beforeOffset := m.cursor - m.top
	beforeSelection := m.entries[m.cursor].selection()
	m.captureRefreshSelection()

	// Simulate refreshed data inserting an item before the selected object, then
	// the normal grouped-list reconstruction that resets cursor and top.
	domainID := m.loc.node.ID
	content := m.domainContents[domainID]
	content.projects = append([]osclient.Project{{
		ID: "p-new", Name: "proj-new", DomainID: domainID, DomainName: "Default", Enabled: true,
	}}, content.projects...)
	m.domainContents[domainID] = content
	m.allEntries = m.domainRelatedEntries(m.loc.node)
	m.entries = nil
	m.cursor, m.top = 0, 0
	m.applyFilters()
	m.restoreRefreshSelection()

	if !m.entries[m.cursor].selection().equal(beforeSelection) {
		t.Fatalf("grouped refresh changed selection: got %+v, want %+v", m.entries[m.cursor].selection(), beforeSelection)
	}
	if got := m.cursor - m.top; got != beforeOffset {
		t.Fatalf("grouped refresh row offset = %d, want %d (cursor=%d top=%d)", got, beforeOffset, m.cursor, m.top)
	}
}

// A detail overview's related sub-panel shows scroll hints next to the list (not
// on the subtitle line at the top of the screen), the "▲ more" hint clears when
// scrolled back to the top even past a pinned heading, and on a wide screen the
// hint is mirrored on both edges.
func TestRelatedListScrollMarkers(t *testing.T) {
	fb := &fakeBackend{cap: switchCapability{CanSwitch: true}}
	for i := 0; i < 15; i++ {
		fb.projectList = append(fb.projectList, osclient.Project{
			ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("proj%02d", i),
			DomainID: "default", DomainName: "Default", Enabled: true,
		})
	}
	m := New(fb, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = upd(t, m, lbsMsg{lbs: mustLBs(t, m)})
	m = updExec(t, m, press("A"))
	m = updExec(t, m, press("1")) // domains
	if idx, ok := m.selectLabel("domain:Default"); ok {
		m.cursor = idx
	}
	m = updExecAll(t, m, press("enter")) // related list overflows the sub-panel

	// At the top: a "▼ more" hint but no "▲ more" (nothing is hidden above).
	top := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(top, "▼ more") {
		t.Fatalf("an overflowing related list should show a ▼ hint at the top:\n%s", top)
	}
	if strings.Contains(top, "▲ more") {
		t.Fatalf("nothing is hidden above at the top, so no ▲ hint:\n%s", top)
	}

	// Scroll down: the ▲ hint appears, mirrored on both edges on this wide screen.
	for i := 0; i < 25; i++ {
		m = upd(t, m, press("down"))
	}
	scrolled := ansiRE.ReplaceAllString(m.View(), "")
	if n := strings.Count(scrolled, "▲ more"); n != 2 {
		t.Fatalf("on a wide screen the ▲ hint should be mirrored on both edges (2); got %d:\n%s", n, scrolled)
	}
	// Placement: the ▲ hint sits in the related panel, on a line below the subtitle
	// ("scope:") line — not on the subtitle line itself.
	viewLines := strings.Split(scrolled, "\n")
	subIdx, markIdx := -1, -1
	for i, ln := range viewLines {
		if strings.Contains(ln, "scope:") {
			subIdx = i
		}
		if strings.Contains(ln, "▲ more") && markIdx == -1 {
			markIdx = i
		}
	}
	if subIdx < 0 || markIdx <= subIdx {
		t.Fatalf("the ▲ hint should sit below the subtitle line, near the list (sub=%d mark=%d):\n%s", subIdx, markIdx, scrolled)
	}

	// Scroll back to the top: the ▲ hint clears (the reported bug).
	for i := 0; i < 30; i++ {
		m = upd(t, m, press("up"))
	}
	if back := ansiRE.ReplaceAllString(m.View(), ""); strings.Contains(back, "▲ more") {
		t.Fatalf("scrolled back to the top, the ▲ hint should clear:\n%s", back)
	}
}

// A list that fits shows no scroll markers.
func TestMainListNoScrollMarkersWhenFits(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true}) // two load balancers
	if view := ansiRE.ReplaceAllString(m.View(), ""); strings.Contains(view, "▲ more") || strings.Contains(view, "▼ more") {
		t.Fatalf("a list that fits should show no scroll markers:\n%s", view)
	}
}
