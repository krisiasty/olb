package tui

import (
	"fmt"
	"strings"
	"testing"
)

func scrollOverlayLines(m Model) (title, footer string) {
	lines := strings.Split(ansiRE.ReplaceAllString(m.telemetryView(), ""), "\n")
	return strings.TrimRight(lines[0], " "), strings.TrimRight(lines[len(lines)-1], " ")
}

func TestOverlayScrollMarkers(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.width, m.height = 120, 10
	m.overlay = overlayTelemetry
	m.vp.Width, m.vp.Height = m.width, m.height-2
	var b strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	m.vp.SetContent(b.String())

	m.vp.GotoTop()
	title, footer := scrollOverlayLines(m)
	if strings.Contains(title, "▲") {
		t.Errorf("no above-marker expected at top: %q", title)
	}
	if !strings.Contains(footer, "▼ more") || !strings.Contains(footer, "0%") {
		t.Errorf("footer should show below-marker and 0%% at top: %q", footer)
	}

	m.vp.SetYOffset(15)
	title, footer = scrollOverlayLines(m)
	if !strings.Contains(title, "▲ more") {
		t.Errorf("above-marker expected once scrolled: %q", title)
	}
	if !strings.Contains(footer, "▼ more") {
		t.Errorf("below-marker expected mid-scroll: %q", footer)
	}

	m.vp.GotoBottom()
	title, footer = scrollOverlayLines(m)
	if !strings.Contains(title, "▲ more") {
		t.Errorf("above-marker expected at bottom: %q", title)
	}
	if strings.Contains(footer, "▼") || !strings.Contains(footer, "100%") {
		t.Errorf("footer should show 100%% and no below-marker at bottom: %q", footer)
	}
}

func TestOverlayScrollMarkersHiddenWhenContentFits(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.width, m.height = 120, 40
	m.overlay = overlayTelemetry
	m.vp.Width, m.vp.Height = m.width, m.height-2
	m.vp.SetContent("just a couple\nof lines\n")
	m.vp.GotoTop()
	title, footer := scrollOverlayLines(m)
	if strings.Contains(title, "▲") || strings.Contains(footer, "▼") || strings.Contains(footer, "%") {
		t.Errorf("no scroll markers expected when content fits:\ntitle=%q\nfooter=%q", title, footer)
	}
}
