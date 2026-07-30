package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// requestTimeout bounds every backend round trip so a hung API can't wedge the
// UI (the command goroutine returns an error msg instead).
const (
	requestTimeout    = 30 * time.Second
	coeRequestTimeout = 2 * time.Minute
)

func ctxTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), requestTimeout)
}

// --- messages -------------------------------------------------------------

type lbsMsg struct {
	lbs []osclient.LB
	err error
}

type treeMsg struct {
	lbID       string
	tree       *model.Tree
	err        error
	forID      model.Identity // identity to render once the tree is in
	attach     *model.Node    // non-status node to attach before rendering
	background bool           // stale-refresh: don't disturb the view on error
}

// detailIntent is what to do once a node's full configuration has loaded.
type detailIntent int

const (
	intentOverview detailIntent = iota
	intentYAML
	intentJSON
)

type detailMsg struct {
	nodeID    string
	lbID      string
	res       osclient.DetailResult
	intent    detailIntent
	refresh   bool
	workspace listKind
	err       error
}

// listInspectMsg carries a direct detail fetch for a highlighted top-level row.
// It is separate from detailMsg because the inspected node is not attached to
// the current navigation tree and must not mutate it.
type listInspectMsg struct {
	node      *model.Node
	raw       any
	intent    detailIntent
	workspace listKind
	selection entrySelection
	err       error
}

type refResolveMsg struct {
	sourceID  string // node whose unresolved edge we followed
	lbID      string
	workspace listKind
	label     string // edge label (e.g. "floating IP", "instance")
	node      *model.Node
	err       error
}

type amphoraeMsg struct {
	lbID    string
	nodes   []*model.Node
	refresh bool
	err     error
}

// Top-level resource-list load results (keys 3/4/5). Each carries whether it was
// a background refresh so a failure can be reported without wiping the view.
type listenersMsg struct {
	rows    []osclient.ListenerRow
	refresh bool
	err     error
}

type poolsMsg struct {
	rows    []osclient.PoolRow
	refresh bool
	err     error
}

type amphoraeListMsg struct {
	nodes   []*model.Node
	refresh bool
	err     error
}

// usersMsg carries the identity-area users list and any self-service restriction
// reported by the backend. It may still return ErrAdminRequired when neither the
// full collection nor the current token identity is available.
type usersMsg struct {
	users       []osclient.User
	restriction string
	refresh     bool
	err         error
}

type domainsMsg struct {
	domains     []osclient.Domain
	restriction string
	refresh     bool
	err         error
}

type groupsMsg struct {
	groups      []osclient.Group
	restriction string
	refresh     bool
	err         error
}

type groupMembersMsg struct {
	groupID string
	users   []osclient.User
	err     error
}

type userGroupsMsg struct {
	userID string
	groups []osclient.Group
	err    error
}

type projectListMsg struct {
	projects []osclient.Project
	refresh  bool
	err      error
}

type rolesMsg struct {
	roles       []osclient.Role
	restriction string
	refresh     bool
	err         error
}

type roleRelationsMsg struct {
	roleID      string
	implied     []osclient.Role
	assignments []osclient.RoleAssignment
	err         error
}

type roleInferencesMsg struct {
	inferences map[string][]osclient.Role
	generation uint64
	err        error
}

type assignmentsMsg struct {
	key                    assignmentKey
	assignments            []osclient.RoleAssignment
	err                    error
	accessibleProjects     []osclient.Project
	accessibleProjectsRead bool
	accessibleProjectsErr  error
}

type servicesMsg struct {
	services []osclient.Service
	refresh  bool
	err      error
}

type endpointsMsg struct {
	endpoints []osclient.Endpoint
	refresh   bool
	err       error
}

type regionsMsg struct {
	regions []osclient.Region
	refresh bool
	err     error
}

type instancesMsg struct {
	instances []osclient.Instance
	refresh   bool
	err       error
}

type domainContentsMsg struct {
	domainID string
	projects []osclient.Project
	groups   []osclient.Group
	users    []osclient.User
	err      error
}

type scopesMsg struct {
	scopes []osclient.ScopeInfo
	err    error
}

type switchedScopeMsg struct {
	target osclient.ScopeInfo
	scope  osclient.ScopeInfo
	err    error
}

