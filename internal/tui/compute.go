package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

func formatTableTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.UTC().Format("2006-01-02 15:04")
}

func formatInstanceTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (m *Model) rememberInstances(instances []osclient.Instance) {
	for _, instance := range instances {
		m.knownInstances[instance.ID] = instance
	}
}

func (m Model) instanceNode(id string) *model.Node {
	instance, ok := m.knownInstances[id]
	if !ok {
		return nil
	}
	n := model.NewNode(model.TypeInstance, instance.ID, instance.Name)
	n.OperatingStatus = instance.Status
	n.SetAttr("status", instance.Status)
	n.SetAttr("project_id", instance.ProjectID)
	n.SetAttr("project_name", instance.ProjectName)
	n.SetAttr("user_id", instance.UserID)
	n.SetAttr("flavor_id", instance.FlavorID)
	n.SetAttr("flavor_name", instance.FlavorName)
	n.SetAttr("image_id", instance.ImageID)
	n.SetAttr("image_name", instance.ImageName)
	n.SetAttr("addresses", strings.Join(instance.Addresses, ", "))
	n.SetAttr("availability_zone", instance.AvailabilityZone)
	n.SetAttr("host", instance.Host)
	n.SetAttr("hypervisor_hostname", instance.HypervisorHostname)
	n.SetAttr("instance_name", instance.InstanceName)
	n.SetAttr("key_name", instance.KeyName)
	n.SetAttr("created_at", formatInstanceTime(instance.Created))
	n.SetAttr("updated_at", formatInstanceTime(instance.Updated))
	n.DetailLoaded = true
	n.Raw = instance
	return n
}

func (m Model) isInstanceOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeInstance && m.loc.id.OwningLBID == ""
}

func (m Model) instanceOverviewLines(h int) []string {
	empty := "— no related objects —"
	switch {
	case m.hypervisorsLoading:
		empty = "loading hypervisor…"
	case m.hypervisorsErr != "":
		empty = m.hypervisorsErr
	case m.loc.node != nil:
		if hypervisor, ok := m.instanceHypervisorByID(m.loc.node.ID); ok && m.acceleratorsLoading[hypervisor.ID] {
			empty = "loading accelerator inventory…"
		}
	}
	return m.identityOverviewLines(h, m.instanceOverviewSummary, empty)
}

func (m Model) instanceOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "INSTANCE DETAILS", m.instanceDetailGroups())
}

func (m Model) instanceDetailGroups() []overviewGroup {
	n := m.loc.node
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Name", value: displayValue(n.Name)},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Status", value: displayValue(n.Attrs["status"])},
		}},
		{title: "OWNERSHIP", fields: []overviewField{
			{label: "Project", value: displayValue(n.Attrs["project_name"])},
			{label: "Project ID", value: displayValue(n.Attrs["project_id"])},
			{label: "User ID", value: displayValue(n.Attrs["user_id"])},
		}},
		{title: "IMAGE & FLAVOR", fields: []overviewField{
			{label: "Flavor", value: displayValue(n.Attrs["flavor_name"])},
			{label: "Flavor ID", value: displayValue(n.Attrs["flavor_id"])},
			{label: "Image", value: displayValue(n.Attrs["image_name"])},
			{label: "Image ID", value: displayValue(n.Attrs["image_id"])},
		}},
		{title: "PLACEMENT", fields: []overviewField{
			{label: "Availability zone", value: displayValue(n.Attrs["availability_zone"])},
			{label: "Host", value: displayValue(n.Attrs["host"])},
			{label: "Hypervisor", value: displayValue(n.Attrs["hypervisor_hostname"])},
			{label: "Libvirt name", value: displayValue(n.Attrs["instance_name"])},
		}},
		{title: "NETWORK", fields: []overviewField{
			{label: "Addresses", value: displayValue(n.Attrs["addresses"])},
		}},
		{title: "ACCESS", fields: []overviewField{
			{label: "Key pair", value: displayValue(n.Attrs["key_name"])},
		}},
		{title: "LIFECYCLE", fields: []overviewField{
			{label: "Created", value: displayTimestamp(n.Attrs["created_at"])},
			{label: "Updated", value: displayTimestamp(n.Attrs["updated_at"])},
		}},
	}
}

