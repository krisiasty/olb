package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

func TestComputeAreaListsInstances(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))

	if m.activeWorkspace != kindInstance || areaOf(m.activeWorkspace) != areaCompute {
		t.Fatalf("C should open the compute instances workspace; active=%v area=%v", m.activeWorkspace, areaOf(m.activeWorkspace))
	}
	if !m.loc.id.Equal(model.InstanceListIdentity) {
		t.Fatalf("compute root = %+v, want instance list", m.loc.id)
	}
	if len(m.entries) != 1 || m.entries[0].instance.ID != "instance-1" {
		t.Fatalf("instance rows = %+v", m.entries)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"C-1", "instances", "NAME", "STATUS", "FLAVOR", "ADDRESSES", "CREATED", "api-1", "ACTIVE", "m1.small", "private=10.0.0.12"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compute instance list should contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(lineContaining(view, "STATUS"), "PROJECT") {
		t.Fatalf("project-scoped instance list should omit the redundant project column:\n%s", view)
	}
}

func TestSystemScopedInstanceListShowsOwningProject(t *testing.T) {
	backend := &fakeBackend{
		all: true,
		instances: []osclient.Instance{
			{
				ID: "instance-2", Name: "worker-1", Status: "SHUTOFF",
				ProjectID: "p2", ProjectName: "beta",
				FlavorID: "flavor-2", FlavorName: "m1.medium",
				Addresses: []string{"private=10.0.1.20"}, PrimaryAddress: "10.0.1.20",
				Created: time.Date(2026, 7, 29, 6, 30, 0, 0, time.UTC),
			},
		},
	}
	m := New(backend, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.home = false
	m = updExec(t, m, press("C"))

	header := strings.Join(m.columnTitles(), " ")
	if !strings.Contains(header, "PROJECT") {
		t.Fatalf("system-scoped instance columns should include project: %q", header)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "beta") || !strings.Contains(view, "worker-1") {
		t.Fatalf("system-scoped instance row should show its project name:\n%s", view)
	}
	m = upd(t, m, press("d"))
	if header := strings.Join(m.columnTitles(), " "); !strings.Contains(header, "PROJECT ID") {
		t.Fatalf("ID mode should relabel the owning project column: %q", header)
	}
	if row := strings.Join(m.rowCells(m.entries[0]), " "); !strings.Contains(row, "p2") {
		t.Fatalf("ID mode should display the owning project ID: %q", row)
	}
}

func TestInstanceIDModeControlsColumnsAndFiltering(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = upd(t, m, press("d"))
	if got := m.columnTitles()[0]; got != "INSTANCE ID" {
		t.Fatalf("ID-mode first column = %q", got)
	}
	m.filter.SetValue("instance-1")
	m.applyFilters()
	if len(m.entries) != 1 {
		t.Fatalf("ID-mode filter should match the displayed instance ID; rows=%d", len(m.entries))
	}
	m.filter.SetValue("api-1")
	m.applyFilters()
	if len(m.entries) != 0 {
		t.Fatalf("ID-mode filter should not match a hidden instance name; rows=%d", len(m.entries))
	}
}

func TestInstanceDetailUsesStandaloneComputeData(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.isInstanceOverview() || m.loc.node == nil || m.loc.node.ID != "instance-1" {
		t.Fatalf("enter should open standalone instance detail: loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"INSTANCE DETAILS", "api-1", "alpha", "m1.small", "Ubuntu 24.04",
		"private=10.0.0.12", "Libvirt name", "instance-0000012a",
		"ACCESS", "Key pair", "operator", "2026-07-30 08:15:00 UTC",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("instance detail should contain %q:\n%s", want, view)
		}
	}
}

func TestInstanceRelatedObjectsIncludeHypervisorAndAllocatedAccelerators(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).accelerators = map[string][]osclient.Accelerator{
		"hypervisor-1": {
			{
				ProviderID: "gpu-for-instance", ProviderName: "compute-01_0000:81:00.0",
				PCIAddress: "0000:81:00.0", ResourceClass: "CUSTOM_GPU_NVIDIA_L40S",
				DisplayName: "NVIDIA L40S", Total: 1, Used: 1,
				Allocations: []osclient.AcceleratorAllocation{{ConsumerID: "instance-1", Amount: 1}},
			},
			{
				ProviderID: "gpu-for-other", ProviderName: "compute-01_0000:82:00.0",
				PCIAddress: "0000:82:00.0", ResourceClass: "CUSTOM_GPU_NVIDIA_L40S",
				DisplayName: "NVIDIA L40S", Total: 1, Used: 1,
				Allocations: []osclient.AcceleratorAllocation{{ConsumerID: "instance-other", Amount: 1}},
			},
		},
	}
	m = updExec(t, m, press("C"))

	next, hypervisorCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if hypervisorCmd == nil || !m.hypervisorsLoading {
		t.Fatal("opening an instance should lazily request its hypervisor")
	}
	next, acceleratorCmd := m.Update(hypervisorCmd())
	m = next.(Model)
	if acceleratorCmd == nil || !m.acceleratorsLoading["hypervisor-1"] {
		t.Fatal("resolving the hypervisor should lazily request its accelerator inventory")
	}
	m = upd(t, m, acceleratorCmd())

	plain := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"RELATED OBJECTS 2", "HYPERVISOR 1", "compute-01",
		"ACCELERATORS 1", "NVIDIA L40S", "0000:81:00.0",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("instance related objects should contain %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "0000:82:00.0") {
		t.Fatalf("instance detail should not show an accelerator allocated to another consumer:\n%s", plain)
	}
	if len(m.entries) != 4 || m.entries[1].kind != entHypervisor || m.entries[3].kind != entAccelerator {
		t.Fatalf("instance related entries = %+v", m.entries)
	}

	m.cursor = 1
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isHypervisorOverview() || m.loc.node.ID != "hypervisor-1" {
		t.Fatalf("instance hypervisor relation did not open its detail: loc=%+v", m.loc)
	}
}

