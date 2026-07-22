package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

const testClusterUUID = "71244d81-5d8c-4228-9fdc-793fde6c27b7"

func testCOECluster() osclient.COECluster {
	return osclient.COECluster{
		UUID: testClusterUUID, Name: "clusterapi", ProjectID: "p1", StackID: "kube-slzjy",
		ClusterTemplateID: "template-1", KeyPair: "Openstack Admin",
		NodeCount: 3, MasterCount: 3, FlavorID: "worker-flavor", MasterFlavorID: "master-flavor",
		Status: "UPDATE_COMPLETE", HealthStatus: "HEALTHY",
	}
}

func TestInferKubernetesLoadBalancerNames(t *testing.T) {
	service := inferKubernetesLB("kube_service_" + testClusterUUID + "_tenant-layer_nginx-internet-ingress-nginx-controller")
	if service.kind != kubernetesLBService || service.clusterUUID != testClusterUUID || service.namespace != "tenant-layer" ||
		service.service != "nginx-internet-ingress-nginx-controller" {
		t.Fatalf("service inference = %+v", service)
	}

	api := inferKubernetesLB("k8s-clusterapi-cluster-magnum-system-kube-slzjy-kubeapi")
	if api.kind != kubernetesLBAPI || api.stackID != "kube-slzjy" {
		t.Fatalf("API inference = %+v", api)
	}
	if got := inferKubernetesLB("lb01"); got.kind != kubernetesLBNone {
		t.Fatalf("ordinary LB inference = %+v", got)
	}
}

func TestKubernetesServiceAddsClusterAndServiceRelatedObjects(t *testing.T) {
	backend := &fakeBackend{coeClusters: []osclient.COECluster{testCOECluster()}}
	m := New(backend, Config{})
	m.coeClusters = backend.coeClusters
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_tenant-layer_nginx-internet-ingress-nginx-controller"
	m.loc = location{id: tree.Root.Identity(), node: tree.Root, tree: tree}
	m.applyKubernetesRelations(tree)
	m.allEntries = locationEntries(tree.Root)
	m.applyFilters()

	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	service := firstChildOfType(tree.Root, model.TypeKubeService)
	if cluster == nil || cluster.Name != "clusterapi" || cluster.Attrs["stack_id"] != "kube-slzjy" {
		t.Fatalf("COE cluster relation = %+v", cluster)
	}
	if service == nil || service.Name != "tenant-layer/nginx-internet-ingress-nginx-controller" || service.Attrs["cluster_name"] != "clusterapi" {
		t.Fatalf("Kubernetes service relation = %+v", service)
	}

	var headings []string
	for _, item := range m.entries {
		if item.kind == entGroup {
			headings = append(headings, item.label)
		}
	}
	joined := strings.Join(headings, ",")
	if !strings.Contains(joined, "COE CLUSTERS 1") || !strings.Contains(joined, "KUBERNETES SERVICES 1") {
		t.Fatalf("related headings = %v", headings)
	}
}

func TestCAPIAPILoadBalancerMatchesClusterStackID(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.coeClusters = []osclient.COECluster{testCOECluster()}
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Root.Name = "k8s-clusterapi-cluster-magnum-system-kube-slzjy-kubeapi"
	m.applyKubernetesRelations(tree)

	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	if cluster == nil || cluster.Name != "clusterapi" {
		t.Fatalf("COE cluster relation = %+v", cluster)
	}
	if service := firstChildOfType(tree.Root, model.TypeKubeService); service != nil {
		t.Fatalf("API load balancer gained service relation: %+v", service)
	}
}

func TestCOEClusterProjectFallsBackToOwningLoadBalancer(t *testing.T) {
	cluster := testCOECluster()
	cluster.ProjectID = ""
	m := New(&fakeBackend{}, Config{AllProjects: true})
	m.coeClusters = []osclient.COECluster{cluster}
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Meta.ProjectID = "foreign-project"
	tree.Root.Name = "kube_service_" + testClusterUUID + "_default_web"

	m.applyKubernetesRelations(tree)
	related := firstChildOfType(tree.Root, model.TypeCOECluster)
	if related == nil || related.Attrs["project_id"] != "foreign-project" {
		t.Fatalf("COE cluster project = %+v, want owning LB project", related)
	}
}

