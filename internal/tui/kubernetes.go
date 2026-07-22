package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

const (
	capiLBPrefix       = "k8s-clusterapi-cluster-magnum-system-"
	capiLBSuffix       = "-kubeapi"
	coeClusterCacheTTL = 60 * time.Second
)

var kubeServiceNamePattern = regexp.MustCompile(
	`^kube_service_([[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12})_([^_]+)_(.+)$`,
)

// coeDetailState is one cluster's cached detail fetch: the loaded data plus the
// loading/error/freshness bookkeeping the overview and refresh logic need.
type coeDetailState struct {
	detail  osclient.COEClusterDetail
	loaded  bool
	loading bool
	err     string
	at      time.Time
}

type kubernetesLBKind int

const (
	kubernetesLBNone kubernetesLBKind = iota
	kubernetesLBAPI
	kubernetesLBService
)

type kubernetesLBInfo struct {
	kind        kubernetesLBKind
	clusterUUID string
	stackID     string
	namespace   string
	service     string
}

func inferKubernetesLB(name string) kubernetesLBInfo {
	if matches := kubeServiceNamePattern.FindStringSubmatch(name); len(matches) == 4 {
		return kubernetesLBInfo{
			kind: kubernetesLBService, clusterUUID: strings.ToLower(matches[1]),
			namespace: matches[2], service: matches[3],
		}
	}
	if strings.HasPrefix(name, capiLBPrefix) && strings.HasSuffix(name, capiLBSuffix) {
		stackID := strings.TrimSuffix(strings.TrimPrefix(name, capiLBPrefix), capiLBSuffix)
		if stackID != "" {
			return kubernetesLBInfo{kind: kubernetesLBAPI, stackID: stackID}
		}
	}
	return kubernetesLBInfo{}
}

func (m *Model) ensureCOEClustersCmd(force bool) tea.Cmd {
	if m.loc.tree == nil || m.loc.tree.Root == nil || inferKubernetesLB(m.loc.tree.Root.Name).kind == kubernetesLBNone {
		return nil
	}
	if m.coeClustersLoading {
		// A load is already in flight (e.g. the startup pre-warm); don't refetch.
		// Ensure the spinner animates while this view waits for it.
		if !m.coeSpinnerRunning {
			m.coeSpinnerRunning = true
			return m.coeSpinner.Tick
		}
		return nil
	}
	cacheFresh := m.coeClustersLoaded && !m.coeClustersAt.IsZero() && m.clock().Sub(m.coeClustersAt) < coeClusterCacheTTL
	if !force && cacheFresh {
		return nil
	}
	m.coeClustersLoading = true
	if !force {
		m.coeClustersErr = ""
	}
	m.coeSpinnerRunning = true
	return tea.Batch(m.startCOEClustersLoad(), m.coeSpinner.Tick)
}

// onCOEPreload starts the startup background pre-warm of the Magnum cluster
// list. Setting the in-flight flag here (rather than in New) lets a drill-in
// that happens before the list lands dedupe against it instead of refetching.
func (m Model) onCOEPreload() (tea.Model, tea.Cmd) {
	if m.coeClustersLoading || m.coeClustersLoaded {
		return m, nil
	}
	m.coeClustersLoading = true
	// Assign before returning: startCOEClustersLoad stores m.coeCancel, and Go
	// leaves the order of the plain `m` read vs. this call's side effects
	// unspecified, so the returned model must observe the mutation explicitly.
	cmd := m.startCOEClustersLoad()
	return m, cmd
}