func TestRoleTreeKeyIsInactiveInInstanceDetail(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isInstanceOverview() {
		t.Fatal("setup should open instance detail")
	}

	m = upd(t, m, press("t"))
	if m.overlay != overlayNone || m.flash != "" {
		t.Fatalf("t should be inactive in instance detail: overlay=%v flash=%q", m.overlay, m.flash)
	}
	if !m.isInstanceOverview() {
		t.Fatal("t should leave the instance detail unchanged")
	}
}

func TestHypervisorRelatedObjectsListResidentInstancesByName(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).instances = []osclient.Instance{
		{ID: "instance-z", Name: "zulu", Status: "ACTIVE", Host: "compute-01"},
		{ID: "instance-other", Name: "other-host", Status: "ACTIVE", Host: "compute-02"},
		{ID: "instance-a", Name: "Alpha", Status: "SHUTOFF", HypervisorHostname: "compute-01"},
	}
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))
	m = updExec(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	plain := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"INSTANCES 2", "Alpha", "zulu"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("hypervisor related objects should contain %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "other-host") {
		t.Fatalf("hypervisor detail included an instance resident on another host:\n%s", plain)
	}
	if strings.Index(plain, "Alpha") > strings.Index(plain, "zulu") {
		t.Fatalf("hypervisor instances are not sorted by name:\n%s", plain)
	}
}

func TestInstanceCanBeInspectedFromTopLevelList(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))

	m = upd(t, m, press("y"))
	if m.overlay != overlayRaw || m.rawFormat != "yaml" {
		t.Fatalf("y on an instance row should open raw YAML: overlay=%v format=%q", m.overlay, m.rawFormat)
	}
	if !m.loc.isTopLevelList() || m.loc.listKind() != kindInstance {
		t.Fatalf("raw inspection should not navigate away from the instances list: loc=%+v", m.loc)
	}
	if !strings.Contains(m.rawTitle, "instance:api-1") ||
		!strings.Contains(m.rawContent, "instance_name: instance-0000012a") {
		t.Fatalf("instance YAML should use the highlighted detail object:\ntitle=%q\n%s", m.rawTitle, m.rawContent)
	}

	m = upd(t, m, press("esc"))
	m = upd(t, m, press("j"))
	if m.overlay != overlayRaw || m.rawFormat != "json" ||
		!strings.Contains(m.rawContent, `"instance_name": "instance-0000012a"`) {
		t.Fatalf("j on an instance row should open raw JSON:\n%s", m.rawContent)
	}
}