func TestCOEClusterProjectFallsBackToActiveScopedProject(t *testing.T) {
	cluster := testCOECluster()
	cluster.ProjectID = ""
	backend := &fakeBackend{}
	m := New(backend, Config{})
	m.project = osclient.ProjectInfo{ID: "scoped-project", Name: "scoped"}
	m.coeClusters = []osclient.COECluster{cluster}
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Meta.ProjectID = ""
	tree.Root.Name = "kube_service_" + testClusterUUID + "_default_web"

	m.applyKubernetesRelations(tree)
	related := firstChildOfType(tree.Root, model.TypeCOECluster)
	if related == nil || related.Attrs["project_id"] != "scoped-project" {
		t.Fatalf("COE cluster project = %+v, want active scoped project", related)
	}
}

func TestCOEClusterListIsCachedAndRefreshable(t *testing.T) {
	backend := &fakeBackend{coeClusters: []osclient.COECluster{testCOECluster()}}
	m := New(backend, Config{})
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_default_web"
	m.loc = location{id: tree.Root.Identity(), node: tree.Root, tree: tree}

	cmd := m.ensureCOEClustersCmd(false)
	if cmd == nil {
		t.Fatal("first Kubernetes LB should start Magnum cluster listing")
	}
	if duplicate := m.ensureCOEClustersCmd(false); duplicate != nil {
		t.Fatal("in-flight cluster listing should be deduplicated")
	}
	msg := runCOERequest(t, cmd)
	if backend.coeDeadline <= requestTimeout {
		t.Fatalf("Magnum deadline = %s, want longer than default %s", backend.coeDeadline, requestTimeout)
	}
	next, _ := m.onCOEClusters(msg)
	m = next.(Model)
	if backend.coeCalls != 1 {
		t.Fatalf("cluster list calls = %d, want 1", backend.coeCalls)
	}
	if cached := m.ensureCOEClustersCmd(false); cached != nil {
		t.Fatal("loaded cluster list should be reused")
	}
	now = now.Add(coeClusterCacheTTL - time.Second)
	if cached := m.ensureCOEClustersCmd(false); cached != nil {
		t.Fatal("cluster list should remain cached before the 60-second TTL")
	}
	now = now.Add(time.Second)
	expired := m.ensureCOEClustersCmd(false)
	if expired == nil {
		t.Fatal("expired cluster list should be reloaded")
	}
	expiredMsg := runCOERequest(t, expired)
	next, _ = m.onCOEClusters(expiredMsg)
	m = next.(Model)
	if backend.coeCalls != 2 {
		t.Fatalf("cluster list calls after TTL = %d, want 2", backend.coeCalls)
	}
	refresh := m.ensureCOEClustersCmd(true)
	if refresh == nil {
		t.Fatal("forced refresh should reload the cluster list")
	}
	_ = runCOERequest(t, refresh)
	if backend.coeCalls != 3 {
		t.Fatalf("cluster list calls after manual refresh = %d, want 3", backend.coeCalls)
	}
}

func TestCOEClusterLoadHasIndependentSpinner(t *testing.T) {
	m := New(&fakeBackend{coeClusters: []osclient.COECluster{testCOECluster()}}, Config{})
	tree := newTree()
	tree.Root.Name = "k8s-clusterapi-cluster-magnum-system-kube-slzjy-kubeapi"
	m.loc = location{id: tree.Root.Identity(), node: tree.Root, tree: tree}

	cmd := m.ensureCOEClustersCmd(false)
	batch := coeCommandBatch(t, cmd)
	if !m.coeSpinnerRunning || !m.coeClustersLoading || m.loading {
		t.Fatalf("COE spinner/loading state: spinner=%v coe=%v global=%v", m.coeSpinnerRunning, m.coeClustersLoading, m.loading)
	}
	before := m.coeSpinner.View()
	next, tickCmd := m.Update(batch[1]())
	m = next.(Model)
	if tickCmd == nil || m.coeSpinner.View() == before {
		t.Fatal("COE spinner did not advance independently")
	}

	next, _ = m.onCOEClusters(batch[0]().(coeClustersMsg))
	m = next.(Model)
	if m.coeSpinnerRunning || m.coeClustersLoading {
		t.Fatal("COE spinner should stop when cluster listing completes")
	}
}