type switchedMsg struct {
	project      osclient.ProjectInfo
	multiProject bool
	err          error
}

type statsMsg struct {
	lbID      string
	stats     map[string]any
	sampledAt time.Time
	refresh   bool
	automatic bool
	err       error
}

type listenerStatsMsg struct {
	lbID       string
	listenerID string
	stats      map[string]any
	sampledAt  time.Time
	refresh    bool
	automatic  bool
	err        error
}

type lbFloatingIPMsg struct {
	lbID    string
	nodes   map[string]*model.Node // keyed by fixed VIP address
	refresh bool
	err     error
}

type vipFloatingIPsMsg struct {
	items   []osclient.FloatingIPMapping
	refresh bool
	err     error
}

type listenerSummariesMsg struct {
	lbID    string
	items   map[string]osclient.ListenerSummary
	refresh bool
	err     error
}

type poolSummariesMsg struct {
	lbID    string
	items   map[string]osclient.PoolSummary
	refresh bool
	err     error
}

type coeClustersMsg struct {
	items     []osclient.COECluster
	projectID string
	all       bool
	err       error
}

type coeClusterDetailMsg struct {
	uuid   string
	detail osclient.COEClusterDetail
	err    error
}

// coePreloadMsg triggers the startup background pre-warm of the Magnum cluster
// list. It flows through Update so the in-flight flag is set on the live model,
// letting a fast drill-in dedupe against the pre-warm instead of refetching.
type coePreloadMsg struct{}

func coePreloadCmd() tea.Cmd {
	return func() tea.Msg { return coePreloadMsg{} }
}

type flashClearMsg struct{ token int }

// --- commands -------------------------------------------------------------

func (m Model) loadLBsCmd() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		lbs, err := b.ListLoadBalancers(ctx)
		return lbsMsg{lbs: lbs, err: err}
	}
}

func (m Model) loadListenersCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		rows, err := b.ListListeners(ctx)
		return listenersMsg{rows: rows, refresh: refresh, err: err}
	}
}

func (m Model) loadPoolsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		rows, err := b.ListPools(ctx)
		return poolsMsg{rows: rows, refresh: refresh, err: err}
	}
}

func (m Model) loadAmphoraeListCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ListAllAmphorae(ctx)
		return amphoraeListMsg{nodes: nodes, refresh: refresh, err: err}
	}
}

func (m Model) loadInstancesCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		instances, err := b.ListInstances(ctx)
		return instancesMsg{instances: instances, refresh: refresh, err: err}
	}
}

func (m Model) loadUsersCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		result, err := b.ListUsers(ctx)
		return usersMsg{users: result.Items, restriction: result.Restriction, refresh: refresh, err: err}
	}
}

func (m Model) loadDomainsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		result, err := b.ListDomains(ctx)
		return domainsMsg{domains: result.Items, restriction: result.Restriction, refresh: refresh, err: err}
	}
}

func (m Model) loadGroupsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		result, err := b.ListGroups(ctx)
		return groupsMsg{groups: result.Items, restriction: result.Restriction, refresh: refresh, err: err}
	}
}

func (m Model) loadGroupMembersCmd(groupID string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		us, err := b.ListGroupMembers(ctx, groupID)
		return groupMembersMsg{groupID: groupID, users: us, err: err}
	}
}

func (m Model) loadUserGroupsCmd(userID string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		gs, err := b.ListUserGroups(ctx, userID)
		return userGroupsMsg{userID: userID, groups: gs, err: err}
	}
}

func (m Model) loadProjectListCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		ps, err := b.ListProjectsDetailed(ctx)
		return projectListMsg{projects: ps, refresh: refresh, err: err}
	}
}

// loadDomainContentsCmd fetches a domain's projects, groups, and users. A per-
// category RBAC denial (ErrAdminRequired) leaves that category empty rather than
// failing the whole load; any other error is surfaced.
func (m Model) loadDomainContentsCmd(domainID string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		ps, perr := b.ListProjectsInDomain(ctx, domainID)
		gs, gerr := b.ListGroupsInDomain(ctx, domainID)
		us, uerr := b.ListUsersInDomain(ctx, domainID)
		return domainContentsMsg{
			domainID: domainID, projects: ps, groups: gs, users: us,
			err: firstNonRBACErr(perr, gerr, uerr),
		}
	}
}

