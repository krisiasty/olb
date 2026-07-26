package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// This file isolates the Keystone service-catalog views (services, endpoints,
// regions), following the identity.go precedent. They cross-link: a service lists
// its endpoints, an endpoint opens its service and region, a region lists its
// endpoints and its parent. Endpoints are the join, so all three derive their
// related endpoint rows from the single shared endpoints list rather than
// re-fetching per object.

// --- label helpers (used by entry.identity) -------------------------------

func serviceLabelName(s osclient.Service) string {
	switch {
	case s.Type != "":
		return s.Type
	case s.Name != "":
		return s.Name
	default:
		return shortID(s.ID)
	}
}

func endpointServiceLabel(e osclient.Endpoint) string {
	switch {
	case e.ServiceType != "":
		return e.ServiceType
	case e.ServiceName != "":
		return e.ServiceName
	default:
		return shortID(e.ServiceID)
	}
}

func endpointLabelName(e osclient.Endpoint) string {
	label := endpointServiceLabel(e) + "/" + e.Interface
	if e.RegionID != "" {
		label += "@" + e.RegionID
	}
	return label
}

// --- known-object caches --------------------------------------------------

func (m *Model) rememberServices(ss []osclient.Service) {
	for _, s := range ss {
		m.knownServices[s.ID] = s
	}
}

func (m *Model) rememberRegions(rs []osclient.Region) {
	for _, r := range rs {
		m.knownRegions[r.ID] = r
	}
}

// rememberEndpointRefs records bare service/region references carried by an
// endpoint, so opening an endpoint's service or region resolves even when those
// lists were never visited.
func (m *Model) rememberEndpointRefs(es []osclient.Endpoint) {
	for _, e := range es {
		if e.ServiceID != "" {
			if _, ok := m.knownServices[e.ServiceID]; !ok {
				m.knownServices[e.ServiceID] = osclient.Service{ID: e.ServiceID, Type: e.ServiceType, Name: e.ServiceName}
			}
		}
		if e.RegionID != "" {
			if _, ok := m.knownRegions[e.RegionID]; !ok {
				m.knownRegions[e.RegionID] = osclient.Region{ID: e.RegionID}
			}
		}
	}
}

func (m Model) serviceNode(id string) *model.Node {
	if s, ok := m.knownServices[id]; ok {
		return serviceToNode(s)
	}
	return nil
}

func (m Model) endpointNode(id string) *model.Node {
	for _, e := range m.endpoints {
		if e.ID == id {
			return endpointToNode(e)
		}
	}
	return nil
}

func (m Model) regionNode(id string) *model.Node {
	if r, ok := m.knownRegions[id]; ok {
		return regionToNode(r)
	}
	return nil
}

// --- services list --------------------------------------------------------

func serviceEntries(list []osclient.Service) []entry {
	es := make([]entry, 0, len(list))
	for _, s := range list {
		es = append(es, entry{
			kind: entService, service: s, label: "service:" + serviceLabelName(s),
			extra: joinRelatedRowAttrs(s.Name, s.Description),
		})
	}
	return es
}

func serviceColumnTitles(showIDs bool) []string {
	obj := "TYPE"
	if showIDs {
		obj = "SERVICE ID"
	}
	return []string{obj, "NAME", "ENABLED", "DESCRIPTION"}
}

func serviceRowCells(e entry, showIDs bool) []string {
	s := e.service
	first := s.Type
	if showIDs {
		first = s.ID
	}
	return []string{displayValue(first), displayValue(s.Name), enabledYesNo(s.Enabled), displayValue(s.Description)}
}

func serviceToNode(s osclient.Service) *model.Node {
	n := model.NewNode(model.TypeService, s.ID, serviceLabelName(s))
	n.SetAttr("type", s.Type)
	n.SetAttr("name", s.Name)
	n.SetAttr("description", s.Description)
	if s.Enabled {
		n.SetAttr("enabled", "true")
	} else {
		n.SetAttr("enabled", "false")
	}
	n.DetailLoaded = true
	n.Raw = map[string]any{"id": s.ID, "type": s.Type, "name": s.Name, "description": s.Description, "enabled": s.Enabled}
	return n
}