func TestCOEDetailAutomaticRefreshUsesMagnumCadence(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	m.coeClusters = []osclient.COECluster{testCOECluster()}
	m.coeClustersLoaded = true
	m.coeClustersAt = now.Add(-coeClusterCacheTTL)
	tree := newTree()
	tree.Root.Name = "k8s-clusterapi-cluster-magnum-system-kube-slzjy-kubeapi"
	m.applyKubernetesRelations(tree)
	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	m.loc = location{id: cluster.Identity(), node: cluster, tree: tree}

	next, cmd := m.beginRefresh(true)
	m = next
	if cmd == nil || m.refreshing || m.loading || !m.coeClustersLoading {
		t.Fatalf("automatic COE refresh used Octavia transaction: cmd=%v refreshing=%v loading=%v coe=%v", cmd != nil, m.refreshing, m.loading, m.coeClustersLoading)
	}
	if got := m.autoRefreshCadence(); got != "60s" {
		t.Fatalf("COE auto-refresh cadence = %q, want 60s", got)
	}
}

func TestKubernetesRelationsDegradeWhenMagnumFails(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.coeClustersLoaded = true
	m.coeClustersErr = errors.New("timeout").Error()
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_default_web"
	m.applyKubernetesRelations(tree)

	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	service := firstChildOfType(tree.Root, model.TypeKubeService)
	if cluster == nil || cluster.Name != "cannot obtain cluster data" {
		t.Fatalf("cluster fallback = %+v", cluster)
	}
	if service == nil || service.Attrs["cluster_name"] != "cannot obtain cluster data" || service.Name != "default/web" {
		t.Fatalf("service fallback = %+v", service)
	}
}

func TestKubernetesRelatedObjectsHaveSimpleDetailViews(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.width, m.height = 120, 30
	m.coeClusters = []osclient.COECluster{testCOECluster()}
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_tenant-layer_web"
	m.applyKubernetesRelations(tree)

	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	m.loc = location{id: cluster.Identity(), node: cluster, tree: tree}
	clusterView := ansiRE.ReplaceAllString(strings.Join(m.simpleKubernetesOverviewLines(20), "\n"), "")
	for _, want := range []string{"COE CLUSTER DETAILS", "clusterapi", "kube-slzjy", "HEALTHY", "UPDATE_COMPLETE"} {
		if !strings.Contains(clusterView, want) {
			t.Errorf("cluster detail missing %q:\n%s", want, clusterView)
		}
	}
	for _, group := range []string{"IDENTITY", "STATE", "CAPACITY", "CONFIGURATION"} {
		if !strings.Contains(clusterView, group) {
			t.Errorf("cluster detail missing %s group:\n%s", group, clusterView)
		}
	}

	service := firstChildOfType(tree.Root, model.TypeKubeService)
	m.loc = location{id: service.Identity(), node: service, tree: tree}
	serviceView := strings.Join(m.simpleKubernetesOverviewLines(12), "\n")
	for _, want := range []string{"KUBERNETES SERVICE DETAILS", "tenant-layer", "web", "clusterapi", testClusterUUID} {
		if !strings.Contains(serviceView, want) {
			t.Errorf("service detail missing %q:\n%s", want, serviceView)
		}
	}
}

func TestCOEClusterDetailListsAllLabelsSorted(t *testing.T) {
	cluster := testCOECluster()
	cluster.Labels = map[string]string{
		"kube_tag":          "v1.32.8",
		"availability_zone": "pl1-a",
		"auto_scaling":      "True",
	}
	m := New(&fakeBackend{}, Config{})
	m.width, m.height = 120, 40
	m.coeClusters = []osclient.COECluster{cluster}
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Root.Name = "k8s-clusterapi-cluster-magnum-system-kube-slzjy-kubeapi"
	m.applyKubernetesRelations(tree)
	related := firstChildOfType(tree.Root, model.TypeCOECluster)
	m.loc = location{id: related.Identity(), node: related, tree: tree}

	view := ansiRE.ReplaceAllString(strings.Join(m.simpleKubernetesOverviewLines(35), "\n"), "")
	previous := strings.Index(view, "LABELS")
	if previous < 0 {
		t.Fatalf("cluster detail missing LABELS section:\n%s", view)
	}
	for _, want := range []string{"auto_scaling", "True", "availability_zone", "pl1-a", "kube_tag", "v1.32.8"} {
		index := strings.Index(view, want)
		if index < 0 {
			t.Errorf("cluster detail missing label data %q:\n%s", want, view)
		}
		if index < previous {
			t.Errorf("cluster label data is not sorted at %q:\n%s", want, view)
		}
		previous = index
	}
}

