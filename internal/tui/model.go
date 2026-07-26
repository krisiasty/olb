package tui

import (
	"context"
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
	overlayRaw      // y / j raw object view
	overlayScope    // tab authentication scope switcher
	overlayPicker   // h history picker
	overlaySort     // o sort-column picker (top-level lists)
	overlaySwitcher // space area/view switcher
	overlayTelemetry
	overlayToken // * current-token / whoami
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
	// coeSpinner is independent because slow Magnum enrichment must animate
	// without marking the responsive Octavia UI as globally loading.
	coeSpinner        spinner.Model
	coeSpinnerRunning bool
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

	// Top-level resource lists (keys 2-5). VIP rows combine lbs with one Neutron
	// floating-IP collection; the rest load their own collections on demand.
	vipFloatingIPs        []osclient.FloatingIPMapping
	vipFloatingIPsLoaded  bool
	vipFloatingIPsLoading bool
	vipFloatingIPsErr     string
	listeners             []osclient.ListenerRow
	pools                 []osclient.PoolRow
	amphorae              []*model.Node
	listenersLoaded       bool
	poolsLoaded           bool
	amphoraeLoaded        bool
	amphoraeErr           string // e.g. admin RBAC required

	// Identity (auth) area lists.
	users              []osclient.User
	usersLoaded        bool
	usersErr           string // e.g. admin RBAC required
	usersRestriction   string
	domains            []osclient.Domain
	domainsLoaded      bool
	domainsErr         string // e.g. admin RBAC required
	domainsRestriction string
	groups             []osclient.Group
	groupsLoaded       bool
	groupsErr          string // e.g. admin RBAC required
	groupsRestriction  string
	// Group members (users) load lazily when a group is opened, keyed by group ID.
	groupMembers        map[string][]osclient.User
	groupMembersLoaded  map[string]bool
	groupMembersLoading map[string]bool
	groupMembersErr     map[string]string
	// User groups load lazily when a user is opened, keyed by user ID (the inverse
	// of group membership).
	userGroups        map[string][]osclient.Group
	userGroupsLoaded  map[string]bool
	userGroupsLoading map[string]bool
	userGroupsErr     map[string]string
	// userProjects is populated from the self-service accessible-project list
	// when an unprivileged current user cannot enumerate role assignments.
	userProjects map[string][]osclient.Project
	// projectList is the identity area's browsable projects list, distinct from
	// the authentication scopes shown by the global selector.
	projectList       []osclient.Project
	projectListLoaded bool
	projectListErr    string
	roles             []osclient.Role
	rolesLoaded       bool
	rolesErr          string
	rolesRestriction  string
	// Service catalog: services, endpoints, and regions. The endpoints list is
	// shared — a service's and a region's related endpoints are derived from it
	// rather than re-fetched per object.
	services         []osclient.Service
	servicesLoaded   bool
	servicesErr      string
	endpoints        []osclient.Endpoint
	endpointsLoaded  bool
	endpointsLoading bool
	endpointsErr     string
	regions          []osclient.Region
	regionsLoaded    bool
	regionsErr       string
	// Role relations (implied roles + assignments) load lazily when a role is
	// opened, keyed by role ID.
	roleRelations        map[string]roleRelations
	roleRelationsLoaded  map[string]bool
	roleRelationsLoading map[string]bool
	roleRelationsErr     map[string]string
	// Role assignments seen from the owning side load lazily when a user, group,
	// project, or domain is opened — the mirror of the role→assignments view. One
	// generic cache keyed by (owner kind, owner ID) since the four are identical
	// in shape.
	assignments        map[assignmentKey][]osclient.RoleAssignment
	assignmentsLoaded  map[assignmentKey]bool
	assignmentsLoading map[assignmentKey]bool
	assignmentsErr     map[assignmentKey]string
	// known* accumulate every identity object seen through any list (top-level,
	// related, or domain contents), keyed by ID, so a related-object row can be
	// opened into its detail even when its own top-level list was never visited.
	// knownDomainFull / knownProjectFull mark an object whose full attributes are
	// known (from a list) versus a bare reference (only ID + resolved name, e.g. a
	// project reached only through a role assignment).
	knownUsers       map[string]osclient.User
	knownGroups      map[string]osclient.Group
	knownProjects    map[string]osclient.Project
	knownDomains     map[string]osclient.Domain
	knownRoles       map[string]osclient.Role
	knownServices    map[string]osclient.Service
	knownRegions     map[string]osclient.Region
	knownDomainFull  map[string]bool
	knownProjectFull map[string]bool
	// domainContents holds a domain's related projects, groups, and users, loaded
	// lazily when the domain is opened, keyed by domain ID.
	domainContents        map[string]domainContent
	domainContentsLoaded  map[string]bool
	domainContentsLoading map[string]bool
	domainContentsErr     map[string]string

	hist *history
	loc  location

	// Number keys select persistent workspaces within the active area; uppercase
	// accelerators (and the space switcher) select areas. The active workspace is
	// projected into hist/loc/list fields so the existing navigation and rendering
	// code can stay focused on one browser-like stack at a time. Keyed by listKind
	// so adding a view/area needs no fixed-size bookkeeping.
	workspaces      map[listKind]*workspaceState
	activeWorkspace listKind
	workspaceResume workspacePosition
	// areaLastView remembers the view last active in each area, so returning to an
	// area (via its accelerator or the switcher) restores where you left off.
	areaLastView map[areaKind]listKind

	// Current list rows (allEntries unfiltered; entries after filters applied).
	allEntries []entry
	entries    []entry
	cursor     int
	top        int // scroll offset

	// Filters.
	filtering bool // substring filter input focused
	status    statusFilter

	// sortKey is the active top-level-list sort column (per workspace); "" means
	// the natural API order. Only top-level lists are sortable.
	sortKey string

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
	coeClusters        []osclient.COECluster
	coeClustersLoaded  bool
	coeClustersLoading bool
	coeClustersErr     string
	coeClustersAt      time.Time
	// coeCancel aborts the in-flight Magnum cluster listing. That request is slow
	// (many seconds); a project switch cancels it so the stale scope's request
	// stops immediately instead of running to completion only to be discarded.
	coeCancel context.CancelFunc
	// coeClusterDetails caches the slow per-cluster Magnum detail, keyed by
	// cluster UUID so the API and Service load balancers sharing a cluster only
	// fetch it once.
	coeClusterDetails map[string]coeDetailState

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

	// Overlay search (scope switcher / history picker), kept separate from the
	// list filter so opening an overlay doesn't clobber an active list filter.
	search       textinput.Model
	scopes       []osclient.ScopeInfo
	scopeCursor  int
	scopeError   string
	pickCursor   int
	sortCursor   int // highlighted row in the sort-column overlay
	switchCursor int // highlighted row in the space area/view switcher overlay

	// tokenInfo snapshots the current auth token for the * whoami overlay; it is
	// read from the backend (no network) each time the overlay opens.
	tokenInfo osclient.TokenInfo

	// home is the launch/overview landing: a base-view mode (not an overlay nor a
	// workspace) shown until the operator enters an area. Any workspace switch
	// clears it; the ` key returns to it.
	home bool

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
	refreshVIPLBs            *lbsMsg
	refreshVIPFloatingIPs    *vipFloatingIPsMsg
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

	project           osclient.ProjectInfo
	multiProjectScope bool // active scope may expose resources from multiple projects
	scope             osclient.ScopeInfo
	showIDs           bool // list columns show object/project IDs instead of names
	quitting          bool
	clock             func() time.Time
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
	coeSpinner := spinner.New()
	coeSpinner.Spinner = spinner.Dot
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

	scope := backend.CurrentScope()
	project := osclient.ProjectInfo{}
	if scope.Kind == osclient.ScopeProject {
		project = osclient.ProjectInfo{ID: scope.ID, Name: scope.Name, DomainID: scope.DomainID}
	}
	m := Model{
		backend:                backend,
		keys:                   defaultKeys(),
		st:                     st,
		cfg:                    cfg,
		spinner:                sp,
		coeSpinner:             coeSpinner,
		statsSpinner:           statsSpinner,
		filter:                 fi,
		search:                 se,
		vp:                     viewport.New(0, 0),
		cache:                  cache.New(cfg.CacheSize, cfg.CacheTTL),
		project:                project,
		multiProjectScope:      scope.Kind != "" && scope.Kind != osclient.ScopeProject,
		scope:                  scope,
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
		coeClusterDetails:      map[string]coeDetailState{},
		groupMembers:           map[string][]osclient.User{},
		groupMembersLoaded:     map[string]bool{},
		groupMembersLoading:    map[string]bool{},
		groupMembersErr:        map[string]string{},
		userGroups:             map[string][]osclient.Group{},
		userGroupsLoaded:       map[string]bool{},
		userGroupsLoading:      map[string]bool{},
		userGroupsErr:          map[string]string{},
		userProjects:           map[string][]osclient.Project{},
		knownUsers:             map[string]osclient.User{},
		knownGroups:            map[string]osclient.Group{},
		knownProjects:          map[string]osclient.Project{},
		knownDomains:           map[string]osclient.Domain{},
		knownRoles:             map[string]osclient.Role{},
		knownServices:          map[string]osclient.Service{},
		knownRegions:           map[string]osclient.Region{},
		knownDomainFull:        map[string]bool{},
		knownProjectFull:       map[string]bool{},
		domainContents:         map[string]domainContent{},
		domainContentsLoaded:   map[string]bool{},
		domainContentsLoading:  map[string]bool{},
		domainContentsErr:      map[string]string{},
		roleRelations:          map[string]roleRelations{},
		roleRelationsLoaded:    map[string]bool{},
		roleRelationsLoading:   map[string]bool{},
		roleRelationsErr:       map[string]string{},
		assignments:            map[assignmentKey][]osclient.RoleAssignment{},
		assignmentsLoaded:      map[assignmentKey]bool{},
		assignmentsLoading:     map[assignmentKey]bool{},
		assignmentsErr:         map[assignmentKey]string{},
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
	m.home = true // land on the overview until the operator enters an area
	return m
}

// Init loads the initial load balancer list.
func (m Model) Init() tea.Cmd {
	// Pre-warm the Magnum cluster list in the background at startup so a COE
	// cluster / Kubernetes service overview renders its enrichment without a wait
	// on first visit. It is non-blocking, degrades gracefully when Magnum is
	// absent, and its result is cached like any other cluster-list fetch.
	return tea.Batch(m.spinner.Tick, m.loadLBsCmd(), coePreloadCmd(), m.scheduleAutoRefresh(), freshnessTickCmd())
}
