package tui

import (
	"strings"
	"testing"

	"github.com/krisiasty/olb/internal/osclient"
)

func firstLabels(m Model) []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.label)
	}
	return out
}

func TestSortOverlaySortsByNameAscending(t *testing.T) {
	m := lbListModel(t, true) // rows arrive as web-prod, db-lb (API order)
	if got := firstLabels(m); got[0] != "lb:web-prod" {
		t.Fatalf("expected default (API) order first, got %v", got)
	}

	m = upd(t, m, press("o"))
	if m.overlay != overlaySort {
		t.Fatalf("pressing o should open the sort overlay, got overlay %d", m.overlay)
	}
	// index 0 is "default order"; one Down highlights "Name".
	m = upd(t, m, press("down"))
	m = upd(t, m, press("enter"))

	if m.overlay != overlayNone {
		t.Fatalf("enter should close the sort overlay")
	}
	if m.sortKey != "name" {
		t.Fatalf("sortKey = %q, want name", m.sortKey)
	}
	if got := firstLabels(m); got[0] != "lb:db-lb" || got[1] != "lb:web-prod" {
		t.Fatalf("expected ascending-by-name order [db-lb, web-prod], got %v", got)
	}
}

func TestSortOverlayEscCancels(t *testing.T) {
	m := lbListModel(t, true)
	before := firstLabels(m)

	m = upd(t, m, press("o"))
	m = upd(t, m, press("down")) // move highlight but do not commit
	m = upd(t, m, press("esc"))

	if m.overlay != overlayNone {
		t.Fatalf("esc should close the sort overlay")
	}
	if m.sortKey != "" {
		t.Fatalf("esc must not change the sort; sortKey = %q", m.sortKey)
	}
	if got := firstLabels(m); got[0] != before[0] || got[1] != before[1] {
		t.Fatalf("esc must not reorder the list; got %v want %v", got, before)
	}
}

func TestSortDefaultOrderEntryClearsSort(t *testing.T) {
	m := lbListModel(t, true)

	// Sort by name first.
	m = upd(t, m, press("o"))
	m = upd(t, m, press("down"))
	m = upd(t, m, press("enter"))
	if got := firstLabels(m); got[0] != "lb:db-lb" {
		t.Fatalf("precondition: expected name sort, got %v", got)
	}

	// Reopening pre-selects the active column ("Name"); Up returns to "default order".
	m = upd(t, m, press("o"))
	if m.sortCursor != 1 {
		t.Fatalf("reopening should pre-select the active column (index 1), got %d", m.sortCursor)
	}
	m = upd(t, m, press("up"))
	m = upd(t, m, press("enter"))

	if m.sortKey != "" {
		t.Fatalf("default-order entry should clear the sort; sortKey = %q", m.sortKey)
	}
	if got := firstLabels(m); got[0] != "lb:web-prod" || got[1] != "lb:db-lb" {
		t.Fatalf("default order should restore API order [web-prod, db-lb], got %v", got)
	}
}

func TestSortByIPIsNumericAndPutsEmptyLast(t *testing.T) {
	m := lbListModel(t, false)
	// Addresses chosen so lexical and numeric order disagree, plus one internal
	// LB with no VIP (sorts last).
	m.lbs = []osclient.LB{
		{ID: "l1", Name: "b", VipAddress: "10.0.0.10"},
		{ID: "l2", Name: "c", VipAddress: ""},
		{ID: "l3", Name: "a", VipAddress: "10.0.0.9"},
	}
	(&m).setTopLevelEntries()

	m = upd(t, m, press("o"))
	// Non-all-projects LB columns: default, Name, id, VIP → VIP is the last row.
	m = upd(t, m, press("end"))
	m = upd(t, m, press("enter"))

	if m.sortKey != "vip" {
		t.Fatalf("sortKey = %q, want vip", m.sortKey)
	}
	got := firstLabels(m)
	want := []string{"lb:a", "lb:b", "lb:c"} // 10.0.0.9 < 10.0.0.10 < (empty)
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("numeric IP sort with empty-last failed; got %v want %v", got, want)
	}
}

func TestIPLess(t *testing.T) {
	if !ipLess("10.0.0.9", "10.0.0.10") {
		t.Errorf("10.0.0.9 should sort before 10.0.0.10 (numeric)")
	}
	if !ipLess("192.168.0.1", "") {
		t.Errorf("a real address should sort before an empty one")
	}
	if ipLess("", "192.168.0.1") {
		t.Errorf("empty should not sort before a real address")
	}
}

func TestSortViewRendersCenteredModalOverList(t *testing.T) {
	m := lbListModel(t, false)
	m = upd(t, m, press("o"))
	view := ansiRE.ReplaceAllString(m.View(), "")

	for _, want := range []string{"Sort ·", "default order", "Name", "esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("modal missing %q; view:\n%s", want, view)
		}
	}
	// Rounded border corners mean a boxed pop-up rather than a full-screen list.
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Errorf("expected a bordered modal box; view:\n%s", view)
	}
	// The list stays visible behind the pop-up (its breadcrumb on the top row).
	if !strings.Contains(view, "load balancers") {
		t.Errorf("expected the list visible behind the modal; view:\n%s", view)
	}
	// The box is horizontally centered: its top border is indented, not at col 0.
	var border string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "╭") {
			border = l
			break
		}
	}
	if idx := strings.Index(border, "╭"); idx < 4 {
		t.Errorf("modal top border not indented/centered (╭ at col %d): %q", idx, border)
	}
}

func TestOverlayCenter(t *testing.T) {
	base := strings.Join([]string{"xxxxxxxx", "xxxxxxxx", "xxxxxxxx", "xxxxxxxx", "xxxxxxxx"}, "\n") // 8×5
	out := overlayCenter(base, "BB\nBB", 8, 5)
	lines := strings.Split(out, "\n")
	clean := func(s string) string { return strings.ReplaceAll(s, "\x1b[0m", "") }

	if len(lines) != 5 {
		t.Fatalf("want 5 lines, got %d", len(lines))
	}
	// box 2×2 centered in 8×5: top=1, left=3 → rows 1,2 get "xxxBBxxx".
	if clean(lines[0]) != "xxxxxxxx" || clean(lines[4]) != "xxxxxxxx" {
		t.Errorf("rows outside the box should be untouched: %q / %q", lines[0], lines[4])
	}
	if clean(lines[1]) != "xxxBBxxx" || clean(lines[2]) != "xxxBBxxx" {
		t.Errorf("box not centered over base: %q / %q", clean(lines[1]), clean(lines[2]))
	}
}

func TestSortHintShownOnTopLevelList(t *testing.T) {
	m := lbListModel(t, false)
	if !strings.Contains(m.hintLine(), "o sort") {
		t.Errorf("top-level list hint line should advertise o sort: %q", m.hintLine())
	}
}