func TestCOEClusterLoadingStateIsNotRepeatedAsName(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.width, m.height = 120, 30
	m.coeClustersLoading = true
	tree := newTree()
	tree.Root.Name = "k8s-clusterapi-cluster-magnum-system-kube-bl0a2-kubeapi"
	m.applyKubernetesRelations(tree)
	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	m.loc = location{id: cluster.Identity(), node: cluster, tree: tree}

	view := ansiRE.ReplaceAllString(strings.Join(m.simpleKubernetesOverviewLines(20), "\n"), "")
	if count := strings.Count(view, "obtaining cluster data…"); count != 1 {
		t.Fatalf("loading state appears %d times, want only the title:\n%s", count, view)
	}
	nameUnavailable := false
	for _, line := range strings.Split(view, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Name" && fields[1] == "—" {
			nameUnavailable = true
			break
		}
	}
	if !nameUnavailable {
		t.Fatalf("unavailable cluster name should render as an em dash:\n%s", view)
	}
	for _, group := range []string{"IDENTITY", "STATE", "CAPACITY", "CONFIGURATION"} {
		if !strings.Contains(view, group) {
			t.Errorf("loading cluster detail missing %s group:\n%s", group, view)
		}
	}
}

func TestClusterResponseReplacesOpenPlaceholderWithoutBreakingNavigation(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_default_web"
	m.applyKubernetesRelations(tree)
	placeholder := firstChildOfType(tree.Root, model.TypeCOECluster)
	m.loc = location{id: placeholder.Identity(), node: placeholder, tree: tree}

	next, _ := m.onCOEClusters(coeClustersMsg{items: []osclient.COECluster{testCOECluster()}, projectID: "p1"})
	m = next.(Model)
	if m.loc.dead || m.loc.node == nil || m.loc.node.Type != model.TypeCOECluster || m.loc.node.Name != "clusterapi" {
		t.Fatalf("open cluster after enrichment: dead=%v node=%+v clusters=%+v err=%q", m.loc.dead, m.loc.node, m.coeClusters, m.coeClustersErr)
	}
	if m.loc.id.ID != placeholder.ID || m.loc.node.ID != placeholder.ID {
		t.Fatalf("cluster navigation identity changed from %q to location=%q node=%q", placeholder.ID, m.loc.id.ID, m.loc.node.ID)
	}
}

func firstChildOfType(parent *model.Node, kind model.NodeType) *model.Node {
	for _, child := range parent.Children {
		if child.Type == kind {
			return child
		}
	}
	return nil
}

func coeCommandBatch(t *testing.T, cmd tea.Cmd) tea.BatchMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected COE command")
	}
	result := cmd()
	batch, ok := result.(tea.BatchMsg)
	if !ok {
		t.Fatalf("COE command result = %T, want two-command batch", result)
	}
	if len(batch) != 2 {
		t.Fatalf("COE command has %d children, want 2", len(batch))
	}
	return batch
}

func runCOERequest(t *testing.T, cmd tea.Cmd) coeClustersMsg {
	t.Helper()
	batch := coeCommandBatch(t, cmd)
	result := batch[0]()
	msg, ok := result.(coeClustersMsg)
	if !ok {
		t.Fatalf("COE request result = %T", result)
	}
	return msg
}

func testCOEClusterDetail() osclient.COEClusterDetail {
	return osclient.COEClusterDetail{
		APIAddress: "https://100.104.48.250:6443",
		COEVersion: "v1.32.8",
		HealthStatusReason: map[string]string{
			"kube-slzjy-2dm89-vd8bb.Ready":    "True",
			"kube-slzjy-2dm89-x2xqn.Ready":    "True",
			"kube-slzjy-default-worker.Ready": "False",
			"api":                             "ok",
		},
		FixedNetwork:      "0390a0b4-8823-44bd-b39d-e67abfef70cf",
		FloatingIPEnabled: false,
		MasterLBEnabled:   true,
		CreatedAt:         "2025-10-22T14:28:05Z",
		UpdatedAt:         "2026-06-30T13:31:54Z",
	}
}

