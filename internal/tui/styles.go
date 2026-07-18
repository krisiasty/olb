package tui

import "github.com/charmbracelet/lipgloss"

// styles centralizes the Lip Gloss styling: status color-coding, the breadcrumb
// bar, reference/back-reference markers, and overlay chrome.
type styles struct {
	breadcrumb    lipgloss.Style
	crumbSep      lipgloss.Style
	title         lipgloss.Style
	selected      lipgloss.Style
	dimSelected   lipgloss.Style
	normal        lipgloss.Style
	refMarker     lipgloss.Style
	backRefMarker lipgloss.Style
	relationship  lipgloss.Style
	attrs         lipgloss.Style
	statusBar     lipgloss.Style
	flash         lipgloss.Style
	flashErr      lipgloss.Style
	help          lipgloss.Style
	helpKey       lipgloss.Style
	overlay       lipgloss.Style
	overlayTitle  lipgloss.Style
	disabled      lipgloss.Style
	dead          lipgloss.Style
	panelLabel    lipgloss.Style
}

func newStyles() styles {
	subtle := lipgloss.Color("240")
	return styles{
		breadcrumb:    lipgloss.NewStyle().Bold(true),
		crumbSep:      lipgloss.NewStyle().Foreground(subtle),
		title:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
		selected:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("39")),
		dimSelected:   lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("245")),
		normal:        lipgloss.NewStyle(),
		refMarker:     lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		backRefMarker: lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true),
		relationship:  lipgloss.NewStyle().Foreground(subtle).Italic(true),
		attrs:         lipgloss.NewStyle().Foreground(subtle),
		statusBar:     lipgloss.NewStyle().Foreground(subtle),
		flash:         lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		flashErr:      lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		help:          lipgloss.NewStyle().Foreground(subtle),
		helpKey:       lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		overlay:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		overlayTitle:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
		disabled:      lipgloss.NewStyle().Foreground(subtle).Italic(true),
		dead:          lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Strikethrough(true),
		panelLabel:    lipgloss.NewStyle().Foreground(subtle),
	}
}

// statusColor maps an OpenStack status string to a Lip Gloss color. Operating
// status drives most coloring; provisioning status is used as a fallback and
// for the list's provisioning column.
func statusColor(status string) lipgloss.Color {
	switch status {
	case "ONLINE", "ACTIVE":
		return lipgloss.Color("42") // green
	case "DEGRADED", "DRAINING":
		return lipgloss.Color("214") // amber
	case "ERROR":
		return lipgloss.Color("196") // red
	case "PENDING_CREATE", "PENDING_UPDATE", "PENDING_DELETE":
		return lipgloss.Color("214")
	case "OFFLINE", "NO_MONITOR", "DELETED":
		return lipgloss.Color("244") // grey
	default:
		return lipgloss.Color("252")
	}
}
