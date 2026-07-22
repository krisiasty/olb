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
	refreshAuto   lipgloss.Style
	refreshManual lipgloss.Style
	refreshPaused lipgloss.Style
	flash         lipgloss.Style
	flashErr      lipgloss.Style
	filterPrompt  lipgloss.Style
	help          lipgloss.Style
	helpKey       lipgloss.Style
	overlay       lipgloss.Style
	overlayTitle  lipgloss.Style
	modalFrame    lipgloss.Style
	modalTitle    lipgloss.Style
	modalRow      lipgloss.Style
	modalHelp     lipgloss.Style
	disabled      lipgloss.Style
	dead          lipgloss.Style
	panelLabel    lipgloss.Style
	groupHeading  lipgloss.Style
	relatedGroup  lipgloss.Style
	panelTitle    lipgloss.Style

	tableHeader   lipgloss.Style
	tableSelected lipgloss.Style
	tableCell     lipgloss.Style
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
		refreshAuto:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")),
		refreshManual: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")),
		refreshPaused: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")),
		flash:         lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		flashErr:      lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		filterPrompt:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")),
		help:          lipgloss.NewStyle().Foreground(subtle),
		helpKey:       lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		overlay:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		overlayTitle:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),

		// Centered pop-up modal (the sort picker): a rounded blue border on the
		// terminal's default background — the frame alone sets it off from the
		// list rendered behind it.
		modalFrame: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).Padding(1, 2),
		modalTitle:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
		modalRow:     lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		modalHelp:    lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		disabled:     lipgloss.NewStyle().Foreground(subtle).Italic(true),
		dead:         lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Strikethrough(true),
		panelLabel:   lipgloss.NewStyle().Foreground(subtle),
		groupHeading: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")),
		// Related-object section headers: left-aligned, slightly highlighted in
		// a dim/muted yellow so single-item sections stay easy to scan.
		relatedGroup: lipgloss.NewStyle().Foreground(lipgloss.Color("254")),
		// Panel headers ("LOAD BALANCER DETAILS", "RELATED OBJECTS", "STATS", …):
		// the same color as relatedGroup with foreground/background reversed, so
		// they read as an inverted bar (dark text on a 254 background). One space
		// of horizontal padding sits inside the reversed span, so the bar reads as
		// a padded chip rather than hugging the text.
		panelTitle: lipgloss.NewStyle().Foreground(lipgloss.Color("254")).Reverse(true).Padding(0, 1),

		// The load-balancer list is a Lip Gloss table; the right-pad per cell is
		// the column gap. Two spaces keep long name/project columns legible in
		// narrow/condensed fonts. The selected row carries a full-width highlight,
		// so the pad (part of the cell) stays on the highlight background — keep
		// all three padding values equal so columns and the bar stay aligned.
		tableHeader:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Padding(0, 2, 0, 0),
		tableSelected: lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("39")).Padding(0, 2, 0, 0),
		tableCell:     lipgloss.NewStyle().Padding(0, 2, 0, 0),
	}
}

// statusColor maps an OpenStack status string to a Lip Gloss color. Operating
// status drives most coloring; provisioning status is used as a fallback and
// for the list's provisioning column.
func statusColor(status string) lipgloss.Color {
	switch status {
	case "ONLINE", "ACTIVE", "ENABLED", "ALLOCATED", "READY", "HEALTHY":
		return lipgloss.Color("42") // green
	case "DEGRADED", "DRAINING", "BOOTING":
		return lipgloss.Color("214") // amber
	case "ERROR", "FAILOVER_STOPPED":
		return lipgloss.Color("196") // red
	case "PENDING_CREATE", "PENDING_UPDATE", "PENDING_DELETE":
		return lipgloss.Color("214")
	case "OFFLINE", "NO_MONITOR", "DELETED", "DISABLED":
		return lipgloss.Color("244") // grey
	default:
		return lipgloss.Color("252")
	}
}