// ensureCOEClusterDetailCmd lazily loads the slow per-cluster Magnum detail for
// the cluster the user is viewing. It is keyed and cached by UUID, so it no-ops
// when the detail is fresh or already in flight, and when the cluster is not yet
// identified (the list may still be resolving the UUID for an API-server LB).
func (m *Model) ensureCOEClusterDetailCmd(force bool) tea.Cmd {
	if m.loc.node == nil || m.loc.node.Type != model.TypeCOECluster {
		return nil
	}
	uuid := m.loc.node.Attrs["uuid"]
	if uuid == "" {
		return nil
	}
	state := m.coeClusterDetails[uuid]
	cacheFresh := state.loaded && !state.at.IsZero() && m.clock().Sub(state.at) < coeClusterCacheTTL
	if state.loading || (!force && cacheFresh) {
		return nil
	}
	state.loading = true
	if !force {
		state.err = ""
	}
	m.coeClusterDetails[uuid] = state
	m.coeSpinnerRunning = true
	return tea.Batch(m.getCOEClusterDetailCmd(uuid), m.coeSpinner.Tick)
}

func (m Model) onCOEClusterDetail(msg coeClusterDetailMsg) (tea.Model, tea.Cmd) {
	state := m.coeClusterDetails[msg.uuid]
	state.loading = false
	state.loaded = true
	state.at = m.clock()
	if msg.err != nil {
		state.err = msg.err.Error()
	} else {
		state.detail = msg.detail
		state.err = ""
	}
	m.coeClusterDetails[msg.uuid] = state
	if !m.coeClustersLoading && !m.anyCOEDetailLoading() {
		m.coeSpinnerRunning = false
	}
	if m.loc.node != nil && m.loc.node.Type == model.TypeCOECluster && m.loc.node.Attrs["uuid"] == msg.uuid {
		m.markFresh(m.loc.node.ID, sectionDetails)
	}
	return m, nil
}

func (m Model) anyCOEDetailLoading() bool {
	for _, s := range m.coeClusterDetails {
		if s.loading {
			return true
		}
	}
	return false
}

// loadedCOEDetail returns a cluster's detail only when it loaded successfully,
// so the overview shows the extra fields only once they are actually available.
func (m Model) loadedCOEDetail(uuid string) (osclient.COEClusterDetail, bool) {
	if uuid == "" {
		return osclient.COEClusterDetail{}, false
	}
	state, ok := m.coeClusterDetails[uuid]
	if !ok || !state.loaded || state.err != "" {
		return osclient.COEClusterDetail{}, false
	}
	return state.detail, true
}

// coeHealthSummary condenses Magnum's health_status_reason map — one entry per
// node plus an "api" entry — into a single readable line.
func coeHealthSummary(reason map[string]string) string {
	if len(reason) == 0 {
		return ""
	}
	var ready, total int
	api := ""
	for key, value := range reason {
		if strings.EqualFold(key, "api") {
			api = value
			continue
		}
		if strings.HasSuffix(key, ".Ready") {
			total++
			if strings.EqualFold(value, "true") {
				ready++
			}
		}
	}
	var parts []string
	if total > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d nodes ready", ready, total))
	}
	if api != "" {
		parts = append(parts, "api: "+api)
	}
	return strings.Join(parts, " · ")
}

func (m *Model) applyKubernetesRelations(tree *model.Tree) {
	if tree == nil || tree.Root == nil {
		return
	}
	info := inferKubernetesLB(tree.Root.Name)
	if info.kind == kubernetesLBNone {
		return
	}

	cluster, found := matchCOECluster(info, tree.Meta.ProjectID, m.coeClusters)
	if found && cluster.ProjectID == "" {
		// Magnum's cluster-list response commonly omits project_id. The related
		// cluster is identified from this load balancer, whose Octavia metadata
		// carries the authoritative owning project even in all-projects mode.
		cluster.ProjectID = tree.Meta.ProjectID
		if cluster.ProjectID == "" && !m.allProjects {
			cluster.ProjectID = m.project.ID
		}
	}
	clusterNode := m.coeClusterNode(tree.Root.ID, info, cluster, found)
	tree.ReplaceChildrenOfType(tree.Root, model.TypeCOECluster, []*model.Node{clusterNode})

	var services []*model.Node
	if info.kind == kubernetesLBService {
		service := model.NewNode(
			model.TypeKubeService,
			tree.Root.ID+"/kubernetes-service/"+info.namespace+"/"+info.service,
			info.namespace+"/"+info.service,
		)
		service.OwningLBID = tree.Root.ID
		service.DetailLoaded = true
		service.SetAttr("namespace", info.namespace)
		service.SetAttr("service_name", info.service)
		service.SetAttr("cluster_uuid", info.clusterUUID)
		service.SetAttr("cluster_name", clusterDisplayName(cluster, found, m.coeClustersLoaded, m.coeClustersErr))
		service.Raw = map[string]any{
			"name": info.service, "namespace": info.namespace,
			"cluster_uuid": info.clusterUUID, "cluster_name": service.Attrs["cluster_name"],
			"source": "inferred from Octavia load balancer name",
		}
		services = []*model.Node{service}
	}
	tree.ReplaceChildrenOfType(tree.Root, model.TypeKubeService, services)
}