func TestInstanceListRBACDenialStaysInViewWithFriendlyMessage(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).instancesErr = osclient.ErrAdminRequired
	m = updExec(t, m, press("C"))

	if !m.loc.isTopLevelList() || m.loc.listKind() != kindInstance {
		t.Fatalf("RBAC denial should leave the instances view active: loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "Nova does not authorize instance listing for this token scope") {
		t.Fatalf("instances view should explain the RBAC denial:\n%s", view)
	}
	if strings.Contains(view, "Expected HTTP response code") || strings.Contains(view, `"forbidden"`) {
		t.Fatalf("instances view must not expose the raw HTTP error:\n%s", view)
	}
	if m.flashErr {
		t.Fatalf("RBAC denial should be an explanatory empty view, not an error flash")
	}
}

func TestComputeAreaListsHypervisors(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))

	if m.activeWorkspace != kindHypervisor || areaOf(m.activeWorkspace) != areaCompute {
		t.Fatalf("C-2 should open hypervisors; active=%v area=%v", m.activeWorkspace, areaOf(m.activeWorkspace))
	}
	if !m.loc.id.Equal(model.HypervisorListIdentity) {
		t.Fatalf("hypervisor root = %+v", m.loc.id)
	}
	if len(m.entries) != 1 || m.entries[0].hypervisor.ID != "hypervisor-1" {
		t.Fatalf("hypervisor rows = %+v", m.entries)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"C-2", "hypervisors", "HOSTNAME", "STATE", "STATUS", "TYPE", "VCPUS",
		"MEMORY", "DISK", "VMS", "HOST IP", "compute-01", "UP", "ENABLED",
		"QEMU", "28/64", "96 GiB/256 GiB", "740 GiB/1.76 TiB", "12", "192.0.2.21",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("hypervisor list should contain %q:\n%s", want, view)
		}
	}
}