func (m Model) isServiceOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeService
}

func (m Model) serviceOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.serviceOverviewSummary, m.identityRelatedEmptyMsg("endpoints", m.endpointsLoadingFor()))
}

func (m Model) serviceOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "SERVICE DETAILS", m.serviceDetailGroups())
}

func (m Model) serviceDetailGroups() []overviewGroup {
	n := m.loc.node
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Type", value: displayValue(n.Attrs["type"])},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Name", value: displayValue(n.Attrs["name"])},
			{label: "Description", value: displayValue(n.Attrs["description"])},
		}},
		{title: "STATE", fields: []overviewField{
			{label: "Enabled", value: enabledDisplay(n.Attrs["enabled"])},
		}},
	}
}

// serviceRelatedEntries lists the regions a service is present in and its
// endpoints, both derived from the shared endpoints list once it has loaded.
func (m Model) serviceRelatedEntries(n *model.Node) []entry {
	if n == nil || !m.endpointsLoaded {
		return nil
	}
	var eps []osclient.Endpoint
	for _, e := range m.endpoints {
		if e.ServiceID == n.ID {
			eps = append(eps, e)
		}
	}
	out := regionEntries(m.serviceRegions(n.ID))
	out = append(out, endpointEntries(eps)...)
	return out
}

// serviceRegions returns the distinct regions a service has an endpoint in,
// enriched from the known-region cache where available.
func (m Model) serviceRegions(serviceID string) []osclient.Region {
	seen := map[string]bool{}
	var out []osclient.Region
	for _, e := range m.endpoints {
		if e.ServiceID != serviceID || e.RegionID == "" || seen[e.RegionID] {
			continue
		}
		seen[e.RegionID] = true
		r := osclient.Region{ID: e.RegionID}
		if known, ok := m.knownRegions[e.RegionID]; ok {
			r = known
		}
		out = append(out, r)
	}
	return out
}

// regionServices returns the distinct services with an endpoint in a region,
// enriched from the known-service cache where available.
func (m Model) regionServices(regionID string) []osclient.Service {
	seen := map[string]bool{}
	var out []osclient.Service
	for _, e := range m.endpoints {
		if e.RegionID != regionID || e.ServiceID == "" || seen[e.ServiceID] {
			continue
		}
		seen[e.ServiceID] = true
		s := osclient.Service{ID: e.ServiceID, Type: e.ServiceType, Name: e.ServiceName}
		if known, ok := m.knownServices[e.ServiceID]; ok {
			s = known
		}
		out = append(out, s)
	}
	return out
}

// --- endpoints list -------------------------------------------------------

func endpointEntries(list []osclient.Endpoint) []entry {
	es := make([]entry, 0, len(list))
	for _, e := range list {
		es = append(es, entry{
			kind: entEndpoint, endpoint: e, label: "endpoint:" + endpointLabelName(e),
			extra: strings.TrimSpace(e.URL),
		})
	}
	return es
}

func endpointColumnTitles(showIDs bool) []string {
	svc := "SERVICE"
	if showIDs {
		svc = "SERVICE ID"
	}
	return []string{svc, "INTERFACE", "REGION", "URL", "ENABLED"}
}

func endpointRowCells(e entry, showIDs bool) []string {
	ep := e.endpoint
	svc := endpointServiceLabel(ep)
	if showIDs {
		svc = ep.ServiceID
	}
	return []string{displayValue(svc), displayValue(ep.Interface), displayValue(ep.RegionID), displayValue(ep.URL), enabledYesNo(ep.Enabled)}
}

