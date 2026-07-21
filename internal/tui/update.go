package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/cache"
	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// Update is the central event loop. All shared-graph mutation happens here on
// the single UI goroutine; commands only fetch and return data.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = msg.Width - 4
		m.search.Width = msg.Width - 12
		m.vp.Width = msg.Width
		m.vp.Height = m.bodyHeight()
		if m.overlay == overlayTelemetry {
			m.vp.Height = msg.Height - 2
			m.rebuildTelemetryContent(false)
		}
		m.ensureVisible()
		return m, nil

	case spinner.TickMsg:
		switch msg.ID {
		case m.spinner.ID():
			if !m.loading {
				return m, nil
			}
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		case m.coeSpinner.ID():
			if !m.coeClustersLoading {
				m.coeSpinnerRunning = false
				return m, nil
			}
			var cmd tea.Cmd
			m.coeSpinner, cmd = m.coeSpinner.Update(msg)
			return m, cmd
		case m.statsSpinner.ID():
			updated := m.updatedAt(m.currentStatsID(), sectionStats)
			if !m.isStatsOverview() || !m.statsWithinAutoInterval(updated) {
				m.statsSpinnerRunning = false
				return m, nil
			}
			var cmd tea.Cmd
			m.statsSpinner, cmd = m.statsSpinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case lbsMsg:
		return m.onLBs(msg)
	case vipFloatingIPsMsg:
		return m.onVIPFloatingIPs(msg)
	case listenersMsg:
		return m.onListeners(msg)
	case poolsMsg:
		return m.onPools(msg)
	case amphoraeListMsg:
		return m.onAmphoraeList(msg)
	case treeMsg:
		return m.onTree(msg)
	case detailMsg:
		return m.onDetail(msg)
	case statsMsg:
		return m.onStats(msg)
	case listenerStatsMsg:
		return m.onListenerStats(msg)
	case lbFloatingIPMsg:
		return m.onLBFloatingIP(msg)
	case listenerSummariesMsg:
		return m.onListenerSummaries(msg)
	case poolSummariesMsg:
		return m.onPoolSummaries(msg)
	case coeClustersMsg:
		return m.onCOEClusters(msg)
	case refResolveMsg:
		return m.onRefResolve(msg)
	case amphoraeMsg:
		return m.onAmphorae(msg)
	case projectsMsg:
		return m.onProjects(msg)
	case switchedMsg:
		return m.onSwitched(msg)
	case flashClearMsg:
		if msg.token == m.flashToken {
			m.flash, m.flashErr = "", false
		}
		return m, nil
	case autoStatsTickMsg:
		return m.onAutoStatsTick(msg)
	case autoFullTickMsg:
		return m.onAutoFullTick(msg)
	case telemetryTickMsg:
		return m.onTelemetryTick(msg)
	case freshnessTickMsg:
		return m, freshnessTickCmd()

	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// --- message handlers -----------------------------------------------------

func (m Model) onLBs(msg lbsMsg) (tea.Model, tea.Cmd) {
	wasRefresh := m.refreshing && m.refreshLBID == ""
	if wasRefresh && m.refreshAt.Equal(model.VIPListIdentity) {
		m.refreshVIPLBs = &msg
		return m, m.commitVIPListRefresh()
	}
	m.loading = false
	if msg.err != nil {
		if wasRefresh {
			return m, m.finishRefresh("refresh load balancers: " + msg.err.Error())
		}
		return m, m.setFlash("list load balancers: "+msg.err.Error(), true)
	}
	m.lbs = msg.lbs
	m.lbsLoaded = true
	// The LB list and derived VIPs are sourced directly from this data. The
	// other top-level lists also use it to resolve owning load-balancer names,
	// so rebuild them when their own rows have already arrived.
	if m.loc.isTopLevelList() {
		ready := false
		switch m.loc.listKind() {
		case kindLB:
			ready = true
		case kindVIP:
			ready = m.vipFloatingIPsLoaded
		case kindListener:
			ready = m.listenersLoaded
		case kindPool:
			ready = m.poolsLoaded
		case kindAmphora:
			ready = m.amphoraeLoaded
		}
		if ready {
			m.setTopLevelEntries()
			m.restoreRefreshSelection()
		} else if m.loc.listKind() == kindVIP {
			m.loading, m.loadingWhat = true, "floating IPs"
		}
	}
	if wasRefresh {
		return m, m.finishRefresh("")
	}
	return m, nil
}

func (m Model) onVIPFloatingIPs(msg vipFloatingIPsMsg) (tea.Model, tea.Cmd) {
	if msg.refresh {
		if m.refreshing && m.refreshAt.Equal(model.VIPListIdentity) {
			m.refreshVIPFloatingIPs = &msg
			return m, m.commitVIPListRefresh()
		}
		return m, nil
	}
	m.vipFloatingIPsLoading = false
	m.vipFloatingIPsLoaded = true
	m.loading = m.loc.isTopLevelList() && m.loc.listKind() == kindVIP && !m.lbsLoaded
	if m.loading {
		m.loadingWhat = "load balancers"
	}
	m.vipFloatingIPsErr = ""
	if msg.err == nil {
		m.vipFloatingIPs = msg.items
	} else {
		m.vipFloatingIPs = nil
		m.vipFloatingIPsErr = msg.err.Error()
	}
	if m.loc.isTopLevelList() && m.loc.listKind() == kindVIP && m.lbsLoaded {
		m.setTopLevelEntries()
		m.restoreRefreshSelection()
	}
	if msg.err != nil {
		return m, m.setFlash("list floating IPs: "+msg.err.Error(), true)
	}
	return m, nil
}

func (m *Model) commitVIPListRefresh() tea.Cmd {
	if m.refreshVIPLBs == nil || m.refreshVIPFloatingIPs == nil {
		return nil
	}
	m.loading = false
	m.vipFloatingIPsLoading = false
	var failures []string
	if m.refreshVIPLBs.err != nil {
		failures = append(failures, "load balancers: "+m.refreshVIPLBs.err.Error())
	} else {
		m.lbs = m.refreshVIPLBs.lbs
		m.lbsLoaded = true
	}
	if m.refreshVIPFloatingIPs.err != nil {
		m.vipFloatingIPsErr = m.refreshVIPFloatingIPs.err.Error()
		failures = append(failures, "floating IPs: "+m.refreshVIPFloatingIPs.err.Error())
	} else {
		m.vipFloatingIPs = m.refreshVIPFloatingIPs.items
		m.vipFloatingIPsLoaded = true
		m.vipFloatingIPsErr = ""
	}
	if m.loc.isTopLevelList() && m.loc.listKind() == kindVIP {
		m.setTopLevelEntries()
		m.restoreRefreshSelection()
	}
	if len(failures) > 0 {
		return m.finishRefresh("refresh incomplete (" + strings.Join(failures, "; ") + ")")
	}
	return m.finishRefresh("")
}

func (m Model) onListeners(msg listenersMsg) (tea.Model, tea.Cmd) {
	if !msg.refresh {
		m.loading = false
	}
	if msg.err != nil {
		if msg.refresh {
			return m, m.finishRefresh("refresh listeners: " + msg.err.Error())
		}
		return m, m.setFlash("list listeners: "+msg.err.Error(), true)
	}
	m.listeners = msg.rows
	m.listenersLoaded = true
	if m.loc.isTopLevelList() && m.loc.listKind() == kindListener {
		m.setTopLevelEntries()
		m.restoreRefreshSelection()
	}
	if msg.refresh {
		return m, m.finishRefresh("")
	}
	return m, nil
}

func (m Model) onPools(msg poolsMsg) (tea.Model, tea.Cmd) {
	if !msg.refresh {
		m.loading = false
	}
	if msg.err != nil {
		if msg.refresh {
			return m, m.finishRefresh("refresh pools: " + msg.err.Error())
		}
		return m, m.setFlash("list pools: "+msg.err.Error(), true)
	}
	m.pools = msg.rows
	m.poolsLoaded = true
	if m.loc.isTopLevelList() && m.loc.listKind() == kindPool {
		m.setTopLevelEntries()
		m.restoreRefreshSelection()
	}
	if msg.refresh {
		return m, m.finishRefresh("")
	}
	return m, nil
}

func (m Model) onAmphoraeList(msg amphoraeListMsg) (tea.Model, tea.Cmd) {
	if !msg.refresh {
		m.loading = false
	}
	m.amphoraeLoaded = true
	m.amphoraeErr = ""
	if msg.err != nil {
		if errors.Is(msg.err, osclient.ErrAdminRequired) {
			// Not an error state: show an explanatory empty list instead.
			m.amphorae = nil
			m.amphoraeErr = "amphora listing requires admin RBAC"
		} else if msg.refresh {
			return m, m.finishRefresh("refresh amphorae: " + msg.err.Error())
		} else {
			return m, m.setFlash("list amphorae: "+msg.err.Error(), true)
		}
	} else {
		m.amphorae = msg.nodes
	}
	if m.loc.isTopLevelList() && m.loc.listKind() == kindAmphora {
		m.setTopLevelEntries()
		m.restoreRefreshSelection()
	}
	if msg.refresh {
		return m, m.finishRefresh("")
	}
	return m, nil
}

func (m Model) onTree(msg treeMsg) (tea.Model, tea.Cmd) {
	wasRefresh := m.refreshing && m.refreshLBID == msg.lbID
	if !wasRefresh {
		m.loading = false
	}
	cur, ok := m.hist.current()
	if msg.err != nil {
		if osclient.IsNotFound(msg.err) && ok && cur.id.OwningLBID == msg.lbID {
			// The whole LB is gone: mark this history entry dead and show it.
			m.hist.markDead()
			m.loc = location{id: cur.id, dead: true}
			m.allEntries = nil
			m.applyFilters()
			if wasRefresh {
				m.endRefresh()
			}
			return m, m.setFlash("this object was deleted since you last viewed it", true)
		}
		if wasRefresh {
			refreshErr := msg.err.Error()
			m.lbDetailErr[msg.lbID] = refreshErr
			m.lbStatsErr[msg.lbID] = refreshErr
			m.lbRelatedErr[msg.lbID] = refreshErr
			return m, m.finishRefresh("refresh: " + msg.err.Error())
		}
		return m, m.setFlash("load tree: "+msg.err.Error(), true)
	}
	if wasRefresh {
		m.preserveLBOverview(msg.lbID, msg.tree)
	} else {
		delete(m.lbRelatedErr, msg.lbID)
		m.markFresh(msg.lbID, sectionRelated)
	}
	m.applyKubernetesRelations(msg.tree)
	if msg.attach != nil && msg.tree != nil {
		attachAmphora(msg.tree, msg.attach)
	}
	m.cache.Put(msg.lbID, msg.tree)
	if ok && cur.id.OwningLBID == msg.lbID {
		m.buildNodeLocation(cur.id, msg.tree)
		m.restoreRefreshSelection()
		if wasRefresh {
			if m.loc.node != nil && m.loc.node.Type == model.TypeLoadBalancer {
				return m, m.reloadLBOverview()
			}
			if m.loc.node != nil && m.loc.node.Type == model.TypeVIP {
				return m, m.reloadVIPOverview()
			}
			if m.loc.node != nil && m.loc.node.Type == model.TypeListener {
				return m, m.reloadListenerOverview()
			}
			if m.loc.node != nil && m.loc.node.Type == model.TypePool {
				return m, m.reloadPoolOverview()
			}
			if m.loc.node != nil && m.loc.node.Type == model.TypeMember {
				return m, m.reloadMemberOverview()
			}
			if m.loc.node != nil && m.loc.node.Type == model.TypeHealthMonitor {
				return m, m.reloadHealthMonitorOverview()
			}
			if m.loc.node != nil && (m.loc.node.Type == model.TypeCOECluster || m.loc.node.Type == model.TypeKubeService) {
				return m, tea.Batch(m.ensureCOEClustersCmd(!m.refreshAutomatic), m.finishRefresh(""))
			}
			delete(m.lbRelatedErr, msg.lbID)
			m.markFresh(msg.lbID, sectionRelated)
			return m, m.finishRefresh("")
		}
		if m.loc.node != nil && m.loc.node.Type == model.TypeListener {
			m.markFresh(m.loc.node.ID, sectionRelated)
		}
		return m, m.loadLBOverview()
	}
	if wasRefresh {
		return m, m.finishRefresh("")
	}
	return m, nil
}

func (m Model) onDetail(msg detailMsg) (tea.Model, tea.Cmd) {
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			_, node := m.detailTarget(msg.lbID, msg.nodeID)
			targetType := m.refreshAt.Type
			if targetType == "" && node != nil {
				targetType = node.Type
				if node.Type == model.TypeHealthMonitor && node.Parent != nil {
					targetType = node.Parent.Type
				}
			}
			if node != nil && node.Type == model.TypeHealthMonitor && targetType == model.TypePool {
				m.refreshHealthMonitor = &msg
				return m, m.commitPoolRefresh()
			}
			m.refreshDetail = &msg
			// The transaction remains tied to the resource whose detail request
			// was issued, even if history navigation has since changed m.loc.
			switch targetType {
			case model.TypeVIP:
				return m, m.commitVIPRefresh()
			case model.TypeListener:
				return m, m.commitListenerRefresh()
			case model.TypePool:
				return m, m.commitPoolRefresh()
			case model.TypeMember:
				return m, m.commitMemberRefresh()
			case model.TypeAmphora:
				return m, m.commitAmphoraRefresh()
			case model.TypeHealthMonitor:
				return m, m.commitHealthMonitorRefresh()
			default:
				return m, m.commitLBRefresh()
			}
		}
		return m, nil
	}
	if msg.intent == intentOverview {
		delete(m.lbDetailLoading, msg.nodeID)
	} else if msg.workspace == m.activeWorkspace {
		m.loading = false
	}
	if msg.err != nil {
		if msg.intent == intentOverview {
			m.lbDetailErr[msg.nodeID] = msg.err.Error()
			return m, nil
		}
		if msg.workspace != m.activeWorkspace {
			return m, nil
		}
		return m, m.setFlash("load detail: "+msg.err.Error(), true)
	}
	delete(m.lbDetailErr, msg.nodeID)
	node := m.applyDetailResult(msg)
	if node == nil {
		return m, nil
	}
	if msg.intent == intentOverview {
		if node.Type == model.TypeLoadBalancer || node.Type == model.TypeListener || node.Type == model.TypePool || node.Type == model.TypeMember || node.Type == model.TypeAmphora || node.Type == model.TypeHealthMonitor {
			m.markFresh(node.ID, sectionDetails)
		}
		if node.Type == model.TypeHealthMonitor && m.loc.node != nil && node.Parent == m.loc.node && m.loc.node.Type == model.TypePool {
			m.markFresh(m.loc.node.ID, sectionRelated)
		}
		return m, nil
	}
	if msg.workspace != m.activeWorkspace {
		return m, nil
	}
	return m, m.openInspect(node, msg.intent)
}