func TestHypervisorDetailAndTopLevelInspection(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))

	m = upd(t, m, press("y"))
	if m.overlay != overlayRaw || !strings.Contains(m.rawTitle, "hypervisor:compute-01") ||
		!strings.Contains(m.rawContent, "hostname: compute-01") {
		t.Fatalf("top-level hypervisor YAML is incorrect:\ntitle=%q\n%s", m.rawTitle, m.rawContent)
	}
	m = upd(t, m, press("esc"))
	m = upd(t, m, tea.WindowSizeMsg{Width: 180, Height: 34})
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isHypervisorOverview() {
		t.Fatalf("enter should open hypervisor details: loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"HYPERVISOR DETAILS", "compute-01", "QEMU", "8002000", "UP", "ENABLED",
		"SERVICE", "service-1", "CAPACITY", "28/64", "96 GiB/256 GiB",
		"740 GiB/1.76 TiB", "920 GiB",
		"GPU / ACCELERATORS", "192.0.2.21", "CPU", "Intel", "Skylake-Server", "x86_64",
		"2 NUMA nodes", "2 sockets/node", "16 cores/socket", "2 threads/core",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("hypervisor detail should contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "vmx, aes") {
		t.Fatalf("CPU features should not consume space in the fixed detail panel:\n%s", view)
	}
	capacity := map[string]string{}
	for _, group := range m.hypervisorDetailGroups() {
		if group.title != "CAPACITY" {
			continue
		}
		for _, field := range group.fields {
			capacity[field.label] = field.value
		}
	}
	for label, want := range map[string]string{
		"VCPUs used / total":      "28/64 (43.8 %)",
		"Memory used / total":     "96 GiB/256 GiB (37.5 %)",
		"Local disk used / total": "740 GiB/1.76 TiB (41.1 %)",
		"Disk available least":    "920 GiB",
	} {
		if got := capacity[label]; got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
}

func TestHypervisorUsagePercentageFormatting(t *testing.T) {
	if got := withUsagePercentage("87/384", 87, 384); got != "87/384 (22.7 %)" {
		t.Fatalf("VCPU usage = %q, want one-decimal percentage", got)
	}
	if got := withUsagePercentage("—", 0, 0); got != "—" {
		t.Fatalf("unavailable usage = %q, want no percentage", got)
	}
}

func TestHypervisorTopologyExplainsNUMAHierarchy(t *testing.T) {
	n := model.NewNode(model.TypeHypervisor, "hypervisor-genoa", "compute-genoa")
	n.SetAttr("cpu_cells", "2")
	n.SetAttr("cpu_sockets", "1")
	n.SetAttr("cpu_cores", "96")
	n.SetAttr("cpu_threads", "2")

	const want = "2 NUMA nodes · 1 socket/node · 96 cores/socket · 2 threads/core"
	if got := hypervisorTopology(n); got != want {
		t.Fatalf("Genoa topology = %q, want %q", got, want)
	}
}

func TestHypervisorFeaturesUseDetailOnlyScrollableModal(t *testing.T) {
	features := make([]string, 40)
	for i := range features {
		features[i] = fmt.Sprintf("feature-%02d", len(features)-1-i)
	}

	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).hypervisors = []osclient.Hypervisor{{
		ID: "hypervisor-features", Hostname: "compute-features",
		State: "up", Status: "enabled", CPUFeatures: features,
	}}
	m = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 14})
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))

	if hint := ansiRE.ReplaceAllString(m.hintLine(), ""); strings.Contains(hint, "f CPU features") {
		t.Fatalf("hypervisor list should not advertise the detail-only feature popup: %q", hint)
	}
	m = upd(t, m, press("f"))
	if m.overlay != overlayNone {
		t.Fatalf("f should be inactive in the hypervisor list: overlay=%v", m.overlay)
	}

	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isHypervisorOverview() {
		t.Fatalf("enter should open hypervisor details: loc=%+v", m.loc)
	}
	for _, group := range m.hypervisorDetailGroups() {
		for _, field := range group.fields {
			if field.label == "Features" {
				t.Fatal("CPU features remain in the fixed hypervisor detail fields")
			}
		}
	}
	if hint := ansiRE.ReplaceAllString(m.hintLine(), ""); !strings.Contains(hint, "f CPU features") {
		t.Fatalf("hypervisor details should advertise the feature popup: %q", hint)
	}

	m = upd(t, m, press("f"))
	if m.overlay != overlayHypervisorFeatures {
		t.Fatalf("f did not open CPU features: overlay=%v", m.overlay)
	}
	content := ansiRE.ReplaceAllString(m.hypervisorFeaturesContent(), "")
	if !strings.Contains(content, "feature-00") || !strings.Contains(content, "feature-39") {
		t.Fatalf("feature popup content is incomplete:\n%s", content)
	}
	if strings.Contains(content, ",") || strings.Contains(content, "\n") {
		t.Fatalf("features should be a single space-separated sequence before wrapping:\n%s", content)
	}
	sorted := make([]string, len(features))
	for i := range sorted {
		sorted[i] = fmt.Sprintf("feature-%02d", i)
	}
	if got, want := strings.TrimSpace(content), strings.Join(sorted, " "); got != want {
		t.Fatalf("features are not sorted:\ngot:  %s\nwant: %s", got, want)
	}
	if m.vp.TotalLineCount() <= 1 {
		t.Fatalf("long space-separated feature sequence was not wrapped in the viewport: %q", m.vp.View())
	}

	box := ansiRE.ReplaceAllString(m.hypervisorFeaturesModalBox(), "")
	if !strings.Contains(box, "CPU FEATURES") || !strings.Contains(box, "╭") || !strings.Contains(box, "╯") {
		t.Fatalf("CPU features should use an uppercase framed modal:\n%s", box)
	}
	areaInnerWidth := lipgloss.Width(m.switcherModalBox()) - m.st.modalFrame.GetHorizontalFrameSize()
	if m.vp.Width < areaInnerWidth {
		t.Fatalf("feature modal inner width = %d, want at least area switcher width %d", m.vp.Width, areaInnerWidth)
	}
	lines := strings.Split(box, "\n")
	for i, line := range lines {
		if strings.Contains(line, "CPU FEATURES") {
			if i+1 >= len(lines) || strings.Trim(lines[i+1], " │") != "" {
				t.Fatalf("feature modal header has no fixed empty separator line:\n%s", box)
			}
		}
		if strings.Contains(line, "↑/↓") {
			if i == 0 || strings.Trim(lines[i-1], " │") != "" {
				t.Fatalf("feature modal footer has no fixed empty separator line:\n%s", box)
			}
		}
	}
	if plain := ansiRE.ReplaceAllString(m.View(), ""); !strings.Contains(plain, "▼ more") {
		t.Fatalf("scrollable feature modal has no bottom indicator:\n%s", plain)
	}
	m.vp.GotoBottom()
	plain := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(plain, "▲ more") || !strings.Contains(plain, "100%") ||
		!strings.Contains(plain, "esc/f/q close") {
		t.Fatalf("scrolled feature modal is missing status or hotkeys:\n%s", plain)
	}
	m = upd(t, m, press("f"))
	if m.overlay != overlayNone {
		t.Fatal("f did not close the CPU features modal")
	}
}

