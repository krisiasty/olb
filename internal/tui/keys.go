package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the full keybinding set. Grouped to mirror the spec's keymap and to
// drive the help overlay.
type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Home     key.Binding
	End      key.Binding

	Open     key.Binding // enter: the only descent key
	Back     key.Binding // left / esc / backspace
	Forward  key.Binding // right
	LBList   key.Binding // ctrl+home: active workspace root
	TopLevel key.Binding // 1-9: switch view within the active area
	Area     key.Binding // A/L/…: switch area (uppercase accelerators)
	Switcher key.Binding // space: open the area/view switcher overlay
	Picker   key.Binding // h

	YAML    key.Binding // y
	JSON    key.Binding // j
	CopyID  key.Binding // i
	CopyNm  key.Binding // n
	CopyRaw key.Binding // c (raw YAML/JSON overlay only)

	Filter   key.Binding // /
	Status   key.Binding // s
	ShowIDs  key.Binding // d
	Sort     key.Binding // o
	RoleTree key.Binding // t (roles with implied roles)

	Scope        key.Binding // tab: authentication scope selector
	Refresh      key.Binding // r
	AutoRefresh  key.Binding // a
	IntervalUp   key.Binding // + / =
	IntervalDown key.Binding // -
	Telemetry    key.Binding // #
	Reset        key.Binding // z (telemetry overlay)
	Token        key.Binding // * current-token / whoami overlay
	HomeView     key.Binding // ` return to the overview landing
	Help         key.Binding // ?
	Quit         key.Binding // q
	Force        key.Binding // ctrl+c

	Accept key.Binding // enter inside overlays
	Cancel key.Binding // esc inside overlays
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
		Down:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("PgDn", "page down")),
		Home:     key.NewBinding(key.WithKeys("home", "ctrl+a"), key.WithHelp("Home", "top")),
		End:      key.NewBinding(key.WithKeys("end", "ctrl+e"), key.WithHelp("End", "bottom")),

		Open:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Back:     key.NewBinding(key.WithKeys("left", "esc", "backspace"), key.WithHelp("←/esc", "back")),
		Forward:  key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "forward")),
		LBList:   key.NewBinding(key.WithKeys("ctrl+home"), key.WithHelp("ctrl+home", "view root")),
		TopLevel: key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), key.WithHelp("1-9", "views")),
		Area:     key.NewBinding(key.WithKeys(areaKeyStrings()...), key.WithHelp("S/A/L", "area")),
		Switcher: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "switch area")),
		Picker:   key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "history")),

		YAML:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "YAML")),
		JSON:    key.NewBinding(key.WithKeys("j"), key.WithHelp("j", "JSON")),
		CopyID:  key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "copy id")),
		CopyNm:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "copy name")),
		CopyRaw: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy raw")),

		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Status:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "status filter")),
		ShowIDs:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "names/ids")),
		Sort:     key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "sort")),
		RoleTree: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "inheritance tree")),

		Scope:        key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "scope")),
		Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		AutoRefresh:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "auto-refresh")),
		IntervalUp:   key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "longer interval")),
		IntervalDown: key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "shorter interval")),
		Telemetry:    key.NewBinding(key.WithKeys("#"), key.WithHelp("#", "telemetry")),
		Reset:        key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "reset")),
		Token:        key.NewBinding(key.WithKeys("*"), key.WithHelp("*", "token")),
		HomeView:     key.NewBinding(key.WithKeys("`"), key.WithHelp("`", "home")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:         key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		Force:        key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "force quit")),

		Accept: key.NewBinding(key.WithKeys("enter")),
		Cancel: key.NewBinding(key.WithKeys("esc")),
	}
}