func matchCOECluster(info kubernetesLBInfo, projectID string, items []osclient.COECluster) (osclient.COECluster, bool) {
	var fallback *osclient.COECluster
	for i := range items {
		item := &items[i]
		matches := info.kind == kubernetesLBService && strings.EqualFold(item.UUID, info.clusterUUID)
		matches = matches || info.kind == kubernetesLBAPI && item.StackID == info.stackID
		if !matches {
			continue
		}
		if projectID != "" && item.ProjectID == projectID {
			return *item, true
		}
		if item.ProjectID == "" || projectID == "" {
			copy := *item
			fallback = &copy
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return osclient.COECluster{}, false
}

func (m Model) coeClusterNode(lbID string, info kubernetesLBInfo, cluster osclient.COECluster, found bool) *model.Node {
	id := lbID + "/coe-cluster"
	name := clusterDisplayName(cluster, found, m.coeClustersLoaded, m.coeClustersErr)
	n := model.NewNode(model.TypeCOECluster, id, name)
	n.OwningLBID = lbID
	n.DetailLoaded = true
	if found {
		n.OperatingStatus = cluster.HealthStatus
		n.ProvisioningStatus = cluster.Status
		n.SetAttr("uuid", cluster.UUID)
		n.SetAttr("stack_id", cluster.StackID)
		n.SetAttr("project_id", cluster.ProjectID)
		n.SetAttr("cluster_template_id", cluster.ClusterTemplateID)
		n.SetAttr("keypair", cluster.KeyPair)
		n.SetAttr("node_count", strconv.Itoa(cluster.NodeCount))
		n.SetAttr("master_count", strconv.Itoa(cluster.MasterCount))
		n.SetAttr("flavor_id", cluster.FlavorID)
		n.SetAttr("master_flavor_id", cluster.MasterFlavorID)
		n.Raw = cluster
		return n
	}
	if info.clusterUUID != "" {
		n.SetAttr("uuid", info.clusterUUID)
	}
	if info.stackID != "" {
		n.SetAttr("stack_id", info.stackID)
	}
	n.SetAttr("lookup_state", name)
	n.Raw = map[string]any{
		"uuid": info.clusterUUID, "stack_id": info.stackID,
		"lookup_state": name,
	}
	return n
}

func clusterDisplayName(cluster osclient.COECluster, found, loaded bool, lookupErr string) string {
	if found {
		if strings.TrimSpace(cluster.Name) != "" {
			return cluster.Name
		}
		return "unnamed cluster"
	}
	if lookupErr != "" {
		return "cannot obtain cluster data"
	}
	if loaded {
		return "unknown"
	}
	return "obtaining cluster data…"
}

func coeClusterSummary(n *model.Node) string {
	var parts []string
	if stackID := strings.TrimSpace(n.Attrs["stack_id"]); stackID != "" {
		parts = append(parts, stackID)
	}
	if n.OperatingStatus != "" {
		parts = append(parts, n.OperatingStatus)
	}
	if n.ProvisioningStatus != "" {
		parts = append(parts, n.ProvisioningStatus)
	}
	return strings.Join(parts, " · ")
}

func kubernetesServiceSummary(n *model.Node) string {
	cluster := strings.TrimSpace(n.Attrs["cluster_name"])
	if cluster == "" {
		return ""
	}
	return "cluster " + cluster
}

func (m Model) coeClusterDetailGroups() []overviewGroup {
	n := m.loc.node
	name := n.Name
	if n.Attrs["lookup_state"] != "" {
		name = ""
	}
	detail, hasDetail := m.loadedCOEDetail(n.Attrs["uuid"])

	projectID, projectName := n.Attrs["project_id"], ""
	if m.loc.tree != nil {
		if projectID == "" {
			projectID = m.loc.tree.Meta.ProjectID
		}
		projectName = m.loc.tree.Meta.ProjectName
	}

	state := []overviewField{
		{label: "Health", value: displayValue(n.OperatingStatus), status: true},
		{label: "Status", value: displayValue(n.ProvisioningStatus), status: true},
	}
	if hasDetail {
		if summary := coeHealthSummary(detail.HealthStatusReason); summary != "" {
			state = append(state, overviewField{label: "Node health", value: summary})
		}
		if reason := strings.TrimSpace(detail.StatusReason); reason != "" {
			state = append(state, overviewField{label: "Status reason", value: reason})
		}
	}

	groups := []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Name", value: displayValue(name)},
			{label: "UUID", value: displayValue(n.Attrs["uuid"])},
			{label: "CAPI identifier", value: displayValue(n.Attrs["stack_id"])},
			{label: "Project name", value: displayValue(projectName)},
			{label: "Project ID", value: displayValue(projectID)},
		}},
		{title: "STATE", fields: state},
		{title: "CAPACITY", fields: []overviewField{
			{label: "Control-plane nodes", value: displayValue(n.Attrs["master_count"])},
			{label: "Worker nodes", value: displayValue(n.Attrs["node_count"])},
		}},
	}

	if hasDetail {
		kube := []overviewField{
			{label: "API endpoint", value: displayValue(detail.APIAddress)},
			{label: "Kubernetes version", value: displayValue(detail.COEVersion)},
		}
		if len(detail.MasterAddresses) > 0 {
			kube = append(kube, overviewField{label: "Control-plane IPs", value: strings.Join(detail.MasterAddresses, ", ")})
		}
		if len(detail.NodeAddresses) > 0 {
			kube = append(kube, overviewField{label: "Worker IPs", value: strings.Join(detail.NodeAddresses, ", ")})
		}
		groups = append(groups, overviewGroup{title: "KUBERNETES", fields: kube})
	}

	config := []overviewField{
		{label: "Control-plane flavor", value: displayValue(n.Attrs["master_flavor_id"])},
		{label: "Worker flavor", value: displayValue(n.Attrs["flavor_id"])},
		{label: "Cluster template ID", value: displayValue(n.Attrs["cluster_template_id"])},
		{label: "Key pair", value: displayValue(n.Attrs["keypair"])},
	}
	if hasDetail {
		config = append(config,
			overviewField{label: "API load balancer", value: yesNoValue(strconv.FormatBool(detail.MasterLBEnabled))},
			overviewField{label: "Floating IP", value: yesNoValue(strconv.FormatBool(detail.FloatingIPEnabled))},
		)
		if detail.FixedNetwork != "" {
			config = append(config, overviewField{label: "Fixed network", value: detail.FixedNetwork})
		}
		if detail.FixedSubnet != "" {
			config = append(config, overviewField{label: "Fixed subnet", value: detail.FixedSubnet})
		}
	}
	groups = append(groups, overviewGroup{title: "CONFIGURATION", fields: config})

	if hasDetail && (detail.CreatedAt != "" || detail.UpdatedAt != "") {
		groups = append(groups, overviewGroup{title: "LIFECYCLE", fields: []overviewField{
			{label: "Created", value: displayTimestamp(detail.CreatedAt)},
			{label: "Updated", value: displayTimestamp(detail.UpdatedAt)},
		}})
	}

	if labels := coeClusterLabelFields(n); len(labels) > 0 {
		groups = append(groups, overviewGroup{title: "LABELS", fields: labels})
	}
	return groups
}

