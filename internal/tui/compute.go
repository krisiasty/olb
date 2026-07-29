package tui

import (
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
	return m.identityOverviewLines(h, m.instanceOverviewSummary, "— no related objects —")
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