func TestCOEClusterDetailEnrichment(t *testing.T) {
	uuid := testClusterUUID
	backend := &fakeBackend{
		coeClusters: []osclient.COECluster{testCOECluster()},
		coeDetails:  map[string]osclient.COEClusterDetail{uuid: testCOEClusterDetail()},
	}
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	m := New(backend, Config{})
	m.clock = func() time.Time { return now }
	m.width, m.height = 120, 40
	m.coeClusters = backend.coeClusters
	m.coeClustersLoaded = true
	tree := newTree()
	tree.Root.Name = "kube_service_" + uuid + "_tenant-layer_web"
	m.applyKubernetesRelations(tree)
	cluster := firstChildOfType(tree.Root, model.TypeCOECluster)
	m.loc = location{id: cluster.Identity(), node: cluster, tree: tree}

	before := ansiRE.ReplaceAllString(strings.Join(m.simpleKubernetesOverviewLines(30), "\n"), "")
	if strings.Contains(before, "API endpoint") {
		t.Fatalf("detail-only fields must not appear before the detail loads:\n%s", before)
	}

	if cmd := (&m).ensureCOEClusterDetailCmd(false); cmd == nil {
		t.Fatal("first landing on a COE cluster should trigger a detail fetch")
	}
	if !m.coeClusterDetails[uuid].loading {
		t.Fatal("detail state should be marked loading after triggering the fetch")
	}

	msg := m.getCOEClusterDetailCmd(uuid)().(coeClusterDetailMsg)
	if backend.coeGetCalls == 0 {
		t.Fatal("GetCOECluster was not called")
	}
	next, _ := m.onCOEClusterDetail(msg)
	m = next.(Model)

	view := ansiRE.ReplaceAllString(strings.Join(m.simpleKubernetesOverviewLines(30), "\n"), "")
	for _, want := range []string{
		"KUBERNETES", "API endpoint", "https://100.104.48.250:6443",
		"Kubernetes version", "v1.32.8",
		"Node health", "2/3 nodes ready", "api: ok",
		"API load balancer", "Floating IP",
		"Fixed network", "LIFECYCLE", "Created", "2025-10-22",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("enriched cluster detail missing %q:\n%s", want, view)
		}
	}

	// The cached detail must not refetch on a subsequent landing.
	if cmd := (&m).ensureCOEClusterDetailCmd(false); cmd != nil {
		t.Error("a fresh cached detail should not trigger a refetch")
	}
}

func TestStartupBackgroundLoadsCOEClusters(t *testing.T) {
	backend := &fakeBackend{coeClusters: []osclient.COECluster{testCOECluster()}}
	m := New(backend, Config{})

	// Start-up batch must include the COE pre-warm trigger.
	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init should return a batch of commands")
	}
	var preloaded bool
	for _, c := range batch {
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func(cmd tea.Cmd) { done <- cmd() }(c)
		select {
		case got := <-done:
			if _, ok := got.(coePreloadMsg); ok {
				preloaded = true
			}
		case <-time.After(500 * time.Millisecond):
		}
		if preloaded {
			break
		}
	}
	if !preloaded {
		t.Fatal("startup did not dispatch the COE cluster pre-warm")
	}

	// The pre-warm marks the load in flight (so a drill-in dedupes) and fetches.
	next, cmd := m.onCOEPreload()
	m = next.(Model)
	if !m.coeClustersLoading {
		t.Fatal("pre-warm should mark the COE list load in flight")
	}
	if cmd == nil {
		t.Fatal("pre-warm should dispatch the COE list load")
	}
	msg, ok := cmd().(coeClustersMsg)
	if !ok {
		t.Fatalf("pre-warm command should load the COE list, got %T", cmd())
	}
	updated, _ := m.onCOEClusters(msg)
	if got := updated.(Model); !got.coeClustersLoaded || got.coeClustersLoading || len(got.coeClusters) != 1 {
		t.Fatalf("startup COE list not stored: loaded=%v loading=%v n=%d", got.coeClustersLoaded, got.coeClustersLoading, len(got.coeClusters))
	}
}