func coeClusterLabelFields(n *model.Node) []overviewField {
	if n == nil {
		return nil
	}
	cluster, ok := n.Raw.(osclient.COECluster)
	if !ok || len(cluster.Labels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cluster.Labels))
	for key := range cluster.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]overviewField, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, overviewField{label: key, value: displayValue(cluster.Labels[key])})
	}
	return fields
}

func (m Model) kubernetesServiceDetailFields() []overviewField {
	n := m.loc.node
	return []overviewField{
		{label: "Name", value: displayValue(n.Attrs["service_name"])},
		{label: "Namespace", value: displayValue(n.Attrs["namespace"])},
		{label: "Cluster name", value: displayValue(n.Attrs["cluster_name"])},
		{label: "Cluster UUID", value: displayValue(n.Attrs["cluster_uuid"])},
		{label: "Source", value: "inferred from load balancer name"},
	}
}

func (m Model) simpleKubernetesOverviewLines(h int) []string {
	if h <= 0 || m.loc.node == nil {
		return nil
	}
	if m.loc.node.Type == model.TypeCOECluster {
		return m.coeClusterOverviewLines(h)
	}
	title := m.kubernetesDetailTitle("KUBERNETES SERVICE DETAILS")
	fields := m.kubernetesServiceDetailFields()
	lines := []string{""}
	if h > 1 {
		lines = append(lines, strings.Split(m.renderOverviewPanel(m.clip(title), fields, m.width, h-1), "\n")...)
	}
	return padOverviewLines(lines, h)
}