func (m *Model) applyDetailResult(msg detailMsg) *model.Node {
	tree, node := m.detailTarget(msg.lbID, msg.nodeID)
	if node == nil {
		return nil
	}
	// Apply the fetched detail on the UI goroutine.
	node.Raw = msg.res.Raw
	node.DetailLoaded = true
	if node.Type == model.TypeLoadBalancer {
		for _, key := range []string{
			"provider", "vip_address", "admin_state_up", "flavor_id",
			"created_at", "updated_at", "description",
		} {
			delete(node.Attrs, key)
		}
	}
	if node.Type == model.TypeVIP {
		for _, key := range []string{
			"port_name", "port_id", "subnet_name", "subnet_id", "network_name",
			"network_id", "security_group_ids",
		} {
			delete(node.Attrs, key)
		}
	}
	if msg.res.IsListener {
		for _, key := range []string{
			"protocol", "port", "admin_state_up", "connection_limit", "description",
			"created_at", "updated_at", "allowed_cidrs", "certificate_ref",
			"certificate_name", "certificate_subject", "certificate_issuer",
			"certificate_not_before", "certificate_not_after", "certificate_error",
			"sni_certificate_count", "tls_versions", "alpn_protocols",
		} {
			delete(node.Attrs, key)
		}
	}
	if node.Type == model.TypePool {
		for _, key := range []string{
			"protocol", "lb_algorithm", "admin_state_up", "project_id", "description",
			"member_count", "listener_count", "healthmonitor_id", "subnet_id",
			"session_persistence", "persistence_cookie", "tls_enabled", "tls_versions",
			"alpn_protocols", "tls_ciphers", "tags", "created_at", "updated_at",
		} {
			delete(node.Attrs, key)
		}
	}
	if node.Type == model.TypeMember {
		for _, key := range []string{
			"name", "project_id", "subnet_id", "address", "port", "weight",
			"backup", "admin_state_up", "monitor_address", "monitor_port",
			"tags", "created_at", "updated_at",
		} {
			delete(node.Attrs, key)
		}
	}
	if node.Type == model.TypeAmphora {
		for _, key := range []string{
			"role", "status", "lb_network_ip", "ha_ip", "ha_port_id", "compute_id",
			"vrrp_port_id", "vrrp_ip", "vrrp_interface", "vrrp_id", "vrrp_priority",
			"cached_zone", "image_id", "cert_expiration", "cert_busy", "created_at", "updated_at",
		} {
			delete(node.Attrs, key)
		}
	}
	if node.Type == model.TypeHealthMonitor {
		for _, key := range []string{
			"type", "delay", "timeout", "max_retries", "max_retries_down",
			"admin_state_up", "http_method", "url_path", "expected_codes",
			"project_id", "created_at", "updated_at", "tags", "domain_name", "http_version",
		} {
			delete(node.Attrs, key)
		}
	}
	for k, v := range msg.res.Attrs {
		node.SetAttr(k, v)
	}
	if tree != nil {
		if msg.res.IsListener {
			tree.ResolveListenerDefaultPool(node.ID, msg.res.ListenerDefaultPoolID)
		}
		if msg.res.IsL7Policy {
			tree.ResolveL7PolicyRedirect(node.ID, msg.res.L7Action, msg.res.L7RedirectPoolID)
		}
	}
	node.RefsResolved = true
	// Newly-resolved reference edges can add rows to the current view.
	if m.loc.node == node || (node.Parent != nil && node.Parent == m.loc.node) ||
		(m.loc.node != nil && m.loc.node.Parent == node) {
		m.allEntries = locationEntries(m.loc.node)
		m.applyFilters()
	}
	return node
}

func (m Model) onStats(msg statsMsg) (tea.Model, tea.Cmd) {
	if msg.automatic {
		delete(m.autoStatsLoading, msg.lbID)
		// A full refresh owns the visible value once it has begun; its staged
		// stats response will be committed with the rest of the overview.
		if m.refreshing && m.refreshLBID == msg.lbID {
			return m, nil
		}
	}
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			m.refreshStats = &msg
			return m, m.commitLBRefresh()
		}
		return m, nil
	}
	delete(m.lbStatsLoading, msg.lbID)
	if msg.err != nil {
		m.lbStatsErr[msg.lbID] = msg.err.Error()
		return m, nil
	}
	delete(m.lbStatsErr, msg.lbID)
	m.applyStatsSample(msg.lbID, msg.stats, msg.sampledAt)
	m.markFresh(msg.lbID, sectionStats)
	cmd := m.ensureStatsSpinner()
	return m, cmd
}