func hypervisorLabel(h osclient.Hypervisor) string {
	if h.Hostname != "" {
		return h.Hostname
	}
	return shortID(h.ID)
}

func countPair(used, total int) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d/%d", used, total)
}

func memoryPair(used, total int) string {
	if total <= 0 {
		return "—"
	}
	return formatIEC(float64(used)*1024*1024) + "/" + formatIEC(float64(total)*1024*1024)
}

func diskPair(used, total int) string {
	if total <= 0 {
		return "—"
	}
	return formatDiskGiB(used) + "/" + formatDiskGiB(total)
}

func withUsagePercentage(value string, used, total int) string {
	if total <= 0 {
		return value
	}
	return fmt.Sprintf("%s (%.1f %%)", value, float64(used)*100/float64(total))
}

func formatDiskGiB(value int) string {
	return formatIEC(float64(value) * 1024 * 1024 * 1024)
}

func setPositiveIntAttr(n *model.Node, key string, value int) {
	if value > 0 {
		n.SetAttr(key, strconv.Itoa(value))
	}
}

func (m *Model) rememberHypervisors(hypervisors []osclient.Hypervisor) {
	for _, hypervisor := range hypervisors {
		m.knownHypervisors[hypervisor.ID] = hypervisor
	}
}

func (m Model) hypervisorNode(id string) *model.Node {
	hypervisor, ok := m.knownHypervisors[id]
	if !ok {
		return nil
	}
	n := model.NewNode(model.TypeHypervisor, hypervisor.ID, hypervisorLabel(hypervisor))
	n.OperatingStatus = strings.ToUpper(hypervisor.State)
	n.ProvisioningStatus = strings.ToUpper(hypervisor.Status)
	n.SetAttr("type", hypervisor.Type)
	setPositiveIntAttr(n, "version", hypervisor.Version)
	n.SetAttr("state", strings.ToUpper(hypervisor.State))
	n.SetAttr("status", strings.ToUpper(hypervisor.Status))
	n.SetAttr("host_ip", hypervisor.HostIP)
	n.SetAttr("service_host", hypervisor.ServiceHost)
	n.SetAttr("service_id", hypervisor.ServiceID)
	n.SetAttr("disabled_reason", hypervisor.DisabledReason)
	n.SetAttr("current_workload", strconv.Itoa(hypervisor.CurrentWorkload))
	n.SetAttr("running_vms", strconv.Itoa(hypervisor.RunningVMs))
	setPositiveIntAttr(n, "vcpus", hypervisor.VCPUs)
	n.SetAttr("vcpus_used", strconv.Itoa(hypervisor.VCPUsUsed))
	setPositiveIntAttr(n, "memory_mb", hypervisor.MemoryMB)
	n.SetAttr("memory_mb_used", strconv.Itoa(hypervisor.MemoryMBUsed))
	setPositiveIntAttr(n, "local_gb", hypervisor.LocalGB)
	n.SetAttr("local_gb_used", strconv.Itoa(hypervisor.LocalGBUsed))
	setPositiveIntAttr(n, "disk_available_least", hypervisor.DiskAvailableLeast)
	n.SetAttr("cpu_arch", hypervisor.CPUArch)
	n.SetAttr("cpu_model", hypervisor.CPUModel)
	n.SetAttr("cpu_vendor", hypervisor.CPUVendor)
	n.SetAttr("cpu_features", strings.Join(hypervisor.CPUFeatures, ", "))
	setPositiveIntAttr(n, "cpu_cells", hypervisor.CPUCells)
	setPositiveIntAttr(n, "cpu_sockets", hypervisor.CPUSockets)
	setPositiveIntAttr(n, "cpu_cores", hypervisor.CPUCores)
	setPositiveIntAttr(n, "cpu_threads", hypervisor.CPUThreads)
	n.DetailLoaded = true
	n.Raw = hypervisor
	return n
}

func (m Model) isHypervisorOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeHypervisor
}

func (m Model) hypervisorOverviewLines(h int) []string {
	empty := "— no related objects —"
	if m.loc.node != nil && (m.instancesLoading || m.acceleratorsLoading[m.loc.node.ID]) {
		empty = "loading related objects…"
	}
	return m.identityOverviewLines(h, m.hypervisorOverviewSummary, empty)
}