func TestDrillInDuringPrewarmDedupesAndAnimates(t *testing.T) {
	backend := &fakeBackend{coeClusters: []osclient.COECluster{testCOECluster()}}
	m := New(backend, Config{})
	// Pre-warm is in flight (as at startup, before the list has landed).
	next, _ := m.onCOEPreload()
	m = next.(Model)

	// Drill into a k8s LB whose COE list is still loading.
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_tenant-layer_web"
	m.loc = location{id: tree.Root.Identity(), node: tree.Root, tree: tree}

	cmd := m.ensureCOEClustersCmd(false)
	if cmd == nil {
		t.Fatal("in-flight pre-warm should start the spinner for the waiting view")
	}
	if !m.coeSpinnerRunning {
		t.Fatal("spinner should animate while the pre-warm is in flight")
	}
	if backend.coeCalls != 0 {
		t.Fatalf("drill-in during pre-warm must not refetch the list; coeCalls=%d", backend.coeCalls)
	}
}

func TestProjectSwitchRewarmsCOEClusters(t *testing.T) {
	backend := &fakeBackend{
		coeClusters: []osclient.COECluster{testCOECluster()},
		cap:         osclient.SwitchCapability{CanSwitch: true},
	}
	m := New(backend, Config{})
	// A completed startup pre-warm for the initial project.
	m.coeClusters = backend.coeClusters
	m.coeClustersLoaded = true

	next, cmd := m.onSwitched(switchedMsg{project: osclient.ProjectInfo{ID: "p2", Name: "beta"}})
	m = next.(Model)
	if m.coeClustersLoaded || m.coeClustersLoading || len(m.coeClusters) != 0 {
		t.Fatal("a project switch must invalidate the previous project's COE list")
	}
	// The switch must re-warm the COE list for the new scope.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("onSwitched should return a batch of commands")
	}
	rewarmed := false
	for _, c := range batch {
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func(cmd tea.Cmd) { done <- cmd() }(c)
		select {
		case got := <-done:
			if _, ok := got.(coePreloadMsg); ok {
				rewarmed = true
			}
		case <-time.After(500 * time.Millisecond):
		}
		if rewarmed {
			break
		}
	}
	if !rewarmed {
		t.Fatal("project switch did not re-warm the COE cluster list")
	}
}

func TestNavigatingRebuildsStaleCOENode(t *testing.T) {
	m := New(&fakeBackend{}, Config{})
	m.width, m.height = 100, 40

	// A tree built while the cluster list was still loading shows "obtaining…".
	tree := newTree()
	tree.Root.Name = "kube_service_" + testClusterUUID + "_tenant-layer_web"
	m.applyKubernetesRelations(tree)
	if stale := firstChildOfType(tree.Root, model.TypeCOECluster); stale == nil || stale.Name != "obtaining cluster data…" {
		t.Fatalf("precondition: expected an obtaining-data COE node, got %+v", stale)
	}

	// The list lands afterwards (e.g. the pre-warm completes on the LB list).
	m.coeClusters = []osclient.COECluster{testCOECluster()}
	m.coeClustersLoaded = true

	// Navigating to the (possibly cached) tree must rebuild the stale node.
	m.buildNodeLocation(tree.Root.Identity(), tree)
	if fresh := firstChildOfType(tree.Root, model.TypeCOECluster); fresh == nil || fresh.Name != "clusterapi" {
		t.Fatalf("navigation should rebuild the stale COE node from the loaded list, got %+v", fresh)
	}
}

func TestProjectSwitchCancelsInFlightCOELoad(t *testing.T) {
	backend := &fakeBackend{coeBlock: true, cap: osclient.SwitchCapability{CanSwitch: true}}
	m := New(backend, Config{})

	// Start the pre-warm; ListCOEClusters blocks until its context is cancelled.
	next, cmd := m.onCOEPreload()
	m = next.(Model)
	if cmd == nil || m.coeCancel == nil {
		t.Fatal("pre-warm should start a cancellable load and store its cancel handle")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	// Switching project must abort the in-flight (slow) load, not wait it out.
	next, _ = m.onSwitched(switchedMsg{project: osclient.ProjectInfo{ID: "p2", Name: "beta"}})
	m = next.(Model)
	if m.coeCancel != nil {
		t.Fatal("switch should clear the cancel handle after aborting the load")
	}
	select {
	case msg := <-done:
		cm, ok := msg.(coeClustersMsg)
		if !ok || cm.err == nil {
			t.Fatalf("cancelled load should return a context error, got %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project switch did not cancel the in-flight COE cluster load")
	}
}
