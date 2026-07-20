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
	"github.com/krisiasty/olb/internal/telemetry"
)

// overlayKind is the modal layer currently on top of the list, if any.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlayRaw     // y / j raw object view
	overlayProject // p project switcher
	overlayPicker  // h history picker
	overlayTelemetry
)

// location is what the main pane currently shows: the LB list, or a node whose
// subtree has been reparented to the top.
type location struct {
	id   model.Identity
	node *model.Node // nil for the LB list
	tree *model.Tree // owning tree; nil for the LB list
	dead bool
}

// Config holds the runtime knobs passed in from main.
type Config struct {
	// PrintMode routes copy actions to an on-screen value the user can select,
	// instead of emitting OSC 52 — the escape hatch for terminals without it.
	PrintMode bool
	// AllProjects starts the tool with its original authentication scope and no
	// local project filter (the backend must already be in that view mode).
	AllProjects bool
	// CacheSize bounds the LRU of status trees; CacheTTL bounds staleness.
	CacheSize int
	CacheTTL  time.Duration
	// HistoryCap bounds each workspace's navigation history (picker usability,
	// not memory).
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
	// statsSpinner is a cadence indicator, not a loading indicator: while the
	// latest automatic stats sample is still within its expected interval, the
	// moving point shows that another sample is scheduled.
	statsSpinner        spinner.Model
	statsSpinnerRunning bool
	filter              textinput.Model // shared for list filter and overlay search
	vp                  viewport.Model  // raw / help scroll region

	cache *cache.TreeCache

	// Top-level LB list.
	lbs       []osclient.LB
	lbsLoaded bool

	// Top-level resource lists (keys 2-5). VIPs derive from lbs; the rest load on
	// demand and are cached until the next refresh or scope change.
	listeners       []osclient.ListenerRow
	pools           []osclient.PoolRow
	amphorae        []*model.Node
	listenersLoaded bool
	poolsLoaded     bool
	amphoraeLoaded  bool
	amphoraeErr     string // e.g. admin RBAC required

	hist *history
	loc  location

	// Keys 1-5 select persistent workspaces. The active workspace is projected
	// into hist/loc/list fields so the existing navigation and rendering code can
	// stay focused on one browser-like stack at a time.
	workspaces      [5]workspaceState
	activeWorkspace listKind
	workspaceResume workspacePosition

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
	rawContent string // last y/j content for the current node (drives o)
	rawFormat  string // "yaml" | "json" | ""
	rawTitle   string // overlay title override (print mode)

	// Load-balancer overview state. Full configuration and stats load
	// independently when an LB is opened; both are cached alongside the tree.
	lbStats            map[string]map[string]any
	lbStatsChanges     map[string]map[string]statChange
	lbStatsSampledAt   map[string]time.Time
	lbDetailLoading    map[string]bool
	lbStatsLoading     map[string]bool
	lbDetailErr        map[string]string
	lbStatsErr         map[string]string
	lbRelatedErr       map[string]string
	lbFreshness        map[string]overviewFreshness
	lbFIPLoading       map[string]bool
	lbFIPLoaded        map[string]bool
	lbAmphoraLoading   map[string]bool
	lbAmphoraLoaded    map[string]bool
	lbListenersLoading map[string]bool
	lbListenersLoaded  map[string]bool
	lbPoolsLoading     map[string]bool
	lbPoolsLoaded      map[string]bool

	// Automatic refresh uses a fast, user-selectable cadence for stats and a
	// slower fixed cadence for the full list/status graph. Generations make old
	// Bubble Tea timer messages harmless after toggling or changing intervals.
	autoRefreshEnabled bool
	autoIntervalIndex  int
	autoGeneration     uint64
	autoStatsLoading   map[string]bool

	// API telemetry is collected continuously by the OpenStack HTTP transport;
	// application telemetry is sampled when the overlay refreshes. The overlay
	// uses an independently-controlled display cadence for both.
	telemetrySnapshot      telemetry.Snapshot
	applicationTelemetry   telemetry.ApplicationSnapshot
	telemetryUpdatedAt     time.Time
	telemetryAutoEnabled   bool
	telemetryIntervalIndex int
	telemetryGeneration    uint64

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

	// An explicit or automatic full refresh keeps the rendered data in place.
	// Every API result required by the current overview is staged and committed
	// together so no field or related-object row jumps ahead.
	refreshing               bool
	refreshLBID              string
	refreshDetail            *detailMsg
	refreshHealthMonitor     *detailMsg
	refreshMonitorExpected   bool
	refreshStats             *statsMsg
	refreshListenerStats     *listenerStatsMsg
	refreshFIP               *lbFloatingIPMsg
	refreshFIPExpected       bool
	refreshAmphorae          *amphoraeMsg
	refreshAmphoraeExpected  bool
	refreshListeners         *listenerSummariesMsg
	refreshListenersExpected bool
	refreshPools             *poolSummariesMsg
	refreshPoolsExpected     bool
	refreshAt                model.Identity
	refreshSelection         entrySelection
	refreshSelectionOK       bool
	refreshCursor            int
	refreshAutomatic         bool

	project     osclient.ProjectInfo
	allProjects bool // global-admin listing without a concrete project filter
	showIDs     bool // list columns show object/project IDs instead of names
	quitting    bool
	clock       func() time.Time
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
	statsSpinner := spinner.New()
	statsSpinner.Spinner = spinner.Spinner{
		Frames: []string{"∙∙∙∙", "●∙∙∙", "∙●∙∙", "∙∙●∙", "∙∙∙●"},
		FPS:    time.Second,
	}
	st := newStyles()

	fi := textinput.New()
	fi.Prompt = "filter: "
	fi.PromptStyle = st.filterPrompt
	fi.CharLimit = 128

	se := textinput.New()
	se.Prompt = "search: "
	se.CharLimit = 128

	m := Model{
		backend:                backend,
		keys:                   defaultKeys(),
		st:                     st,
		cfg:                    cfg,
		spinner:                sp,
		statsSpinner:           statsSpinner,
		filter:                 fi,
		search:                 se,
		vp:                     viewport.New(0, 0),
		cache:                  cache.New(cfg.CacheSize, cfg.CacheTTL),
		project:                backend.CurrentProject(),
		allProjects:            cfg.AllProjects,
		lbStats:                map[string]map[string]any{},
		lbStatsChanges:         map[string]map[string]statChange{},
		lbStatsSampledAt:       map[string]time.Time{},
		lbDetailLoading:        map[string]bool{},
		lbStatsLoading:         map[string]bool{},
		lbDetailErr:            map[string]string{},
		lbStatsErr:             map[string]string{},
		lbRelatedErr:           map[string]string{},
		lbFreshness:            map[string]overviewFreshness{},
		lbFIPLoading:           map[string]bool{},
		lbFIPLoaded:            map[string]bool{},
		lbAmphoraLoading:       map[string]bool{},
		lbAmphoraLoaded:        map[string]bool{},
		lbListenersLoading:     map[string]bool{},
		lbListenersLoaded:      map[string]bool{},
		lbPoolsLoading:         map[string]bool{},
		lbPoolsLoaded:          map[string]bool{},
		autoRefreshEnabled:     true,
		autoIntervalIndex:      defaultAutoRefreshIntervalIndex,
		autoGeneration:         1,
		autoStatsLoading:       map[string]bool{},
		telemetryAutoEnabled:   true,
		telemetryIntervalIndex: defaultAutoRefreshIntervalIndex,
		telemetryGeneration:    1,
		clock:                  time.Now,
	}
	m.resetWorkspaces()
	return m
}

// Init loads the initial load balancer list.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadLBsCmd(), m.scheduleAutoRefresh(), freshnessTickCmd())
}
