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

	Open    key.Binding // enter: the only descent key
	Back    key.Binding // left / esc / backspace
	Forward key.Binding // right
	LBList  key.Binding // ctrl+home
	Picker  key.Binding // h

	YAML    key.Binding // y
	JSON    key.Binding // j
	CopyID  key.Binding // i
	CopyNm  key.Binding // n
	CopyRaw key.Binding // o

	Filter  key.Binding // /
	Status  key.Binding // s
	ShowIDs key.Binding // d

	Project      key.Binding // p
	Refresh      key.Binding // r
	AutoRefresh  key.Binding // a
	IntervalUp   key.Binding // + / =
	IntervalDown key.Binding // -
	Telemetry    key.Binding // t
	Reset        key.Binding // z (telemetry overlay)
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
		Home:     key.NewBinding(key.WithKeys("home"), key.WithHelp("Home", "top")),
		End:      key.NewBinding(key.WithKeys("end"), key.WithHelp("End", "bottom")),

		Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Back:    key.NewBinding(key.WithKeys("left", "esc", "backspace"), key.WithHelp("←/esc", "back")),
		Forward: key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "forward")),
		LBList:  key.NewBinding(key.WithKeys("ctrl+home"), key.WithHelp("ctrl+home", "LB list")),
		Picker:  key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "history")),

		YAML:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "YAML")),
		JSON:    key.NewBinding(key.WithKeys("j"), key.WithHelp("j", "JSON")),
		CopyID:  key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "copy id")),
		CopyNm:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "copy name")),
		CopyRaw: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "copy raw")),

		Filter:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Status:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "status filter")),
		ShowIDs: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "names/ids")),

		Project:      key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "project")),
		Refresh:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		AutoRefresh:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "auto-refresh")),
		IntervalUp:   key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "longer interval")),
		IntervalDown: key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "shorter interval")),
		Telemetry:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "API telemetry")),
		Reset:        key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "reset")),
		Help:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:         key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		Force:        key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "force quit")),

		Accept: key.NewBinding(key.WithKeys("enter")),
		Cancel: key.NewBinding(key.WithKeys("esc")),
	}
}