// firstNonRBACErr returns the first error that is neither nil nor an admin-RBAC
// denial (those are handled by degrading to an empty category).
func firstNonRBACErr(errs ...error) error {
	for _, err := range errs {
		if err != nil && !errors.Is(err, osclient.ErrAdminRequired) {
			return err
		}
	}
	return nil
}

func (m Model) loadRolesCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		result, err := b.ListRoles(ctx)
		return rolesMsg{roles: result.Items, restriction: result.Restriction, refresh: refresh, err: err}
	}
}

// loadRoleRelationsCmd fetches a role's implied roles and assignments together.
// A per-category RBAC denial leaves that category empty; any other error is
// surfaced.
func (m Model) loadRoleRelationsCmd(roleID string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		implied, ierr := b.ListImpliedRoles(ctx, roleID)
		assignments, aerr := b.ListRoleAssignments(ctx, roleID)
		return roleRelationsMsg{
			roleID: roleID, implied: implied, assignments: assignments,
			err: firstNonRBACErr(ierr, aerr),
		}
	}
}

func (m Model) loadRoleInferencesCmd() tea.Cmd {
	b := m.backend
	generation := m.roleInferencesGeneration
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		inferences, err := b.ListRoleInferences(ctx)
		return roleInferencesMsg{inferences: inferences, generation: generation, err: err}
	}
}

// loadAssignmentsCmd fetches the role assignments touching one identity object
// (user, group, project, or domain) — the mirror of a role's assignment list.
// Which backend call it makes depends on the owner side.
func (m Model) loadAssignmentsCmd(key assignmentKey) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		var as []osclient.RoleAssignment
		var err error
		switch key.owner {
		case ownerUser:
			as, err = b.ListUserAssignments(ctx, key.id)
		case ownerGroup:
			as, err = b.ListGroupAssignments(ctx, key.id)
		case ownerProject:
			as, err = b.ListProjectAssignments(ctx, key.id)
		case ownerDomain:
			as, err = b.ListDomainAssignments(ctx, key.id)
		}
		// Some Keystone policies return 403 for a forbidden assignment list;
		// others return a successful but empty collection. In either case, an
		// empty result must not hide effective roles proven by the active token.
		if errors.Is(err, osclient.ErrAdminRequired) || (err == nil && len(as) == 0) {
			token := b.CurrentToken()
			tokenAssignments, applicable := activeTokenAssignments(token, key)
			if applicable {
				msg := assignmentsMsg{
					key: key, assignments: tokenAssignments,
				}
				// Keystone commonly denies role-assignment enumeration to project
				// members. For the authenticated user, the self-service projects
				// endpoint still provides every project they can access, not only
				// the active token scope.
				if key.owner != ownerUser {
					return msg
				}
				ps, projectsErr := b.ListProjectsDetailed(ctx)
				msg.accessibleProjects = ps
				msg.accessibleProjectsRead = true
				msg.accessibleProjectsErr = projectsErr
				return msg
			}
		}
		return assignmentsMsg{key: key, assignments: as, err: err}
	}
}

// activeTokenAssignments returns the effective roles the token can prove for
// the selected owner. A token identifies the current user and its active scope,
// but does not expose whether a role was direct, inherited, or supplied by a
// group, so group fallbacks are deliberately unsupported.
func activeTokenAssignments(token osclient.TokenInfo, key assignmentKey) ([]osclient.RoleAssignment, bool) {
	if !token.Available || token.UserID == "" {
		return nil, false
	}
	switch key.owner {
	case ownerUser:
		if key.id != token.UserID {
			return nil, false
		}
	case ownerProject:
		if token.ScopeType != "project" || key.id != token.ScopeID {
			return nil, false
		}
	case ownerDomain:
		if token.ScopeType != "domain" || key.id != token.ScopeID {
			return nil, false
		}
	default:
		return nil, false
	}

	targetType, targetID, targetName := token.ScopeType, token.ScopeID, token.ScopeName
	if targetType != "project" && targetType != "domain" && targetType != "system" {
		return nil, true
	}
	roles := token.RoleDetails
	if len(roles) == 0 {
		roles = make([]osclient.TokenRole, 0, len(token.Roles))
		for _, name := range token.Roles {
			roles = append(roles, osclient.TokenRole{Name: name})
		}
	}
	out := make([]osclient.RoleAssignment, 0, len(roles))
	for _, role := range roles {
		out = append(out, osclient.RoleAssignment{
			RoleID: role.ID, RoleName: role.Name,
			ActorType: "user", ActorID: token.UserID, ActorName: token.UserName,
			TargetType: targetType, TargetID: targetID, TargetName: targetName,
			TokenScoped: true,
		})
	}
	return out, true
}