func (m Model) hypervisorOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "HYPERVISOR DETAILS", m.hypervisorDetailGroups())
}

func (m Model) hypervisorDetailGroups() []overviewGroup {
	n := m.loc.node
	version := n.Attrs["version"]
	vcpusUsed, vcpusTotal := parseInt(n.Attrs["vcpus_used"]), parseInt(n.Attrs["vcpus"])
	memoryUsed, memoryTotal := parseInt(n.Attrs["memory_mb_used"]), parseInt(n.Attrs["memory_mb"])
	diskUsed, diskTotal := parseInt(n.Attrs["local_gb_used"]), parseInt(n.Attrs["local_gb"])
	vcpus := withUsagePercentage(countPair(vcpusUsed, vcpusTotal), vcpusUsed, vcpusTotal)
	memory := withUsagePercentage(memoryPair(memoryUsed, memoryTotal), memoryUsed, memoryTotal)
	disk := withUsagePercentage(diskPair(diskUsed, diskTotal), diskUsed, diskTotal)
	topology := hypervisorTopology(n)
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Hostname", value: displayValue(n.Name)},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Type", value: displayValue(n.Attrs["type"])},
			{label: "Version", value: displayValue(version)},
		}},
		{title: "STATE", fields: []overviewField{
			{label: "State", value: displayValue(n.Attrs["state"]), status: true},
			{label: "Status", value: displayValue(n.Attrs["status"]), status: true},
			{label: "Disabled reason", value: displayValue(n.Attrs["disabled_reason"])},
			{label: "Current workload", value: displayValue(n.Attrs["current_workload"])},
		}},
		{title: "SERVICE", fields: []overviewField{
			{label: "Host", value: displayValue(n.Attrs["service_host"])},
			{label: "Service ID", value: displayValue(n.Attrs["service_id"])},
			{label: "Host IP", value: displayValue(n.Attrs["host_ip"])},
		}},
		{title: "CAPACITY", fields: []overviewField{
			{label: "VCPUs used / total", value: vcpus},
			{label: "Memory used / total", value: memory},
			{label: "Local disk used / total", value: disk},
			{label: "Disk available least", value: diskValue(n.Attrs["disk_available_least"])},
			{label: "Running VMs", value: displayValue(n.Attrs["running_vms"])},
		}},
		{title: "GPU / ACCELERATORS", fields: []overviewField{
			{label: "Inventory", value: m.hypervisorAcceleratorSummary(n.ID)},
		}},
		{title: "CPU", fields: []overviewField{
			{label: "Vendor", value: displayValue(n.Attrs["cpu_vendor"])},
			{label: "Model", value: displayValue(n.Attrs["cpu_model"])},
			{label: "Architecture", value: displayValue(n.Attrs["cpu_arch"])},
			{label: "Topology", value: displayValue(topology)},
		}},
	}
}

func (m Model) hypervisorAcceleratorSummary(hypervisorID string) string {
	if m.acceleratorsLoading[hypervisorID] {
		return "loading Placement inventory…"
	}
	if err := m.acceleratorsErr[hypervisorID]; err != "" {
		return err
	}
	if !m.acceleratorsLoaded[hypervisorID] {
		return "not loaded"
	}
	items := m.accelerators[hypervisorID]
	if len(items) == 0 {
		return "none reported"
	}

	providers := make(map[string]struct{}, len(items))
	total, used := 0, 0
	for _, item := range items {
		providers[item.ProviderID] = struct{}{}
		total += item.Total
		used += item.Used
	}
	providerLabel := "providers"
	if len(providers) == 1 {
		providerLabel = "provider"
	}
	unitLabel := "units"
	if total == 1 {
		unitLabel = "unit"
	}
	return fmt.Sprintf("%d %s · %d/%d %s allocated", len(providers), providerLabel, used, total, unitLabel)
}

func acceleratorIdentityID(item osclient.Accelerator) string {
	return item.ProviderID + "|" + item.ResourceClass
}

