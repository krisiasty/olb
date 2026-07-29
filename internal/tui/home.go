package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/osclient"
)

// homeView renders the launch / overview landing. Now that olb browses more than
// load balancers, it opens here to orient the operator: the current scope and
// identity (from the auth token — a local read, no network), then the browsable
// areas. It is a base view (not an overlay, not a workspace); an area accelerator
// enters a workspace, space opens the switcher, and ` returns here.
func (m Model) homeView() string {
	tok := m.backend.CurrentToken()
	lines := make([]string, 0, m.height)
	lines = append(lines, m.clip(m.st.title.Render("OLB — OpenStack Live Browser")))
	lines = append(lines, "")

	// Scope + whoami panel.
	rows := [][2]string{{"scope", m.scopeText()}}
	if tok.Available {
		you := displayValue(tok.UserName)
		if tok.UserDomain != "" {
			you += " @" + tok.UserDomain
		}
		rows = append(rows, [2]string{"you", you})
		if len(tok.Roles) > 0 {
			rows = append(rows, [2]string{"roles", strings.Join(tok.Roles, ", ")})
		}
		rows = append(rows, [2]string{"token", m.tokenExpiryLine(tok)})
	}
	labelW := 0
	for _, r := range rows {
		if w := lipgloss.Width(r[0]); w > labelW {
			labelW = w
		}
	}
	for _, r := range rows {
		lines = append(lines, m.clip("  "+m.st.panelLabel.Render(padRight(r[0], labelW))+"   "+r[1]))
	}
	lines = append(lines, "")

	// Areas — in registry (switcher) order, each with its accelerator and count.
	lines = append(lines, m.clip(m.st.groupHeading.Render("BROWSE")), "")
	nameW := 0
	for _, a := range areas {
		if w := lipgloss.Width(a.label); w > nameW {
			nameW = w
		}
	}
	for _, a := range areas {
		chip := m.st.panelTitle.Render(fmt.Sprintf(" %s ", string(a.key)))
		count := m.st.attrs.Render(fmt.Sprintf("%d views", len(a.views)))
		lines = append(lines, m.clip("  "+chip+"  "+padRight(a.label, nameW)+"   "+count))
	}

	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.clip(m.st.help.Render(strings.Join(areaKeyStrings(), " / ")+" enter area · space switch · tab scope · * token · ? help · q quit")))
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// scopeText is the plain-text summary of the active Keystone token scope.
func (m Model) scopeText() string {
	switch m.scope.Kind {
	case osclient.ScopeSystem:
		return "system:" + m.scope.Label()
	case osclient.ScopeDomain:
		return "domain " + m.scope.Label()
	case osclient.ScopeProject:
		label := "project "
		if m.scope.DomainName != "" {
			label += m.scope.DomainName + " / "
		}
		return label + m.scope.Label()
	default:
		return "unscoped"
	}
}

// onHomeKey handles keys while on the overview landing: area accelerators enter
// an area, and the global overlays/quit still work; everything else is inert.
func (m Model) onHomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Area):
		return m.goArea(msg.String())
	case key.Matches(msg, m.keys.Switcher):
		return m.openSwitcher()
	case key.Matches(msg, m.keys.Scope):
		return m.openScope()
	case key.Matches(msg, m.keys.Token):
		return m.openToken()
	case key.Matches(msg, m.keys.Telemetry):
		return m.openTelemetry()
	case key.Matches(msg, m.keys.Help):
		m.overlay = overlayHelp
		m.setupHelpViewport()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}