func (m Model) loadServicesCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		ss, err := b.ListServices(ctx)
		return servicesMsg{services: ss, refresh: refresh, err: err}
	}
}

func (m Model) loadEndpointsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		es, err := b.ListEndpoints(ctx)
		return endpointsMsg{endpoints: es, refresh: refresh, err: err}
	}
}

func (m Model) loadRegionsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		rs, err := b.ListRegions(ctx)
		return regionsMsg{regions: rs, refresh: refresh, err: err}
	}
}

func (m Model) loadVIPFloatingIPsCmd(refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		items, err := b.ListFloatingIPMappings(ctx)
		return vipFloatingIPsMsg{items: items, refresh: refresh, err: err}
	}
}

// startCOEClustersLoad cancels any in-flight Magnum cluster listing and returns
// a command that lists clusters afresh, bound to a cancellable context stored on
// the model. Magnum listing is enrichment (it never blocks Octavia views) but can
// take many seconds; storing the cancel lets a project switch abort the previous
// scope's request instead of leaving it to run to completion only to be discarded
// on arrival. coeClustersLoading still deduplicates concurrent calls.
func (m *Model) startCOEClustersLoad() tea.Cmd {
	m.cancelCOEClustersLoad()
	ctx, cancel := context.WithTimeout(context.Background(), coeRequestTimeout)
	m.coeCancel = cancel
	b := m.backend
	projectID, all := m.project.ID, m.multiProjectScope
	return func() tea.Msg {
		defer cancel()
		items, err := b.ListCOEClusters(ctx)
		return coeClustersMsg{items: items, projectID: projectID, all: all, err: err}
	}
}

// cancelCOEClustersLoad aborts the in-flight cluster listing, if any.
func (m *Model) cancelCOEClustersLoad() {
	if m.coeCancel != nil {
		m.coeCancel()
		m.coeCancel = nil
	}
}

func (m Model) getCOEClusterDetailCmd(uuid string) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		// The per-cluster Magnum detail endpoint is slow (seconds); use the same
		// generous timeout as the cluster list rather than the interactive one.
		ctx, cancel := context.WithTimeout(context.Background(), coeRequestTimeout)
		defer cancel()
		detail, err := b.GetCOECluster(ctx, uuid)
		return coeClusterDetailMsg{uuid: uuid, detail: detail, err: err}
	}
}

func (m Model) getTreeCmd(lbID string, forID model.Identity, background bool) tea.Cmd {
	return m.treeCmd(lbID, forID, background, true)
}

func (m Model) refreshTreeCmd(lbID string, forID model.Identity) tea.Cmd {
	return m.treeCmd(lbID, forID, false, false)
}

func (m Model) amphoraTreeCmd(n *model.Node) tea.Cmd {
	cmd := m.getTreeCmd(n.OwningLBID, n.Identity(), false)
	return func() tea.Msg {
		msg := cmd().(treeMsg)
		msg.attach = n
		return msg
	}
}

func (m Model) treeCmd(lbID string, forID model.Identity, background, useListHint bool) tea.Cmd {
	b := m.backend
	var hint *model.LBMeta
	if useListHint {
		for _, lb := range m.lbs {
			if lb.ID == lbID {
				h := lb.Meta()
				hint = &h
				break
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		tree, err := b.GetTree(ctx, lbID, hint)
		return treeMsg{lbID: lbID, tree: tree, err: err, forID: forID, background: background}
	}
}

func (m Model) fetchDetailCmd(n *model.Node, intent detailIntent) tea.Cmd {
	return m.detailCmd(n, intent, false)
}

func (m Model) inspectListEntryCmd(n *model.Node, intent detailIntent, selection entrySelection) tea.Cmd {
	b := m.backend
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		res, err := b.FetchDetail(ctx, n)
		return listInspectMsg{
			node: n, raw: res.Raw, intent: intent, workspace: workspace, selection: selection, err: err,
		}
	}
}

func (m Model) refreshDetailCmd(n *model.Node) tea.Cmd {
	return m.detailCmd(n, intentOverview, true)
}

func (m Model) detailCmd(n *model.Node, intent detailIntent, refresh bool) tea.Cmd {
	b := m.backend
	id := n.ID
	lbID := n.OwningLBID
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		res, err := b.FetchDetail(ctx, n)
		return detailMsg{nodeID: id, lbID: lbID, res: res, intent: intent, refresh: refresh, workspace: workspace, err: err}
	}
}