func TestHypervisorAcceleratorsLoadAsRelatedObjectsAndOpenDetails(t *testing.T) {
	items := make([]osclient.Accelerator, 16)
	for index := range items {
		items[index] = osclient.Accelerator{
			ProviderID:    fmt.Sprintf("provider-%02d", index),
			ProviderName:  fmt.Sprintf("compute-01_0000:%02X:00.0", 0x80+index),
			PCIAddress:    fmt.Sprintf("0000:%02X:00.0", 0x80+index),
			ResourceClass: "CUSTOM_GPU_NVIDIA_RTX_PRO_6000WM",
			DisplayName:   "NVIDIA RTX PRO 6000WM",
			Total:         1,
		}
		if index < 8 {
			items[index].Used = 1
			items[index].Allocations = []osclient.AcceleratorAllocation{{
				ConsumerID: "instance-1",
				Amount:     1,
			}}
		}
	}

	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).accelerators = map[string][]osclient.Accelerator{
		"hypervisor-1": items,
	}
	m = updExec(t, m, press("C")) // also remembers instance-1 for allocation labels
	m = updExec(t, m, press("2"))

	m = updExec(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isHypervisorOverview() || !m.acceleratorsLoaded["hypervisor-1"] {
		t.Fatalf("hypervisor details did not load accelerator inventory: loc=%+v", m.loc)
	}
	if got := m.hypervisorAcceleratorSummary("hypervisor-1"); got != "16 providers · 8/16 units allocated" {
		t.Fatalf("accelerator summary = %q", got)
	}
	plain := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"GPU / ACCELERATORS", "16 providers · 8/16 units allocated",
		"RELATED OBJECTS 17", "INSTANCES 1", "api-1", "ACCELERATORS 16",
		"NVIDIA RTX PRO 6000WM", "0000:80:00.0", "1/1", "api-1",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("hypervisor detail should contain %q:\n%s", want, plain)
		}
	}
	if styled := m.View(); !strings.Contains(styled, m.st.panelTitle.Render("RELATED OBJECTS 17")) {
		t.Fatalf("hypervisor related objects should use the shared panel-title style:\n%s", styled)
	}
	if len(m.entries) != 19 || m.entries[0].kind != entGroup {
		t.Fatalf("accelerator related entries = %+v", m.entries)
	}

	for i := range m.entries {
		if m.entries[i].kind == entAccelerator {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isAcceleratorOverview() {
		t.Fatalf("accelerator row did not open details: loc=%+v", m.loc)
	}
	plain = ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"ACCELERATOR DETAILS", "NVIDIA RTX PRO 6000WM", "provider-00",
		"compute-01_0000:80:00.0", "0000:80:00.0",
		"CUSTOM_GPU_NVIDIA_RTX_PRO_6000W", "1/1 (100.0 %)", "api-1",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("accelerator detail should contain %q:\n%s", want, plain)
		}
	}
	if got := m.loc.node.Attrs["resource_class"]; got != "CUSTOM_GPU_NVIDIA_RTX_PRO_6000WM" {
		t.Fatalf("accelerator resource class = %q", got)
	}
	id, name := m.currentIDName()
	if id != "provider-00" || name != "NVIDIA RTX PRO 6000WM" {
		t.Fatalf("accelerator copy identity = %q, %q", id, name)
	}
}