func (m Model) onListenerStats(msg listenerStatsMsg) (tea.Model, tea.Cmd) {
	resourceID := msg.listenerID
	if msg.automatic {
		delete(m.autoStatsLoading, resourceID)
		if m.refreshing && m.refreshLBID == msg.lbID && m.loc.node != nil && m.loc.node.ID == resourceID {
			return m, nil
		}
	}
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			m.refreshListenerStats = &msg
			return m, m.commitListenerRefresh()
		}
		return m, nil
	}
	delete(m.lbStatsLoading, resourceID)
	if msg.err != nil {
		m.lbStatsErr[resourceID] = msg.err.Error()
		return m, nil
	}
	delete(m.lbStatsErr, resourceID)
	m.applyStatsSample(resourceID, msg.stats, msg.sampledAt)
	m.markFresh(resourceID, sectionStats)
	return m, m.ensureStatsSpinner()
}

func (m Model) onLBFloatingIP(msg lbFloatingIPMsg) (tea.Model, tea.Cmd) {
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			m.refreshFIP = &msg
			if m.refreshAt.Type == model.TypeVIP {
				return m, m.commitVIPRefresh()
			}
			return m, m.commitLBRefresh()
		}
		return m, nil
	}
	delete(m.lbFIPLoading, msg.lbID)
	m.lbFIPLoaded[msg.lbID] = true
	m.applyLBFloatingIP(msg)
	if msg.err == nil {
		m.markFresh(msg.lbID, sectionRelated)
	}
	return m, nil
}

func (m Model) onListenerSummaries(msg listenerSummariesMsg) (tea.Model, tea.Cmd) {
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			m.refreshListeners = &msg
			if m.refreshAt.Type == model.TypePool {
				return m, m.commitPoolRefresh()
			}
			return m, m.commitLBRefresh()
		}
		return m, nil
	}
	delete(m.lbListenersLoading, msg.lbID)
	m.lbListenersLoaded[msg.lbID] = true
	if msg.err == nil {
		m.applyListenerSummaries(msg.lbID, msg.items)
		m.markFresh(msg.lbID, sectionRelated)
	}
	return m, nil
}

func (m Model) onPoolSummaries(msg poolSummariesMsg) (tea.Model, tea.Cmd) {
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			m.refreshPools = &msg
			if m.refreshAt.Type == model.TypeListener {
				return m, m.commitListenerRefresh()
			}
			return m, m.commitLBRefresh()
		}
		return m, nil
	}
	delete(m.lbPoolsLoading, msg.lbID)
	m.lbPoolsLoaded[msg.lbID] = true
	if msg.err == nil {
		m.applyPoolSummaries(msg.lbID, msg.items)
		m.markFresh(msg.lbID, sectionRelated)
	}
	return m, nil
}

// detailTarget locates the tree and node targeted by an async detail response
// even if the user navigated elsewhere while the request was in flight.
func (m Model) detailTarget(lbID, nodeID string) (*model.Tree, *model.Node) {
	if m.loc.tree != nil && (lbID == "" || m.loc.tree.Root.ID == lbID) {
		if n := m.loc.tree.Node(nodeID); n != nil {
			return m.loc.tree, n
		}
	}
	if entry, ok := m.cache.Peek(lbID); ok && entry.Tree != nil {
		return entry.Tree, entry.Tree.Node(nodeID)
	}
	return nil, nil
}

// loadLBOverview starts the two independent requests backing the inline LB
// overview. Re-entering a cached LB does not refetch data already present.
func (m *Model) loadLBOverview() tea.Cmd {
	if m.isVIPOverview() {
		return m.loadVIPOverview(false)
	}
	if m.isListenerOverview() {
		return m.loadListenerOverview(false)
	}
	if m.isPoolOverview() {
		return m.loadPoolOverview(false)
	}
	if m.isMemberOverview() {
		return m.loadMemberOverview(false)
	}
	if m.isAmphoraOverview() {
		return m.loadAmphoraOverview(false)
	}
	if m.isHealthMonitorOverview() {
		return m.loadHealthMonitorOverview(false)
	}
	if m.isCOEClusterOverview() || m.isKubernetesServiceOverview() {
		return m.ensureCOEClustersCmd(false)
	}
	return m.startLBOverview(false)
}

func (m *Model) reloadListenerOverview() tea.Cmd {
	return m.loadListenerOverview(true)
}

func (m *Model) reloadPoolOverview() tea.Cmd {
	return m.loadPoolOverview(true)
}

func (m *Model) reloadMemberOverview() tea.Cmd {
	return m.loadMemberOverview(true)
}

func (m *Model) loadAmphoraOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypeAmphora {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.lbDetailLoading[n.ID] = true
		return m.refreshDetailCmd(n)
	}
	if n.DetailLoaded || m.lbDetailLoading[n.ID] {
		return nil
	}
	m.lbDetailLoading[n.ID] = true
	delete(m.lbDetailErr, n.ID)
	return m.fetchDetailCmd(n, intentOverview)
}

func (m *Model) reloadHealthMonitorOverview() tea.Cmd {
	return m.loadHealthMonitorOverview(true)
}

func (m *Model) reloadVIPOverview() tea.Cmd {
	return m.loadVIPOverview(true)
}