func (m *Model) rememberAccelerators(items []osclient.Accelerator) {
	for _, item := range items {
		m.knownAccelerators[acceleratorIdentityID(item)] = item
	}
}

func (m Model) acceleratorNode(id string) *model.Node {
	item, ok := m.knownAccelerators[id]
	if !ok {
		return nil
	}
	name := item.DisplayName
	if name == "" {
		name = item.ResourceClass
	}
	n := model.NewNode(model.TypeAccelerator, id, name)
	n.SetAttr("provider_id", item.ProviderID)
	n.SetAttr("provider_name", item.ProviderName)
	n.SetAttr("pci_address", item.PCIAddress)
	n.SetAttr("resource_class", item.ResourceClass)
	n.SetAttr("total", strconv.Itoa(item.Total))
	n.SetAttr("reserved", strconv.Itoa(item.Reserved))
	n.SetAttr("used", strconv.Itoa(item.Used))
	available := item.Total - item.Reserved - item.Used
	if available < 0 {
		available = 0
	}
	n.SetAttr("available", strconv.Itoa(available))
	n.SetAttr("allocation_ratio", strconv.FormatFloat(float64(item.AllocationRatio), 'g', -1, 32))
	n.SetAttr("allocations", m.acceleratorAllocations(item))
	n.DetailLoaded = true
	n.Raw = item
	return n
}

func (m Model) isAcceleratorOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeAccelerator
}

func (m Model) acceleratorOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.acceleratorOverviewSummary, "— no related objects —")
}

func (m Model) acceleratorOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "ACCELERATOR DETAILS", m.acceleratorDetailGroups())
}

func (m Model) acceleratorDetailGroups() []overviewGroup {
	n := m.loc.node
	used, total := parseInt(n.Attrs["used"]), parseInt(n.Attrs["total"])
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Model", value: displayValue(n.Name)},
			{label: "Provider ID", value: displayValue(n.Attrs["provider_id"])},
			{label: "Provider name", value: displayValue(n.Attrs["provider_name"])},
		}},
		{title: "PCI", fields: []overviewField{
			{label: "Address", value: displayValue(n.Attrs["pci_address"])},
			{label: "Resource class", value: displayValue(n.Attrs["resource_class"])},
		}},
		{title: "CAPACITY", fields: []overviewField{
			{label: "Used / total", value: withUsagePercentage(countPair(used, total), used, total)},
			{label: "Reserved", value: displayValue(n.Attrs["reserved"])},
			{label: "Available", value: displayValue(n.Attrs["available"])},
			{label: "Allocation ratio", value: displayValue(n.Attrs["allocation_ratio"])},
		}},
		{title: "ALLOCATIONS", fields: []overviewField{
			{label: "Consumers", value: displayValue(n.Attrs["allocations"])},
		}},
	}
}

func (m Model) hypervisorRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	var out []entry
	if m.instancesLoaded {
		for _, instance := range m.instances {
			hypervisor, ok := m.hypervisorForInstance(instance)
			if !ok || hypervisor.ID != n.ID {
				continue
			}
			out = append(out, relatedInstanceEntry(instance))
		}
	}
	if m.acceleratorsLoaded[n.ID] {
		for _, item := range m.accelerators[n.ID] {
			out = append(out, m.relatedAcceleratorEntry(item))
		}
	}
	return out
}

func relatedInstanceEntry(instance osclient.Instance) entry {
	name := instance.Name
	if name == "" {
		name = shortID(instance.ID)
	}
	return entry{
		kind: entInstance, instance: instance,
		label: "instance:" + name, oper: instance.Status,
		extra: joinRelatedRowAttrs(
			instance.Status,
			orShort(instance.ProjectName, instance.ProjectID),
			orShort(instance.FlavorName, instance.FlavorID),
			strings.Join(instance.Addresses, ", "),
		),
	}
}

func relatedHypervisorEntry(hypervisor osclient.Hypervisor) entry {
	state, status := strings.ToUpper(hypervisor.State), strings.ToUpper(hypervisor.Status)
	oper := state
	if state == "DOWN" {
		oper = "ERROR"
	}
	return entry{
		kind: entHypervisor, hypervisor: hypervisor,
		label: "hypervisor:" + hypervisorLabel(hypervisor),
		oper:  oper, prov: status,
		extra: joinRelatedRowAttrs(state, status, hypervisor.HostIP),
	}
}