func endpointToNode(e osclient.Endpoint) *model.Node {
	n := model.NewNode(model.TypeEndpoint, e.ID, endpointLabelName(e))
	n.SetAttr("interface", e.Interface)
	n.SetAttr("url", e.URL)
	n.SetAttr("description", e.Description)
	n.SetAttr("service_id", e.ServiceID)
	n.SetAttr("service_type", e.ServiceType)
	n.SetAttr("service_name", e.ServiceName)
	n.SetAttr("region_id", e.RegionID)
	if e.Enabled {
		n.SetAttr("enabled", "true")
	} else {
		n.SetAttr("enabled", "false")
	}
	n.DetailLoaded = true
	n.Raw = map[string]any{
		"id": e.ID, "interface": e.Interface, "url": e.URL, "description": e.Description,
		"service_id": e.ServiceID, "region_id": e.RegionID, "enabled": e.Enabled,
	}
	return n
}

func (m Model) isEndpointOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeEndpoint
}

func (m Model) endpointOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.endpointOverviewSummary, "— no related objects —")
}

func (m Model) endpointOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "ENDPOINT DETAILS", m.endpointDetailGroups())
}

func (m Model) endpointDetailGroups() []overviewGroup {
	n := m.loc.node
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Interface", value: displayValue(n.Attrs["interface"])},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Region", value: displayValue(n.Attrs["region_id"])},
			{label: "URL", value: displayValue(n.Attrs["url"])},
			{label: "Description", value: displayValue(n.Attrs["description"])},
		}},
		{title: "STATE", fields: []overviewField{
			{label: "Enabled", value: enabledDisplay(n.Attrs["enabled"])},
		}},
	}
}

// endpointRelatedEntries links an endpoint to its service and its region, both
// resolvable from the known-object caches.
func (m Model) endpointRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	var out []entry
	if sid := n.Attrs["service_id"]; sid != "" {
		svc := osclient.Service{ID: sid, Type: n.Attrs["service_type"], Name: n.Attrs["service_name"]}
		out = append(out, entry{kind: entService, service: svc, label: "service:" + serviceLabelName(svc)})
	}
	if rid := n.Attrs["region_id"]; rid != "" {
		out = append(out, entry{kind: entRegion, region: osclient.Region{ID: rid}, label: "region:" + rid})
	}
	return out
}

// --- regions list ---------------------------------------------------------

func regionEntries(list []osclient.Region) []entry {
	es := make([]entry, 0, len(list))
	for _, r := range list {
		parent := ""
		if r.ParentRegionID != "" {
			parent = "parent " + r.ParentRegionID
		}
		extra := joinRelatedRowAttrs(parent, r.Description)
		es = append(es, entry{kind: entRegion, region: r, label: "region:" + r.ID, extra: extra})
	}
	return es
}

func regionColumnTitles(bool) []string {
	return []string{"REGION", "PARENT", "DESCRIPTION"}
}

func regionRowCells(e entry, _ bool) []string {
	r := e.region
	return []string{displayValue(r.ID), displayValue(r.ParentRegionID), displayValue(r.Description)}
}

func regionToNode(r osclient.Region) *model.Node {
	n := model.NewNode(model.TypeRegion, r.ID, r.ID)
	n.SetAttr("description", r.Description)
	n.SetAttr("parent_region_id", r.ParentRegionID)
	n.DetailLoaded = true
	n.Raw = map[string]any{"id": r.ID, "description": r.Description, "parent_region_id": r.ParentRegionID}
	return n
}

func (m Model) isRegionOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeRegion
}

func (m Model) regionOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.regionOverviewSummary, m.identityRelatedEmptyMsg("endpoints", m.endpointsLoadingFor()))
}

func (m Model) regionOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "REGION DETAILS", m.regionDetailGroups())
}

func (m Model) regionDetailGroups() []overviewGroup {
	n := m.loc.node
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "ID", value: displayValue(n.ID)},
			{label: "Description", value: displayValue(n.Attrs["description"])},
			{label: "Parent", value: displayValue(n.Attrs["parent_region_id"])},
		}},
	}
}

