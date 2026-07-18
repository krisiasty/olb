package tui

import (
	"io"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/cache"
	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// overlayKind is the modal layer currently on top of the list, if any.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlayRaw     // y / j raw object view
	overlayDetail  // d detail panel
	overlayProject // p project switcher
	overlayPicker  // h history picker
)

// location is what the main pane currently shows: the LB list, or a node whose
// subtree has been reparented to the top.
type location struct {
	id   model.Identity
	node *model.Node // nil for the LB list
	tree *model.Tree // owning tree; nil for the LB list
	dead bool
}

func (l location) isList() bool { return l.node == nil && l.id.IsLBList() }

// Config holds the runtime knobs passed in from main.
type Config struct {
	// PrintMode routes copy actions to an on-screen value the user can select,
	// instead of emitting OSC 52 — the escape hatch for terminals without it.
	PrintMode bool
	// CacheSize bounds the LRU of status trees; CacheTTL bounds staleness.
	CacheSize int
	CacheTTL  time.Duration
	// HistoryCap bounds the navigation history (picker usability, not memory).
	HistoryCap int
	// Stdout is where OSC 52 sequences are written (defaults to os.Stdout).
	Stdout io.Writer
}

// model is the root Bubble Tea model.
type Model struct {
	backend Backend
	keys    keyMap
	st      styles
	cfg     Config

	width, height int

	spinner spinner.Model
	filter  textinput.Model // shared for list filter and overlay search
	vp      viewport.Model  // raw / detail / help scroll region

	cache *cache.TreeCache

	// Top-level LB list.
	lbs       []osclient.LB
	lbsLoaded bool

	hist *history
	loc  location

	// Current list rows (allEntries unfiltered; entries after filters applied).
	allEntries []entry
	entries    []entry
	cursor     int
	top        int // scroll offset

	// Filters.
	filtering bool // substring filter input focused
	status    statusFilter

	// Overlay state.
	overlay    overlayKind
	rawContent string                    // last y/j content for the current node (drives o)
	rawFormat  string                    // "yaml" | "json" | ""
	rawTitle   string                    // overlay title override (print mode)
	lbStats    map[string]map[string]any // LB stats cache for the detail panel

	// Overlay search (project switcher / history picker), kept separate from the
	// list filter so opening an overlay doesn't clobber an active list filter.
	search     textinput.Model
	projects   []osclient.ProjectInfo
	projCursor int
	pickCursor int

	// Async / feedback.
	loading     bool
	loadingWhat string
	flash       string
	flashErr    bool
	flashToken  int

	project  osclient.ProjectInfo
	quitting bool
}

// New builds the root model. backend must be authenticated.
func New(backend Backend, cfg Config) Model {
	if cfg.CacheSize <= 0 {
		cfg.CacheSize = 8
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 30 * time.Second
	}
	if cfg.HistoryCap <= 0 {
		cfg.HistoryCap = 300
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	fi := textinput.New()
	fi.Prompt = "/"
	fi.CharLimit = 128

	se := textinput.New()
	se.Prompt = "search: "
	se.CharLimit = 128

	return Model{
		backend: backend,
		keys:    defaultKeys(),
		st:      newStyles(),
		cfg:     cfg,
		spinner: sp,
		filter:  fi,
		search:  se,
		vp:      viewport.New(0, 0),
		cache:   cache.New(cfg.CacheSize, cfg.CacheTTL),
		hist:    newHistory(cfg.HistoryCap),
		project: backend.CurrentProject(),
		lbStats: map[string]map[string]any{},
	}
}

// Init loads the initial load balancer list.
func (m Model) Init() tea.Cmd {
	m.hist.navigate(histEntry{id: model.LBListIdentity})
	return tea.Batch(m.spinner.Tick, m.loadLBsCmd())
}