func (m Model) resolveFloatingIPCmd(source *model.Node, portID string) tea.Cmd {
	b := m.backend
	sid := source.ID
	lbID := source.OwningLBID
	fixedIP := source.Attrs["address"]
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ResolveFloatingIPs(ctx, lbID, portID)
		node := nodes[fixedIP]
		return refResolveMsg{sourceID: sid, lbID: lbID, workspace: workspace, label: "floating IP", node: node, err: err}
	}
}

func (m Model) lbFloatingIPCmd(lbID, portID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ResolveFloatingIPs(ctx, lbID, portID)
		return lbFloatingIPMsg{lbID: lbID, nodes: nodes, refresh: refresh, err: err}
	}
}

func (m Model) resolveInstanceCmd(source *model.Node, address string) tea.Cmd {
	b := m.backend
	sid := source.ID
	lbID := source.OwningLBID
	workspace := m.activeWorkspace
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		node, err := b.ResolveInstance(ctx, lbID, address)
		return refResolveMsg{sourceID: sid, lbID: lbID, workspace: workspace, label: "instance", node: node, err: err}
	}
}

func (m Model) loadAmphoraeCmd(lbID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		nodes, err := b.ListAmphorae(ctx, lbID)
		return amphoraeMsg{lbID: lbID, nodes: nodes, refresh: refresh, err: err}
	}
}

func (m Model) loadScopesCmd() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		scopes, err := b.ListScopes(ctx)
		return scopesMsg{scopes: scopes, err: err}
	}
}

func (m Model) switchScopeCmd(target osclient.ScopeInfo) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		err := b.SwitchScope(ctx, target)
		return switchedScopeMsg{target: target, scope: b.CurrentScope(), err: err}
	}
}

func (m Model) lbStatsCmd(lbID string) tea.Cmd {
	return m.statsCmd(lbID, false, false)
}

func (m Model) listenerSummariesCmd(lbID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		items, err := b.ListListenerSummaries(ctx, lbID)
		return listenerSummariesMsg{lbID: lbID, items: items, refresh: refresh, err: err}
	}
}

func (m Model) poolSummariesCmd(lbID string, refresh bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		items, err := b.ListPoolSummaries(ctx, lbID)
		return poolSummariesMsg{lbID: lbID, items: items, refresh: refresh, err: err}
	}
}

func (m Model) refreshStatsCmd(lbID string) tea.Cmd {
	return m.statsCmd(lbID, true, false)
}

func (m Model) statsCmd(lbID string, refresh, automatic bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		stats, err := b.LBStats(ctx, lbID)
		return statsMsg{lbID: lbID, stats: stats, sampledAt: m.clock(), refresh: refresh, automatic: automatic, err: err}
	}
}

func (m Model) listenerStatsCmd(lbID, listenerID string, refresh, automatic bool) tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		stats, err := b.ListenerStats(ctx, lbID, listenerID)
		return listenerStatsMsg{
			lbID: lbID, listenerID: listenerID, stats: stats, sampledAt: m.clock(),
			refresh: refresh, automatic: automatic, err: err,
		}
	}
}

func (m Model) currentStatsCmd(refresh, automatic bool) tea.Cmd {
	if m.loc.node == nil {
		return nil
	}
	if m.loc.node.Type == model.TypeListener {
		return m.listenerStatsCmd(m.loc.node.OwningLBID, m.loc.node.ID, refresh, automatic)
	}
	if m.loc.node.Type == model.TypeLoadBalancer {
		return m.statsCmd(m.loc.node.ID, refresh, automatic)
	}
	return nil
}

// flashCmd clears the status flash after a short delay. The token guards against
// a stale timer clearing a newer flash.
func flashCmd(token int) tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return flashClearMsg{token: token}
	})
}