// regionRelatedEntries lists a region's parent (when nested) and the endpoints
// located in it (derived from the shared endpoints list).
func (m Model) regionRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	var out []entry
	if pid := n.Attrs["parent_region_id"]; pid != "" {
		out = append(out, entry{kind: entRegion, region: osclient.Region{ID: pid}, label: "region:" + pid})
	}
	if m.endpointsLoaded {
		var eps []osclient.Endpoint
		for _, e := range m.endpoints {
			if e.RegionID == n.ID {
				eps = append(eps, e)
			}
		}
		out = append(out, serviceEntries(m.regionServices(n.ID))...)
		out = append(out, endpointEntries(eps)...)
	}
	return out
}

// endpointsLoadingFor reports whether the shared endpoints list is in flight, so
// a service/region detail can show "loading endpoints…" before it arrives. The
// map is keyed only to satisfy identityRelatedEmptyMsg's signature.
func (m Model) endpointsLoadingFor() map[string]bool {
	if m.loc.node == nil {
		return nil
	}
	return map[string]bool{m.loc.node.ID: m.endpointsLoading && !m.endpointsLoaded}
}

// enabledYesNo renders a boolean enabled flag for a list cell.
func enabledYesNo(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

// --- current-token / whoami overlay ---------------------------------------

// openToken snapshots the active token (a local read of the cached auth result,
// no network) and shows it in a centered pop-up.
func (m Model) openToken() (tea.Model, tea.Cmd) {
	m.overlay = overlayToken
	m.tokenInfo = m.backend.CurrentToken()
	return m, nil
}

// tokenView renders the current-token pop-up centered over the list.
func (m Model) tokenView() string {
	return overlayCenter(m.listView(), m.tokenModalBox(), m.width, m.height)
}

func (m Model) tokenModalBox() string {
	t := m.tokenInfo
	title := "Current token"
	footer := "esc / * close"
	type kv struct{ k, v string }
	var rows []kv
	if !t.Available {
		rows = []kv{{"", "token details are unavailable"}}
	} else {
		user := displayValue(t.UserName)
		if t.UserDomain != "" {
			user += "  @" + t.UserDomain
		}
		roles := "—"
		if len(t.Roles) > 0 {
			roles = strings.Join(t.Roles, ", ")
		}
		rows = []kv{
			{"User", user},
			{"Scope", tokenScopeLine(t)},
			{"Roles", roles},
			{"Expires", m.tokenExpiryLine(t)},
		}
	}

	labelW := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.k); w > labelW {
			labelW = w
		}
	}
	iw := max(lipgloss.Width(title), lipgloss.Width(footer))
	rendered := make([]string, len(rows))
	for i, r := range rows {
		text := r.v
		if r.k != "" {
			text = padRight(r.k, labelW) + "   " + r.v
		}
		rendered[i] = text
		if w := lipgloss.Width(text); w > iw {
			iw = w
		}
	}
	lines := []string{m.st.modalTitle.Width(iw).Render(title), m.st.modalRow.Width(iw).Render("")}
	for _, text := range rendered {
		lines = append(lines, m.st.modalRow.Width(iw).Render(text))
	}
	lines = append(lines, m.st.modalRow.Width(iw).Render(""), m.st.modalHelp.Width(iw).Render(footer))
	return m.st.modalFrame.Render(strings.Join(lines, "\n"))
}

func tokenScopeLine(t osclient.TokenInfo) string {
	switch t.ScopeType {
	case "project":
		s := "project " + displayValue(t.ScopeName)
		if t.ScopeDomain != "" {
			s += "  (domain " + t.ScopeDomain + ")"
		}
		return s
	case "domain":
		return "domain " + displayValue(t.ScopeName)
	case "system":
		return "system"
	default:
		return "unscoped"
	}
}

func (m Model) tokenExpiryLine(t osclient.TokenInfo) string {
	if t.ExpiresAt.IsZero() {
		return "—"
	}
	abs := t.ExpiresAt.UTC().Format("2006-01-02 15:04 MST")
	d := t.ExpiresAt.Sub(m.clock())
	if d <= 0 {
		return abs + "  (expired)"
	}
	return abs + "  (in " + shortDuration(d) + ")"
}

// shortDuration renders a coarse "5h32m" / "12m" remaining-time label.
func shortDuration(d time.Duration) string {
	d = d.Truncate(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%dh%dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