func TestHypervisorAcceleratorPoolAndUnknownConsumerFormatting(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.acceleratorsLoaded["hypervisor-1"] = true
	m.accelerators["hypervisor-1"] = []osclient.Accelerator{{
		ProviderID: "provider-pool", PCIAddress: "0000:C7:00.0",
		DisplayName: "NVIDIA A16 VFS", Total: 16, Used: 11,
		Allocations: []osclient.AcceleratorAllocation{
			{ConsumerID: "instance-1", Amount: 5},
			{ConsumerID: "1194004f-b245-4055-9a99-cd3d06e139c2", Amount: 6},
		},
	}}
	m.rememberInstances([]osclient.Instance{{ID: "instance-1", Name: "gpu-worker-01"}})

	if got := m.hypervisorAcceleratorSummary("hypervisor-1"); got != "1 provider · 11/16 units allocated" {
		t.Fatalf("VF-pool summary = %q", got)
	}
	got := m.acceleratorAllocations(m.accelerators["hypervisor-1"][0])
	if !strings.Contains(got, "gpu-worker-01 ×5") || !strings.Contains(got, "consumer:1194004f ×6") {
		t.Fatalf("VF-pool allocations = %q", got)
	}
}

func TestHypervisorAcceleratorRBACFailureIsFriendly(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).acceleratorErr = osclient.ErrAdminRequired
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))
	m = updExec(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	const want = "Placement does not authorize PCI inventory for this token scope"
	if got := m.hypervisorAcceleratorSummary("hypervisor-1"); got != want {
		t.Fatalf("RBAC summary = %q", got)
	}
	plain := ansiRE.ReplaceAllString(m.View(), "")
	for _, text := range []string{"GPU / ACCELERATORS", "Placement does not authorize PCI", "inventory for this token scope", "RELATED OBJECTS 1", "INSTANCES 1", "ACCELERATORS 0"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("RBAC hypervisor detail should contain %q:\n%s", text, plain)
		}
	}
	if strings.Contains(plain, "Expected HTTP response code") {
		t.Fatalf("RBAC hypervisor detail exposed raw HTTP error:\n%s", plain)
	}
}

func TestHypervisorDiskFormattingScalesIECUnits(t *testing.T) {
	if got := diskPair(512, 2048); got != "512 GiB/2 TiB" {
		t.Fatalf("disk pair = %q, want dynamically scaled IEC units", got)
	}
	if got := diskValue("1536"); got != "1.5 TiB" {
		t.Fatalf("disk value = %q, want dynamically scaled IEC units", got)
	}
	if got := diskPair(0, 0); got != "—" {
		t.Fatalf("unavailable disk capacity = %q, want em dash", got)
	}
}

func TestHypervisorDetailColorsStateAndStatusIndependently(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).hypervisors = []osclient.Hypervisor{{
		ID: "hypervisor-down", Hostname: "compute-down",
		State: "down", Status: "enabled",
	}}
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	groups := m.hypervisorDetailGroups()
	if len(groups) < 2 {
		t.Fatalf("hypervisor detail groups = %+v", groups)
	}
	fields := groups[1].fields
	if len(fields) < 2 || fields[0].label != "State" || fields[0].value != "DOWN" || !fields[0].status {
		t.Fatalf("State should use status coloring: %+v", fields)
	}
	if fields[1].label != "Status" || fields[1].value != "ENABLED" || !fields[1].status {
		t.Fatalf("Status should use status coloring: %+v", fields)
	}
	if got := string(statusColor("DOWN")); got != "196" {
		t.Fatalf("DOWN color = %q, want red (196)", got)
	}
	if got := string(statusColor("ENABLED")); got != "42" {
		t.Fatalf("ENABLED color = %q, want green (42)", got)
	}
}

func TestHypervisorIDModeAndRBACDenial(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = updExec(t, m, press("2"))
	m = upd(t, m, press("d"))
	if m.columnTitles()[0] != "HYPERVISOR ID" || m.rowCells(m.entries[0])[0] != "hypervisor-1" {
		t.Fatalf("hypervisor ID mode = %q / %q", m.columnTitles()[0], m.rowCells(m.entries[0])[0])
	}
	m.filter.SetValue("hypervisor-1")
	m.applyFilters()
	if len(m.entries) != 1 {
		t.Fatalf("ID-mode filter should match hypervisor ID; rows=%d", len(m.entries))
	}

	denied := start(t, switchCapability{CanSwitch: true})
	denied.backend.(*fakeBackend).hypervisorsErr = osclient.ErrAdminRequired
	denied = updExec(t, denied, press("C"))
	denied = updExec(t, denied, press("2"))
	view := ansiRE.ReplaceAllString(denied.View(), "")
	if !strings.Contains(view, "Nova does not authorize hypervisor listing for this token scope") {
		t.Fatalf("hypervisor RBAC denial should stay in view:\n%s", view)
	}
}