func (m Model) relatedAcceleratorEntry(item osclient.Accelerator) entry {
	name := item.DisplayName
	if name == "" {
		name = item.ResourceClass
	}
	return entry{
		kind: entAccelerator, accelerator: item,
		label: "accelerator:" + name,
		extra: joinRelatedRowAttrs(
			item.PCIAddress,
			acceleratorUsage(item),
			m.acceleratorAllocations(item),
		),
	}
}

func sameComputeHost(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	return left != "" && right != "" && strings.EqualFold(left, right)
}

func instanceRunsOnHypervisor(instance osclient.Instance, hypervisor osclient.Hypervisor) bool {
	if instance.HypervisorHostname != "" {
		return sameComputeHost(instance.HypervisorHostname, hypervisor.Hostname)
	}
	return sameComputeHost(instance.Host, hypervisor.ServiceHost) ||
		sameComputeHost(instance.Host, hypervisor.Hostname)
}

func (m Model) hypervisorForInstance(instance osclient.Instance) (osclient.Hypervisor, bool) {
	for _, hypervisor := range m.hypervisors {
		if instanceRunsOnHypervisor(instance, hypervisor) {
			return hypervisor, true
		}
	}
	return osclient.Hypervisor{}, false
}

func (m Model) instanceHypervisorByID(instanceID string) (osclient.Hypervisor, bool) {
	instance, ok := m.knownInstances[instanceID]
	if !ok {
		return osclient.Hypervisor{}, false
	}
	return m.hypervisorForInstance(instance)
}

func acceleratorAllocatedToInstance(item osclient.Accelerator, instanceID string) bool {
	for _, allocation := range item.Allocations {
		if allocation.ConsumerID == instanceID && allocation.Amount > 0 {
			return true
		}
	}
	return false
}

func (m Model) instanceRelatedEntries(n *model.Node) []entry {
	if n == nil || !m.hypervisorsLoaded {
		return nil
	}
	hypervisor, ok := m.instanceHypervisorByID(n.ID)
	if !ok {
		return nil
	}
	out := []entry{relatedHypervisorEntry(hypervisor)}
	if m.acceleratorsLoaded[hypervisor.ID] {
		for _, item := range m.accelerators[hypervisor.ID] {
			if acceleratorAllocatedToInstance(item, n.ID) {
				out = append(out, m.relatedAcceleratorEntry(item))
			}
		}
	}
	return out
}

func acceleratorUsage(item osclient.Accelerator) string {
	if item.Total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d/%d", item.Used, item.Total)
}

func (m Model) acceleratorAllocations(item osclient.Accelerator) string {
	if len(item.Allocations) == 0 {
		return ""
	}
	labels := make([]string, 0, len(item.Allocations))
	for _, allocation := range item.Allocations {
		label := ""
		if instance, ok := m.knownInstances[allocation.ConsumerID]; ok {
			label = instance.Name
			if label == "" {
				label = shortID(instance.ID)
			}
		}
		if label == "" {
			label = "consumer:" + shortID(allocation.ConsumerID)
		}
		if allocation.Amount != 1 {
			label += fmt.Sprintf(" ×%d", allocation.Amount)
		}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return strings.Join(labels, " · ")
}

func parseInt(value string) int {
	n, _ := strconv.Atoi(value)
	return n
}

func diskValue(value string) string {
	if value == "" {
		return "—"
	}
	return formatDiskGiB(parseInt(value))
}

func hypervisorTopology(n *model.Node) string {
	var parts []string
	for _, item := range []struct {
		key      string
		singular string
		plural   string
	}{
		{"cpu_cells", "NUMA node", "NUMA nodes"},
		{"cpu_sockets", "socket/node", "sockets/node"},
		{"cpu_cores", "core/socket", "cores/socket"},
		{"cpu_threads", "thread/core", "threads/core"},
	} {
		if value := n.Attrs[item.key]; value != "" {
			label := item.plural
			if value == "1" {
				label = item.singular
			}
			parts = append(parts, value+" "+label)
		}
	}
	return strings.Join(parts, " · ")
}