func (m Model) coeClusterOverviewLines(h int) []string {
	lines := []string{""}
	if h <= 1 {
		return padOverviewLines(lines, h)
	}
	budget := h - 1
	content := []string{m.clip(m.kubernetesDetailTitle("COE CLUSTER DETAILS"))}
	groups := m.coeClusterDetailGroups()
	if m.width >= 90 {
		gap := 3
		available := m.width - gap
		leftWidth := available / 2
		rightWidth := available - leftWidth
		i := 0
		for ; i+1 < len(groups); i += 2 {
			if i > 0 {
				content = append(content, "")
			}
			content = append(content, strings.Split(m.renderOverviewGroupPair(groups[i], groups[i+1], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		}
		if i < len(groups) {
			if i > 0 {
				content = append(content, "")
			}
			content = append(content, strings.Split(m.renderOverviewGroup(groups[i], m.width, m.subsectionHeading), "\n")...)
		}
	} else {
		for i, group := range groups {
			if i > 0 {
				content = append(content, "")
			}
			content = append(content, strings.Split(m.renderOverviewGroup(group, m.width, m.subsectionHeading), "\n")...)
		}
	}
	lines = append(lines, limitLines(content, budget)...)
	return padOverviewLines(lines, h)
}

func (m Model) kubernetesDetailTitle(title string) string {
	rendered := m.st.panelTitle.Render(title)
	switch {
	case m.coeClustersLoading:
		rendered += "  " + m.coeSpinner.View() + " " + m.st.disabled.Render("obtaining cluster data…")
	case m.coeClustersErr != "":
		rendered += "  " + m.st.flashErr.Render("[cluster data unavailable]")
	default:
		// Once the cluster list has resolved, reflect the slow per-cluster detail
		// fetch (only the COE cluster view has one).
		if n := m.loc.node; n != nil && n.Type == model.TypeCOECluster {
			switch st := m.coeClusterDetails[n.Attrs["uuid"]]; {
			case st.loading:
				rendered += "  " + m.coeSpinner.View() + " " + m.st.disabled.Render("loading details…")
			case st.err != "":
				rendered += "  " + m.st.flashErr.Render("[details unavailable]")
			}
		}
	}
	return rendered
}

func padOverviewLines(lines []string, h int) []string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}