func (m *Model) loadListenerOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypeListener {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.refreshListenerStats = nil
		m.lbDetailLoading[n.ID] = true
		m.lbStatsLoading[n.ID] = true
		m.refreshPoolsExpected = false
		cmds := []tea.Cmd{
			m.refreshDetailCmd(n),
			m.listenerStatsCmd(n.OwningLBID, n.ID, true, false),
		}
		// Pool summaries enrich related-object rows, but they are independent of
		// the listener's own detail and statistics refresh. Do not let a slow
		// pool-list request keep the entire view in the refreshing state.
		if !m.lbPoolsLoading[n.OwningLBID] {
			m.lbPoolsLoading[n.OwningLBID] = true
			cmds = append(cmds, m.poolSummariesCmd(n.OwningLBID, false))
		}
		return tea.Batch(cmds...)
	}
	var cmds []tea.Cmd
	if !n.DetailLoaded && !m.lbDetailLoading[n.ID] {
		m.lbDetailLoading[n.ID] = true
		delete(m.lbDetailErr, n.ID)
		cmds = append(cmds, m.fetchDetailCmd(n, intentOverview))
	}
	if _, loaded := m.lbStats[n.ID]; !loaded && !m.lbStatsLoading[n.ID] {
		m.lbStatsLoading[n.ID] = true
		delete(m.lbStatsErr, n.ID)
		cmds = append(cmds, m.listenerStatsCmd(n.OwningLBID, n.ID, false, false))
	}
	if !m.lbPoolsLoaded[n.OwningLBID] && !m.lbPoolsLoading[n.OwningLBID] {
		m.lbPoolsLoading[n.OwningLBID] = true
		cmds = append(cmds, m.poolSummariesCmd(n.OwningLBID, false))
	}
	if cmd := m.ensureStatsSpinner(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

func (m *Model) loadPoolOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypePool {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.refreshHealthMonitor = nil
		m.lbDetailLoading[n.ID] = true
		cmds := []tea.Cmd{m.refreshDetailCmd(n)}
		monitor := poolHealthMonitor(n)
		m.refreshMonitorExpected = monitor != nil
		if m.refreshMonitorExpected {
			m.lbDetailLoading[monitor.ID] = true
			cmds = append(cmds, m.refreshDetailCmd(monitor))
		}
		m.refreshListeners = nil
		m.refreshListenersExpected = true
		m.lbListenersLoading[n.OwningLBID] = true
		cmds = append(cmds, m.listenerSummariesCmd(n.OwningLBID, true))
		return tea.Batch(cmds...)
	}
	var cmds []tea.Cmd
	if !n.DetailLoaded && !m.lbDetailLoading[n.ID] {
		m.lbDetailLoading[n.ID] = true
		delete(m.lbDetailErr, n.ID)
		cmds = append(cmds, m.fetchDetailCmd(n, intentOverview))
	}
	if monitor := poolHealthMonitor(n); monitor != nil && !monitor.DetailLoaded && !m.lbDetailLoading[monitor.ID] {
		m.lbDetailLoading[monitor.ID] = true
		delete(m.lbDetailErr, monitor.ID)
		cmds = append(cmds, m.fetchDetailCmd(monitor, intentOverview))
	}
	lbID := n.OwningLBID
	if !m.lbListenersLoaded[lbID] && !m.lbListenersLoading[lbID] {
		m.lbListenersLoading[lbID] = true
		cmds = append(cmds, m.listenerSummariesCmd(lbID, false))
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

func poolHealthMonitor(pool *model.Node) *model.Node {
	for _, child := range pool.Children {
		if child.Type == model.TypeHealthMonitor {
			return child
		}
	}
	return nil
}

func (m *Model) loadMemberOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypeMember {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.lbDetailLoading[n.ID] = true
		return m.refreshDetailCmd(n)
	}
	if n.DetailLoaded || m.lbDetailLoading[n.ID] {
		return nil
	}
	m.lbDetailLoading[n.ID] = true
	delete(m.lbDetailErr, n.ID)
	return m.fetchDetailCmd(n, intentOverview)
}

func (m *Model) loadHealthMonitorOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypeHealthMonitor {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.lbDetailLoading[n.ID] = true
		return m.refreshDetailCmd(n)
	}
	var cmds []tea.Cmd
	if !n.DetailLoaded && !m.lbDetailLoading[n.ID] {
		m.lbDetailLoading[n.ID] = true
		delete(m.lbDetailErr, n.ID)
		cmds = append(cmds, m.fetchDetailCmd(n, intentOverview))
	}
	// The pool row uses the same rich summary as every other overview. Enrich
	// it lazily when a monitor was reached from cold history/cache state.
	if pool := n.Parent; pool != nil && pool.Type == model.TypePool && !pool.DetailLoaded && !m.lbDetailLoading[pool.ID] {
		m.lbDetailLoading[pool.ID] = true
		delete(m.lbDetailErr, pool.ID)
		cmds = append(cmds, m.fetchDetailCmd(pool, intentOverview))
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

func (m *Model) loadVIPOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypeVIP {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.refreshFIP = nil
		m.lbDetailLoading[n.ID] = true
		cmds := []tea.Cmd{m.refreshDetailCmd(n)}
		portID := n.Attrs["port_id"]
		m.refreshFIPExpected = portID != ""
		if m.refreshFIPExpected {
			m.lbFIPLoading[n.OwningLBID] = true
			cmds = append(cmds, m.lbFloatingIPCmd(n.OwningLBID, portID, true))
		}
		return tea.Batch(cmds...)
	}
	var cmds []tea.Cmd
	if !n.DetailLoaded && !m.lbDetailLoading[n.ID] {
		m.lbDetailLoading[n.ID] = true
		delete(m.lbDetailErr, n.ID)
		cmds = append(cmds, m.fetchDetailCmd(n, intentOverview))
	}
	lbID := n.OwningLBID
	if !m.lbFIPLoaded[lbID] && !m.lbFIPLoading[lbID] {
		if portID := n.Attrs["port_id"]; portID != "" {
			m.lbFIPLoading[lbID] = true
			cmds = append(cmds, m.lbFloatingIPCmd(lbID, portID, false))
		}
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

// reloadLBOverview forces both requests for an explicit refresh while leaving
// the previously-rendered values in place until both responses have arrived.
func (m *Model) reloadLBOverview() tea.Cmd {
	return m.startLBOverview(true)
}

func (m *Model) startLBOverview(refresh bool) tea.Cmd {
	n := m.loc.node
	if n == nil || n.Type != model.TypeLoadBalancer {
		return nil
	}
	if refresh {
		m.refreshDetail = nil
		m.refreshStats = nil
		m.refreshFIP = nil
		m.refreshAmphorae = nil
		m.refreshListeners = nil
		m.refreshPools = nil
		m.lbDetailLoading[n.ID] = true
		m.lbStatsLoading[n.ID] = true
		cmds := []tea.Cmd{m.refreshDetailCmd(n), m.refreshStatsCmd(n.ID)}
		portID := lbVIPPortID(n)
		m.refreshFIPExpected = portID != ""
		if m.refreshFIPExpected {
			m.lbFIPLoading[n.ID] = true
			cmds = append(cmds, m.lbFloatingIPCmd(n.ID, portID, true))
		}
		m.refreshAmphoraeExpected = m.loc.tree != nil && !m.loc.tree.Meta.IsOVN()
		if m.refreshAmphoraeExpected {
			m.lbAmphoraLoading[n.ID] = true
			cmds = append(cmds, m.loadAmphoraeCmd(n.ID, true))
		}
		m.refreshListenersExpected = true
		m.lbListenersLoading[n.ID] = true
		cmds = append(cmds, m.listenerSummariesCmd(n.ID, true))
		m.refreshPoolsExpected = true
		m.lbPoolsLoading[n.ID] = true
		cmds = append(cmds, m.poolSummariesCmd(n.ID, true))
		if cmd := m.ensureCOEClustersCmd(!m.refreshAutomatic); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...)
	}
	var cmds []tea.Cmd
	if !n.DetailLoaded && !m.lbDetailLoading[n.ID] {
		m.lbDetailLoading[n.ID] = true
		delete(m.lbDetailErr, n.ID)
		cmds = append(cmds, m.fetchDetailCmd(n, intentOverview))
	}
	if _, loaded := m.lbStats[n.ID]; !loaded && !m.lbStatsLoading[n.ID] {
		m.lbStatsLoading[n.ID] = true
		delete(m.lbStatsErr, n.ID)
		cmds = append(cmds, m.lbStatsCmd(n.ID))
	}
	if !m.lbFIPLoaded[n.ID] && !m.lbFIPLoading[n.ID] {
		if portID := lbVIPPortID(n); portID != "" {
			m.lbFIPLoading[n.ID] = true
			cmds = append(cmds, m.lbFloatingIPCmd(n.ID, portID, false))
		}
	}
	if m.loc.tree != nil && !m.loc.tree.Meta.IsOVN() && !m.lbAmphoraLoaded[n.ID] && !m.lbAmphoraLoading[n.ID] {
		m.lbAmphoraLoading[n.ID] = true
		cmds = append(cmds, m.loadAmphoraeCmd(n.ID, false))
	}
	if !m.lbListenersLoaded[n.ID] && !m.lbListenersLoading[n.ID] {
		m.lbListenersLoading[n.ID] = true
		cmds = append(cmds, m.listenerSummariesCmd(n.ID, false))
	}
	if !m.lbPoolsLoaded[n.ID] && !m.lbPoolsLoading[n.ID] {
		m.lbPoolsLoading[n.ID] = true
		cmds = append(cmds, m.poolSummariesCmd(n.ID, false))
	}
	if cmd := m.ensureCOEClustersCmd(false); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.ensureStatsSpinner(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return tea.Batch(cmds...)
	}
}

func primaryVIP(lb *model.Node) *model.Node {
	for _, child := range lb.Children {
		if child.Type == model.TypeVIP && child.Attrs["vip_kind"] == "primary" {
			return child
		}
	}
	// Trees cached by older versions have no vip_kind marker.
	for _, child := range lb.Children {
		if child.Type == model.TypeVIP {
			return child
		}
	}
	return nil
}

func lbVIPPortID(lb *model.Node) string {
	if vip := primaryVIP(lb); vip != nil {
		return vip.Attrs["port_id"]
	}
	return ""
}

// preserveLBOverview carries full-detail values into a newly-fetched status
// tree. The new detail response is applied only when the matching stats response
// is also ready, so these values bridge the refresh without flickering.
func (m *Model) preserveLBOverview(lbID string, fresh *model.Tree) {
	if fresh == nil || fresh.Root == nil {
		return
	}
	var old *model.Node
	var oldMeta model.LBMeta
	if m.loc.tree != nil && m.loc.tree.Root != nil && m.loc.tree.Root.ID == lbID {
		old = m.loc.tree.Root
		oldMeta = m.loc.tree.Meta
	} else if entry, ok := m.cache.Peek(lbID); ok && entry.Tree != nil {
		old = entry.Tree.Root
		oldMeta = entry.Tree.Meta
	}
	if old == nil {
		return
	}
	if fresh.Meta.ProjectName == "" && fresh.Meta.ProjectID == oldMeta.ProjectID {
		fresh.Meta.ProjectName = oldMeta.ProjectName
	}
	for key, value := range old.Attrs {
		if _, exists := fresh.Root.Attrs[key]; !exists {
			fresh.Root.SetAttr(key, value)
		}
	}
	for _, child := range old.Children {
		if replacement := fresh.Node(child.ID); replacement != nil {
			switch child.Type {
			case model.TypeVIP:
				for _, key := range []string{
					"port_name", "subnet_name", "network_name", "security_group_ids", "floating_ip",
				} {
					if value := child.Attrs[key]; value != "" {
						replacement.SetAttr(key, value)
					}
				}
				if child.DetailLoaded {
					replacement.DetailLoaded = true
					replacement.Raw = child.Raw
				}
			case model.TypeListener:
				for key, value := range child.Attrs {
					replacement.SetAttr(key, value)
				}
				if child.DetailLoaded {
					replacement.DetailLoaded = true
					replacement.Raw = child.Raw
				}
			case model.TypePool:
				for key, value := range child.Attrs {
					replacement.SetAttr(key, value)
				}
				if child.DetailLoaded {
					replacement.DetailLoaded = true
					replacement.Raw = child.Raw
				}
				preservePoolHealthMonitor(child, fresh)
				preservePoolMembers(child, fresh)
			}
		}
	}
	if old.DetailLoaded {
		fresh.Root.DetailLoaded = true
		fresh.Root.Raw = old.Raw
	}
	// Direct amphora rows are not part of Octavia's status tree. Carry the
	// last-known instances into the replacement graph until their background
	// listing completes, just like the cached detail/stats values above.
	for _, child := range old.Children {
		if child.Type != model.TypeAmphora {
			continue
		}
		child.Parent = fresh.Root
		fresh.Root.Children = append(fresh.Root.Children, child)
		fresh.Attach(child)
	}
}

func preservePoolHealthMonitor(oldPool *model.Node, fresh *model.Tree) {
	oldMonitor := poolHealthMonitor(oldPool)
	if oldMonitor == nil || !oldMonitor.DetailLoaded {
		return
	}
	replacement := fresh.Node(oldMonitor.ID)
	if replacement == nil || replacement.Type != model.TypeHealthMonitor {
		return
	}
	for key, value := range oldMonitor.Attrs {
		replacement.SetAttr(key, value)
	}
	replacement.DetailLoaded = true
	replacement.Raw = oldMonitor.Raw
}

func preservePoolMembers(oldPool *model.Node, fresh *model.Tree) {
	for _, oldMember := range oldPool.Children {
		if oldMember.Type != model.TypeMember || !oldMember.DetailLoaded {
			continue
		}
		replacement := fresh.Node(oldMember.ID)
		if replacement == nil || replacement.Type != model.TypeMember {
			continue
		}
		for key, value := range oldMember.Attrs {
			replacement.SetAttr(key, value)
		}
		replacement.DetailLoaded = true
		replacement.Raw = oldMember.Raw
	}
}

// commitLBRefresh atomically publishes the detail and stats responses. Failed
// sections retain their old data, completion timestamp, and a stale marker.
func (m *Model) commitLBRefresh() tea.Cmd {
	if m.refreshDetail == nil || m.refreshStats == nil ||
		(m.refreshFIPExpected && m.refreshFIP == nil) ||
		(m.refreshAmphoraeExpected && m.refreshAmphorae == nil) ||
		(m.refreshListenersExpected && m.refreshListeners == nil) ||
		(m.refreshPoolsExpected && m.refreshPools == nil) {
		return nil
	}
	lbID := m.refreshLBID
	detail := *m.refreshDetail
	stats := *m.refreshStats
	delete(m.lbDetailLoading, lbID)
	delete(m.lbStatsLoading, lbID)
	delete(m.lbFIPLoading, lbID)
	delete(m.lbAmphoraLoading, lbID)
	delete(m.lbListenersLoading, lbID)
	delete(m.lbPoolsLoading, lbID)

	var failures []string
	if detail.err != nil {
		m.lbDetailErr[lbID] = detail.err.Error()
		failures = append(failures, "details: "+detail.err.Error())
	} else {
		delete(m.lbDetailErr, lbID)
		m.applyDetailResult(detail)
		m.markFresh(lbID, sectionDetails)
	}
	if stats.err != nil {
		m.lbStatsErr[lbID] = stats.err.Error()
		failures = append(failures, "stats: "+stats.err.Error())
	} else {
		delete(m.lbStatsErr, lbID)
		m.applyStatsSample(lbID, stats.stats, stats.sampledAt)
		m.markFresh(lbID, sectionStats)
	}
	var relatedFailures []string
	if m.refreshFIPExpected {
		m.lbFIPLoaded[lbID] = true
		if m.refreshFIP.err == nil {
			m.applyLBFloatingIP(*m.refreshFIP)
		} else if isRefreshFailure(m.refreshFIP.err) {
			relatedFailures = append(relatedFailures, "floating IP: "+m.refreshFIP.err.Error())
		}
	}
	if m.refreshAmphoraeExpected {
		m.lbAmphoraLoaded[lbID] = true
		if m.refreshAmphorae.err == nil {
			m.applyAmphorae(lbID, m.refreshAmphorae.nodes)
			m.restoreRefreshSelection()
		} else if isRefreshFailure(m.refreshAmphorae.err) {
			relatedFailures = append(relatedFailures, "amphorae: "+m.refreshAmphorae.err.Error())
		}
	}
	if m.refreshListenersExpected {
		m.lbListenersLoaded[lbID] = true
		if m.refreshListeners.err == nil {
			m.applyListenerSummaries(lbID, m.refreshListeners.items)
		} else if isRefreshFailure(m.refreshListeners.err) {
			relatedFailures = append(relatedFailures, "listeners: "+m.refreshListeners.err.Error())
		}
	}
	if m.refreshPoolsExpected {
		m.lbPoolsLoaded[lbID] = true
		if m.refreshPools.err == nil {
			m.applyPoolSummaries(lbID, m.refreshPools.items)
			m.restoreRefreshSelection()
		} else if isRefreshFailure(m.refreshPools.err) {
			relatedFailures = append(relatedFailures, "pools: "+m.refreshPools.err.Error())
		}
	}
	if len(relatedFailures) == 0 {
		delete(m.lbRelatedErr, lbID)
		m.markFresh(lbID, sectionRelated)
	} else {
		m.lbRelatedErr[lbID] = strings.Join(relatedFailures, "; ")
		failures = append(failures, "related objects: "+m.lbRelatedErr[lbID])
	}
	if len(failures) > 0 {
		finish := m.finishRefresh("refresh incomplete (" + strings.Join(failures, "; ") + ")")
		return batchWithOptional(finish, m.ensureStatsSpinner())
	}
	finish := m.finishRefresh("")
	return batchWithOptional(finish, m.ensureStatsSpinner())
}

func (m *Model) commitListenerRefresh() tea.Cmd {
	if m.refreshDetail == nil || m.refreshListenerStats == nil ||
		(m.refreshPoolsExpected && m.refreshPools == nil) {
		return nil
	}
	detail := *m.refreshDetail
	stats := *m.refreshListenerStats
	resourceID := detail.nodeID
	delete(m.lbDetailLoading, resourceID)
	delete(m.lbStatsLoading, resourceID)
	if m.refreshPoolsExpected {
		delete(m.lbPoolsLoading, detail.lbID)
	}

	var failures []string
	if detail.err != nil {
		m.lbDetailErr[resourceID] = detail.err.Error()
		failures = append(failures, "details: "+detail.err.Error())
	} else {
		delete(m.lbDetailErr, resourceID)
		m.applyDetailResult(detail)
		m.markFresh(resourceID, sectionDetails)
	}
	if stats.err != nil {
		m.lbStatsErr[resourceID] = stats.err.Error()
		failures = append(failures, "stats: "+stats.err.Error())
	} else {
		delete(m.lbStatsErr, resourceID)
		m.applyStatsSample(resourceID, stats.stats, stats.sampledAt)
		m.markFresh(resourceID, sectionStats)
	}
	if m.refreshPoolsExpected {
		m.lbPoolsLoaded[detail.lbID] = true
		if m.refreshPools.err != nil {
			m.lbRelatedErr[resourceID] = m.refreshPools.err.Error()
			failures = append(failures, "related pools: "+m.refreshPools.err.Error())
		} else {
			delete(m.lbRelatedErr, resourceID)
			m.applyPoolSummaries(detail.lbID, m.refreshPools.items)
			m.markFresh(resourceID, sectionRelated)
		}
	}
	if len(failures) > 0 {
		return batchWithOptional(
			m.finishRefresh("refresh incomplete ("+strings.Join(failures, "; ")+")"),
			m.ensureStatsSpinner(),
		)
	}
	return batchWithOptional(m.finishRefresh(""), m.ensureStatsSpinner())
}

func (m *Model) commitVIPRefresh() tea.Cmd {
	if m.refreshDetail == nil || (m.refreshFIPExpected && m.refreshFIP == nil) {
		return nil
	}
	detail := *m.refreshDetail
	resourceID := detail.nodeID
	delete(m.lbDetailLoading, resourceID)
	delete(m.lbFIPLoading, detail.lbID)

	var failures []string
	if detail.err != nil {
		m.lbDetailErr[resourceID] = detail.err.Error()
		failures = append(failures, "details: "+detail.err.Error())
	} else {
		delete(m.lbDetailErr, resourceID)
		m.applyDetailResult(detail)
		m.markFresh(resourceID, sectionDetails)
	}
	if m.refreshFIPExpected {
		m.lbFIPLoaded[detail.lbID] = true
		if m.refreshFIP.err == nil {
			m.applyLBFloatingIP(*m.refreshFIP)
		} else if isRefreshFailure(m.refreshFIP.err) {
			failures = append(failures, "floating IP: "+m.refreshFIP.err.Error())
		}
	}
	if len(failures) > 0 {
		return m.finishRefresh("refresh incomplete (" + strings.Join(failures, "; ") + ")")
	}
	return m.finishRefresh("")
}

func (m *Model) commitPoolRefresh() tea.Cmd {
	if m.refreshDetail == nil ||
		(m.refreshMonitorExpected && m.refreshHealthMonitor == nil) ||
		(m.refreshListenersExpected && m.refreshListeners == nil) {
		return nil
	}
	detail := *m.refreshDetail
	resourceID := detail.nodeID
	delete(m.lbDetailLoading, resourceID)
	delete(m.lbListenersLoading, detail.lbID)
	if m.refreshHealthMonitor != nil {
		delete(m.lbDetailLoading, m.refreshHealthMonitor.nodeID)
	}

	var failures []string
	if detail.err != nil {
		m.lbDetailErr[resourceID] = detail.err.Error()
		failures = append(failures, "details: "+detail.err.Error())
	} else {
		delete(m.lbDetailErr, resourceID)
		m.applyDetailResult(detail)
		m.markFresh(resourceID, sectionDetails)
	}

	var relatedFailures []string
	if m.refreshMonitorExpected {
		monitor := *m.refreshHealthMonitor
		if monitor.err == nil {
			m.applyDetailResult(monitor)
		} else {
			relatedFailures = append(relatedFailures, "health monitor: "+monitor.err.Error())
		}
	}
	if m.refreshListenersExpected {
		m.lbListenersLoaded[detail.lbID] = true
		if m.refreshListeners.err == nil {
			m.applyListenerSummaries(detail.lbID, m.refreshListeners.items)
		} else {
			relatedFailures = append(relatedFailures, "listeners: "+m.refreshListeners.err.Error())
		}
	}
	if len(relatedFailures) == 0 {
		delete(m.lbRelatedErr, resourceID)
		m.markFresh(resourceID, sectionRelated)
	} else {
		m.lbRelatedErr[resourceID] = strings.Join(relatedFailures, "; ")
		failures = append(failures, "related objects: "+m.lbRelatedErr[resourceID])
	}
	if len(failures) > 0 {
		return m.finishRefresh("refresh incomplete (" + strings.Join(failures, "; ") + ")")
	}
	return m.finishRefresh("")
}

func (m *Model) commitMemberRefresh() tea.Cmd {
	if m.refreshDetail == nil {
		return nil
	}
	detail := *m.refreshDetail
	resourceID := detail.nodeID
	delete(m.lbDetailLoading, resourceID)
	if detail.err != nil {
		m.lbDetailErr[resourceID] = detail.err.Error()
		return m.finishRefresh("refresh incomplete (details: " + detail.err.Error() + ")")
	}
	delete(m.lbDetailErr, resourceID)
	m.applyDetailResult(detail)
	m.markFresh(resourceID, sectionDetails)
	m.markFresh(resourceID, sectionRelated)
	return m.finishRefresh("")
}

func (m *Model) commitAmphoraRefresh() tea.Cmd {
	if m.refreshDetail == nil {
		return nil
	}
	detail := *m.refreshDetail
	resourceID := detail.nodeID
	delete(m.lbDetailLoading, resourceID)
	if detail.err != nil {
		m.lbDetailErr[resourceID] = detail.err.Error()
		return m.finishRefresh("refresh incomplete (details: " + detail.err.Error() + ")")
	}
	delete(m.lbDetailErr, resourceID)
	m.applyDetailResult(detail)
	m.markFresh(resourceID, sectionDetails)
	return m.finishRefresh("")
}

func (m *Model) commitHealthMonitorRefresh() tea.Cmd {
	if m.refreshDetail == nil {
		return nil
	}
	detail := *m.refreshDetail
	resourceID := detail.nodeID
	delete(m.lbDetailLoading, resourceID)
	if detail.err != nil {
		m.lbDetailErr[resourceID] = detail.err.Error()
		return m.finishRefresh("refresh incomplete (details: " + detail.err.Error() + ")")
	}
	delete(m.lbDetailErr, resourceID)
	m.applyDetailResult(detail)
	m.markFresh(resourceID, sectionDetails)
	m.markFresh(resourceID, sectionRelated)
	return m.finishRefresh("")
}

func batchWithOptional(primary, optional tea.Cmd) tea.Cmd {
	if optional == nil {
		return primary
	}
	return tea.Batch(primary, optional)
}

func (m *Model) finishRefresh(errText string) tea.Cmd {
	automatic := m.refreshAutomatic
	m.endRefresh()
	if errText != "" {
		return m.setFlash(errText, true)
	}
	if automatic {
		return nil
	}
	return m.setFlash("refreshed", false)
}

func isRefreshFailure(err error) bool {
	return err != nil && !errors.Is(err, osclient.ErrUnavailable) && !errors.Is(err, osclient.ErrAdminRequired)
}

func (m *Model) endRefresh() {
	m.loading = false
	m.loadingWhat = ""
	m.refreshing = false
	m.refreshLBID = ""
	m.refreshVIPLBs = nil
	m.refreshVIPFloatingIPs = nil
	m.refreshDetail = nil
	m.refreshHealthMonitor = nil
	m.refreshMonitorExpected = false
	m.refreshStats = nil
	m.refreshListenerStats = nil
	m.refreshFIP = nil
	m.refreshFIPExpected = false
	m.refreshAmphorae = nil
	m.refreshAmphoraeExpected = false
	m.refreshListeners = nil
	m.refreshListenersExpected = false
	m.refreshPools = nil
	m.refreshPoolsExpected = false
	m.refreshAt = model.Identity{}
	m.refreshSelection = entrySelection{}
	m.refreshSelectionOK = false
	m.refreshCursor = 0
	m.refreshAutomatic = false
}

func (m *Model) applyLBFloatingIP(msg lbFloatingIPMsg) {
	if msg.err != nil {
		return
	}
	tree, lb := m.detailTarget(msg.lbID, msg.lbID)
	if lb == nil || tree == nil {
		return
	}
	for _, vip := range lb.Children {
		if vip.Type != model.TypeVIP {
			continue
		}
		node := msg.nodes[vip.Attrs["address"]]
		if node == nil {
			delete(vip.Attrs, "floating_ip")
			vip.ResolveEdge("floating IP", nil)
			continue
		}
		node.OwningLBID = msg.lbID
		tree.Attach(node)
		vip.ResolveEdge("floating IP", node)
		address := node.Attrs["floating_ip"]
		if address == "" {
			address = node.Name
		}
		vip.SetAttr("floating_ip", address)
	}
	if m.loc.node != nil && m.loc.node.Type == model.TypeVIP && m.loc.node.OwningLBID == msg.lbID {
		m.allEntries = locationEntries(m.loc.node)
		m.applyFilters()
	}
}

func (m *Model) captureRefreshSelection() {
	m.refreshAt = m.loc.id
	m.refreshCursor = m.cursor
	m.refreshSelectionOK = m.cursor >= 0 && m.cursor < len(m.entries) && m.entries[m.cursor].selectable()
	if m.refreshSelectionOK {
		m.refreshSelection = m.entries[m.cursor].selection()
	}
}

func (m *Model) restoreRefreshSelection() {
	if !m.loc.id.Equal(m.refreshAt) {
		return
	}
	selected := -1
	if m.refreshSelectionOK {
		for i := range m.entries {
			if m.entries[i].selectable() && m.entries[i].selection().equal(m.refreshSelection) {
				selected = i
				break
			}
		}
	}
	if selected < 0 && len(m.entries) > 0 {
		selected = nearestSelectableIndex(m.entries, m.refreshCursor)
	}
	if selected >= 0 {
		m.cursor = selected
		m.ensureVisible()
	}
}

func (m Model) onRefResolve(msg refResolveMsg) (tea.Model, tea.Cmd) {
	active := msg.workspace == m.activeWorkspace
	if active {
		m.loading = false
	}
	if msg.err != nil {
		if !active {
			return m, nil
		}
		if errors.Is(msg.err, osclient.ErrUnavailable) {
			return m, m.setFlash(msg.label+" lookup is unavailable in this cloud/scope", true)
		}
		return m, m.setFlash("resolve "+msg.label+": "+msg.err.Error(), true)
	}
	var tree *model.Tree
	if active && m.loc.tree != nil && (msg.lbID == "" || m.loc.tree.Root.ID == msg.lbID) {
		tree = m.loc.tree
	} else if cached, ok := m.cache.Peek(msg.lbID); ok {
		tree = cached.Tree
	}
	if tree == nil {
		return m, nil
	}
	src := tree.Node(msg.sourceID)
	if src == nil && active {
		src = m.loc.node
	}
	if msg.node == nil {
		// A genuine "no such boundary object" (e.g. an internal LB with no
		// floating IP) — mark the edge missing so it stops inviting a lookup.
		if src != nil {
			src.ResolveEdge(msg.label, nil)
			if msg.label == "floating IP" {
				delete(src.Attrs, "floating_ip")
			}
			if active && m.loc.node == src {
				m.allEntries = locationEntries(src)
				m.applyFilters()
			}
		}
		if !active {
			return m, nil
		}
		return m, m.setFlash("no "+msg.label+" associated with this object", false)
	}
	if src != nil {
		msg.node.OwningLBID = src.OwningLBID
		if msg.label == "floating IP" {
			address := msg.node.Attrs["floating_ip"]
			if address == "" {
				address = msg.node.Name
			}
			src.SetAttr("floating_ip", address)
		}
	}
	tree.Attach(msg.node)
	if src != nil {
		src.ResolveEdge(msg.label, msg.node)
	}
	if active {
		m.saveHistoryPosition()
		m.hist.navigate(histEntry{id: msg.node.Identity(), viaRef: true})
		m.clearFilter()
		return m, m.render()
	}
	state := &m.workspaces[msg.workspace]
	state.hist.navigate(histEntry{id: msg.node.Identity(), viaRef: true})
	state.filterValue = ""
	return m, nil
}

func (m Model) onAmphorae(msg amphoraeMsg) (tea.Model, tea.Cmd) {
	if msg.refresh {
		if m.refreshing && m.refreshLBID == msg.lbID {
			m.refreshAmphorae = &msg
			return m, m.commitLBRefresh()
		}
		return m, nil
	}
	delete(m.lbAmphoraLoading, msg.lbID)
	m.lbAmphoraLoaded[msg.lbID] = true
	if msg.err == nil {
		m.applyAmphorae(msg.lbID, msg.nodes)
		m.markFresh(msg.lbID, sectionRelated)
	}
	return m, nil
}

func (m Model) onCOEClusters(msg coeClustersMsg) (tea.Model, tea.Cmd) {
	if msg.projectID != m.project.ID || msg.all != m.allProjects {
		return m, nil
	}
	m.coeClustersLoading = false
	m.coeSpinnerRunning = false
	m.coeClustersLoaded = true
	m.coeClustersAt = m.clock()
	if msg.err != nil {
		m.coeClustersErr = msg.err.Error()
	} else {
		m.coeClusters = msg.items
		m.coeClustersErr = ""
	}
	if m.loc.tree == nil || m.loc.tree.Root == nil {
		return m, nil
	}
	if inferKubernetesLB(m.loc.tree.Root.Name).kind == kubernetesLBNone {
		return m, nil
	}
	m.applyKubernetesRelations(m.loc.tree)
	if m.loc.node == m.loc.tree.Root {
		m.allEntries = locationEntries(m.loc.node)
		m.applyFilters()
	} else if m.loc.node != nil && (m.loc.node.Type == model.TypeCOECluster || m.loc.node.Type == model.TypeKubeService) {
		if replacement := m.loc.tree.Node(m.loc.id.ID); replacement != nil {
			m.loc.node = replacement
			m.allEntries = locationEntries(replacement)
			m.applyFilters()
		}
	}
	if m.loc.node != nil {
		m.markFresh(m.loc.node.ID, sectionDetails)
	}
	m.markFresh(m.loc.tree.Root.ID, sectionRelated)
	return m, nil
}

func (m *Model) applyAmphorae(lbID string, nodes []*model.Node) {
	var tree *model.Tree
	if m.loc.tree != nil && m.loc.tree.Root != nil && m.loc.tree.Root.ID == lbID {
		tree = m.loc.tree
	} else if entry, ok := m.cache.Peek(lbID); ok {
		tree = entry.Tree
	}
	if tree == nil || tree.Root == nil {
		return
	}
	for _, amphora := range nodes {
		amphora.OwningLBID = lbID
	}
	tree.ReplaceChildrenOfType(tree.Root, model.TypeAmphora, nodes)
	if m.loc.node == tree.Root {
		m.allEntries = nodeEntries(tree.Root)
		m.applyFilters()
	}
}

func (m *Model) applyListenerSummaries(lbID string, items map[string]osclient.ListenerSummary) {
	var tree *model.Tree
	if m.loc.tree != nil && m.loc.tree.Root != nil && m.loc.tree.Root.ID == lbID {
		tree = m.loc.tree
	} else if entry, ok := m.cache.Peek(lbID); ok {
		tree = entry.Tree
	}
	if tree == nil || tree.Root == nil {
		return
	}
	for _, listener := range tree.Root.Children {
		if listener.Type != model.TypeListener {
			continue
		}
		item, ok := items[listener.ID]
		if !ok {
			delete(listener.Attrs, "protocol")
			delete(listener.Attrs, "port")
			continue
		}
		listener.SetAttr("protocol", item.Protocol)
		if item.ProtocolPort > 0 {
			listener.SetAttr("port", fmt.Sprintf("%d", item.ProtocolPort))
		} else {
			delete(listener.Attrs, "port")
		}
	}
	if m.loc.node == tree.Root {
		m.allEntries = nodeEntries(tree.Root)
		m.applyFilters()
	} else if m.loc.node != nil && m.loc.node.Type == model.TypePool && m.loc.node.OwningLBID == lbID {
		m.allEntries = locationEntries(m.loc.node)
		m.applyFilters()
	}
}

func (m *Model) applyPoolSummaries(lbID string, items map[string]osclient.PoolSummary) {
	var tree *model.Tree
	if m.loc.tree != nil && m.loc.tree.Root != nil && m.loc.tree.Root.ID == lbID {
		tree = m.loc.tree
	} else if entry, ok := m.cache.Peek(lbID); ok {
		tree = entry.Tree
	}
	if tree == nil || tree.Root == nil {
		return
	}
	for _, item := range items {
		pool := tree.Node(item.ID)
		if pool == nil {
			pool = model.NewNode(model.TypePool, item.ID, item.Name)
			pool.OwningLBID = lbID
			pool.Parent = tree.Root
			tree.Root.Children = append(tree.Root.Children, pool)
			tree.Attach(pool)
		} else if item.Name != "" {
			pool.Name = item.Name
		}
		pool.ProvisioningStatus = item.ProvisioningStatus
		pool.OperatingStatus = item.OperatingStatus
		pool.SetAttr("protocol", item.Protocol)
		pool.SetAttr("lb_algorithm", item.LBMethod)
		memberCount := 0
		for _, child := range pool.Children {
			if child.Type == model.TypeMember {
				memberCount++
			}
		}
		// Some list responses omit member bodies; use whichever source carries
		// the larger count rather than replacing status-tree data with zero.
		if item.MemberCount > memberCount {
			memberCount = item.MemberCount
		}
		pool.SetAttr("member_count", fmt.Sprintf("%d", memberCount))
		listenerIDs := map[string]struct{}{}
		for _, listenerID := range item.ListenerIDs {
			if listenerID != "" {
				listenerIDs[listenerID] = struct{}{}
			}
		}
		pool.SetAttr("listener_count", fmt.Sprintf("%d", len(listenerIDs)))
		for listenerID := range listenerIDs {
			if listener := tree.Node(listenerID); listener != nil {
				listener.AddRef("pool", pool)
			}
		}
	}
	if m.loc.node == tree.Root {
		m.allEntries = nodeEntries(tree.Root)
		m.applyFilters()
	} else if m.loc.node != nil && m.loc.node.Type == model.TypeListener && m.loc.node.OwningLBID == lbID {
		m.allEntries = locationEntries(m.loc.node)
		m.applyFilters()
	}
}

func (m Model) onProjects(msg projectsMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.overlay = overlayNone
		return m, m.setFlash(msg.err.Error(), true)
	}
	m.search.Prompt = "filter: "
	m.search.PromptStyle = m.st.filterPrompt
	m.search.SetValue("")
	m.search.Blur()
	m.projects = msg.projects
	m.projCursor = m.currentProjectCursor()
	m.overlay = overlayProject
	return m, nil
}

func (m Model) onSwitched(msg switchedMsg) (tea.Model, tea.Cmd) {
	activeWorkspace := m.activeWorkspace
	m.loading = false
	m.refreshing = false
	m.refreshLBID = ""
	m.refreshVIPLBs = nil
	m.refreshVIPFloatingIPs = nil
	m.refreshDetail = nil
	m.refreshHealthMonitor = nil
	m.refreshMonitorExpected = false
	m.refreshStats = nil
	m.refreshListenerStats = nil
	m.refreshFIP = nil
	m.refreshFIPExpected = false
	m.refreshAmphorae = nil
	m.refreshAmphoraeExpected = false
	m.refreshListeners = nil
	m.refreshListenersExpected = false
	m.refreshPools = nil
	m.refreshPoolsExpected = false
	m.refreshAutomatic = false
	m.statsSpinnerRunning = false
	if msg.err != nil {
		return m, m.setFlash(msg.err.Error(), true)
	}
	// A new project scope means a different visible object set: drop caches and
	// history so objects from the previous authorization context cannot leak in.
	m.project = msg.project
	m.allProjects = msg.all
	m.cache = cache.New(m.cfg.CacheSize, m.cfg.CacheTTL)
	m.lbStats = map[string]map[string]any{}
	m.lbStatsChanges = map[string]map[string]statChange{}
	m.lbStatsSampledAt = map[string]time.Time{}
	m.lbDetailLoading = map[string]bool{}
	m.lbStatsLoading = map[string]bool{}
	m.lbDetailErr = map[string]string{}
	m.lbStatsErr = map[string]string{}
	m.lbRelatedErr = map[string]string{}
	m.lbFreshness = map[string]overviewFreshness{}
	m.lbFIPLoading = map[string]bool{}
	m.lbFIPLoaded = map[string]bool{}
	m.lbAmphoraLoading = map[string]bool{}
	m.lbAmphoraLoaded = map[string]bool{}
	m.lbListenersLoading = map[string]bool{}
	m.lbListenersLoaded = map[string]bool{}
	m.lbPoolsLoading = map[string]bool{}
	m.lbPoolsLoaded = map[string]bool{}
	m.coeClusters = nil
	m.coeClustersLoaded = false
	m.coeClustersLoading = false
	m.coeSpinnerRunning = false
	m.coeClustersErr = ""
	m.coeClustersAt = time.Time{}
	m.autoStatsLoading = map[string]bool{}
	m.lbs, m.lbsLoaded = nil, false
	m.vipFloatingIPs = nil
	m.vipFloatingIPsLoaded = false
	m.vipFloatingIPsLoading = false
	m.vipFloatingIPsErr = ""
	// The top-level resource lists are scope-dependent too; drop their caches.
	m.listeners, m.listenersLoaded = nil, false
	m.pools, m.poolsLoaded = nil, false
	m.amphorae, m.amphoraeLoaded, m.amphoraeErr = nil, false, ""
	m.resetWorkspacesAt(activeWorkspace)
	m.overlay = overlayNone
	loadCmd := m.showTopLevelList(activeWorkspace.identity())
	if activeWorkspace != kindLB && activeWorkspace != kindVIP {
		// Listener, pool, and amphora rows label their owning load balancer by
		// name, which comes from the LB list rather than their own API response.
		loadCmd = tea.Batch(loadCmd, m.loadLBsCmd())
	}
	scope := "project " + projectLabel(msg.project)
	if msg.all {
		scope = "all projects"
	}
	if m.backend.SwitchCapability().GlobalAdmin {
		scope = "global admin · " + scope
	}
	return m, tea.Batch(loadCmd, m.setFlash("switched to "+scope, false))
}

// --- navigation & rendering ----------------------------------------------

// render resolves the current history entry into a location, fetching if needed.
func (m *Model) render() tea.Cmd {
	cur, ok := m.hist.current()
	if !ok {
		return nil
	}
	return m.showIdentity(cur.id)
}

func (m *Model) showAmphora(n *model.Node) tea.Cmd {
	entry, fresh := m.cache.Get(n.OwningLBID)
	if entry.Tree != nil && fresh {
		attachAmphora(entry.Tree, n)
		m.buildNodeLocation(n.Identity(), entry.Tree)
		return m.loadLBOverview()
	}
	m.loading, m.loadingWhat = true, "tree"
	return m.amphoraTreeCmd(n)
}

func attachAmphora(tree *model.Tree, amphora *model.Node) {
	if tree == nil || tree.Root == nil || amphora == nil || tree.Node(amphora.ID) != nil {
		return
	}
	amphora.Parent = tree.Root
	amphora.OwningLBID = tree.Root.ID
	tree.Root.Children = append(tree.Root.Children, amphora)
	tree.Attach(amphora)
}

func (m *Model) showIdentity(id model.Identity) tea.Cmd {
	if id.IsTopLevelList() {
		return m.showTopLevelList(id)
	}
	if id.Type == model.TypeAmphora {
		n := model.NewNode(model.TypeAmphora, id.ID, id.ID)
		n.OwningLBID = id.OwningLBID
		if cached, ok := m.cache.Peek(id.OwningLBID); ok && cached.Tree != nil {
			if existing := cached.Tree.Node(id.ID); existing != nil {
				n = existing
			}
		}
		if !n.DetailLoaded {
			for _, listed := range m.amphorae {
				if listed.ID == id.ID && listed.OwningLBID == id.OwningLBID {
					n = listed
					break
				}
			}
		}
		return m.showAmphora(n)
	}
	entry, fresh := m.cache.Get(id.OwningLBID)
	if entry.Tree != nil && fresh {
		m.applyKubernetesRelations(entry.Tree)
		m.buildNodeLocation(id, entry.Tree)
		return m.loadLBOverview()
	}
	delete(m.lbStats, id.OwningLBID)
	delete(m.lbStatsChanges, id.OwningLBID)
	delete(m.lbStatsSampledAt, id.OwningLBID)
	delete(m.lbDetailLoading, id.OwningLBID)
	delete(m.lbStatsLoading, id.OwningLBID)
	delete(m.lbDetailErr, id.OwningLBID)
	delete(m.lbStatsErr, id.OwningLBID)
	delete(m.lbRelatedErr, id.OwningLBID)
	delete(m.lbFreshness, id.OwningLBID)
	delete(m.lbFIPLoading, id.OwningLBID)
	delete(m.lbFIPLoaded, id.OwningLBID)
	delete(m.lbAmphoraLoading, id.OwningLBID)
	delete(m.lbAmphoraLoaded, id.OwningLBID)
	delete(m.lbListenersLoading, id.OwningLBID)
	delete(m.lbListenersLoaded, id.OwningLBID)
	delete(m.lbPoolsLoading, id.OwningLBID)
	delete(m.lbPoolsLoaded, id.OwningLBID)
	if id.Type == model.TypeListener {
		delete(m.lbStats, id.ID)
		delete(m.lbStatsChanges, id.ID)
		delete(m.lbStatsSampledAt, id.ID)
		delete(m.lbDetailLoading, id.ID)
		delete(m.lbStatsLoading, id.ID)
		delete(m.lbDetailErr, id.ID)
		delete(m.lbStatsErr, id.ID)
		delete(m.lbRelatedErr, id.ID)
		delete(m.lbFreshness, id.ID)
	}
	if id.Type == model.TypePool {
		delete(m.lbDetailLoading, id.ID)
		delete(m.lbDetailErr, id.ID)
		delete(m.lbRelatedErr, id.ID)
		delete(m.lbFreshness, id.ID)
	}
	if id.Type == model.TypeMember {
		delete(m.lbDetailLoading, id.ID)
		delete(m.lbDetailErr, id.ID)
		delete(m.lbRelatedErr, id.ID)
		delete(m.lbFreshness, id.ID)
	}
	if id.Type == model.TypeAmphora {
		delete(m.lbDetailLoading, id.ID)
		delete(m.lbDetailErr, id.ID)
		delete(m.lbFreshness, id.ID)
	}
	if id.Type == model.TypeHealthMonitor {
		delete(m.lbDetailLoading, id.ID)
		delete(m.lbDetailErr, id.ID)
		delete(m.lbRelatedErr, id.ID)
		delete(m.lbFreshness, id.ID)
	}
	m.loading, m.loadingWhat = true, "tree"
	return m.getTreeCmd(id.OwningLBID, id, false)
}

// showTopLevelList makes id the active top-level list, building its rows from
// already-loaded data or kicking off the load that will fill it in. VIPs and the
// LB list both source from the LB list; the other three load their own data.
func (m *Model) showTopLevelList(id model.Identity) tea.Cmd {
	m.loc = location{id: id}
	switch listKindOf(id) {
	case kindLB:
		if m.lbsLoaded {
			m.setTopLevelEntries()
			return nil
		}
		m.loading, m.loadingWhat = true, "load balancers"
		m.showLoadingList()
		return m.loadLBsCmd()
	case kindVIP:
		if m.lbsLoaded && m.vipFloatingIPsLoaded {
			m.setTopLevelEntries()
			return nil
		}
		m.loading, m.loadingWhat = true, "virtual IPs"
		m.showLoadingList()
		var cmds []tea.Cmd
		if !m.lbsLoaded {
			cmds = append(cmds, m.loadLBsCmd())
		}
		if !m.vipFloatingIPsLoaded && !m.vipFloatingIPsLoading {
			m.vipFloatingIPsLoading = true
			cmds = append(cmds, m.loadVIPFloatingIPsCmd(false))
		}
		switch len(cmds) {
		case 0:
			return nil
		case 1:
			return cmds[0]
		default:
			return tea.Batch(cmds...)
		}
	case kindListener:
		if m.listenersLoaded {
			m.setTopLevelEntries()
			return nil
		}
		m.loading, m.loadingWhat = true, "listeners"
		m.showLoadingList()
		return m.loadListenersCmd(false)
	case kindPool:
		if m.poolsLoaded {
			m.setTopLevelEntries()
			return nil
		}
		m.loading, m.loadingWhat = true, "pools"
		m.showLoadingList()
		return m.loadPoolsCmd(false)
	case kindAmphora:
		if m.amphoraeLoaded {
			m.setTopLevelEntries()
			return nil
		}
		m.loading, m.loadingWhat = true, "amphorae"
		m.showLoadingList()
		return m.loadAmphoraeListCmd(false)
	}
	return nil
}

// showLoadingList clears the rows so the body shows the loading indicator while a
// top-level list's data is in flight.
func (m *Model) showLoadingList() {
	m.allEntries = nil
	m.entries = nil
	m.cursor, m.top = 0, 0
	m.applyFilters()
}

// setTopLevelEntries rebuilds the visible rows for the active top-level list from
// currently-loaded data.
func (m *Model) setTopLevelEntries() {
	switch m.loc.listKind() {
	case kindVIP:
		m.allEntries = vipEntries(deriveVIPs(m.lbs, m.vipFloatingIPs))
	case kindListener:
		m.allEntries = listenerEntries(m.listeners, m.lbNameByID())
	case kindPool:
		m.allEntries = poolEntries(m.pools, m.lbNameByID())
	case kindAmphora:
		m.allEntries = amphoraEntries(m.amphorae, m.lbNameByID(), !m.allProjects)
	default:
		m.allEntries = lbEntries(m.lbs, m.allProjects)
	}
	m.entries = nil
	m.cursor, m.top = 0, 0
	m.applyFilters()
	m.restoreWorkspacePosition()
}

func (m *Model) buildNodeLocation(id model.Identity, tree *model.Tree) {
	node := tree.Node(id.ID)
	if node == nil {
		m.loc = location{id: id, tree: tree, dead: true}
		m.allEntries = nil
		m.hist.markDead()
		m.applyFilters()
		return
	}
	m.loc = location{id: id, node: node, tree: tree}
	m.allEntries = locationEntries(node)
	m.entries = nil
	m.rawContent, m.rawFormat = "", ""
	m.cursor, m.top = 0, 0
	m.applyFilters()
	m.restoreWorkspacePosition()
}

// applyFilters recomputes the visible rows from the substring filter and the
// status filter, then clamps the cursor.
func (m *Model) applyFilters() {
	var selected entrySelection
	keepSelection := m.cursor >= 0 && m.cursor < len(m.entries) && m.entries[m.cursor].selectable()
	if keepSelection {
		selected = m.entries[m.cursor].selection()
	}

	f := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var res []entry
	for _, e := range m.allEntries {
		if !m.status.match(e.oper, e.prov) {
			continue
		}
		if f != "" && !strings.Contains(e.filterText(), f) {
			continue
		}
		res = append(res, e)
	}
	if m.isLBOverview() || m.isListenerOverview() || m.isPoolOverview() {
		res = withRelatedGroupHeadings(res)
	}
	m.entries = res
	if keepSelection {
		for i := range m.entries {
			if m.entries[i].selectable() && m.entries[i].selection().equal(selected) {
				m.cursor = i
				m.ensureVisible()
				return
			}
		}
	}
	if next := nearestSelectableIndex(m.entries, m.cursor); next >= 0 {
		m.cursor = next
	} else {
		m.cursor = 0
	}
	m.ensureVisible()
}

func (m *Model) ensureVisible() {
	h := m.visibleRows()
	if h < 1 {
		h = 1
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	if m.top < 0 {
		m.top = 0
	}
	maxTop := len(m.entries) - h
	if maxTop < 0 {
		maxTop = 0
	}
	if m.top > maxTop {
		m.top = maxTop
	}
}

// setFlash sets the transient status line and schedules its clearing.
func (m *Model) setFlash(text string, isErr bool) tea.Cmd {
	m.flash, m.flashErr = text, isErr
	m.flashToken++
	return flashCmd(m.flashToken)
}

func (m *Model) clearFilter() {
	m.filter.SetValue("")
	m.filter.Blur()
	m.filtering = false
}

func projectLabel(p osclient.ProjectInfo) string {
	if p.Name != "" {
		return p.Name
	}
	if p.ID != "" {
		return p.ID
	}
	return "(unknown)"
}
