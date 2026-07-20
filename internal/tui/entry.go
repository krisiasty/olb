package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// entryKind classifies a visible row. Group headings are deliberately not
// selectable; every other kind is a navigation target.
type entryKind int

const (
	entLB       entryKind = iota // a load balancer in the top-level list
	entVIP                       // a VIP in the top-level VIPs list
	entListener                  // a listener in the top-level listeners list
	entPool                      // a pool in the top-level pools list
	entAmphora                   // an amphora in the top-level amphorae list
	entChild                     // a containment child of the current node
	entRelated                   // a directly related object rendered as a normal link
	entRef                       // an outgoing reference edge ("→")
	entBackRef                   // an incoming back-reference ("←")
	entGroup                     // a non-selectable related-object group heading
)

// entry is one visible row. Selectable rows follow their containment child or
// reference when opened; group headings only divide the related-object list.
type entry struct {
	kind entryKind

	lb       osclient.LB          // set for entLB
	vip      vipRow               // set for entVIP
	listener osclient.ListenerRow // set for entListener
	pool     osclient.PoolRow     // set for entPool
	lbName   string               // owning load balancer name for resource rows
	node     *model.Node          // child node, resolved edge target, or amphora row
	edge     *model.Edge          // set for entRef / entBackRef

	label         string // target label, e.g. "pool:backend-v2"
	relationship  string // edge relationship, e.g. "default pool"
	oper          string
	prov          string
	extra         string // list-only trailing facts (provider, vip)
	showID        bool   // disambiguate duplicate sibling names
	issueErrors   int    // entGroup visible ERROR count
	issueDegraded int    // entGroup visible DEGRADED count
}

// entrySelection is the durable part of a selectable row. It deliberately
// excludes its position and graph pointers so a refresh can find the same
// object even when the returned rows have been reordered.
type entrySelection struct {
	kind         entryKind
	identity     model.Identity
	relationship string
	targetType   model.NodeType
	targetID     string
}

func (e entry) selection() entrySelection {
	id, _, _ := e.identity()
	s := entrySelection{kind: e.kind, identity: id, relationship: e.relationship}
	if e.edge != nil {
		s.targetType = e.edge.TargetType
		s.targetID = e.edge.TargetID
		if e.edge.Target != nil {
			target := e.edge.Target.Identity()
			if s.targetType == "" {
				s.targetType = target.Type
			}
			if s.targetID == "" {
				s.targetID = target.ID
			}
		}
	}
	return s
}

func (e entry) selectable() bool { return e.kind != entGroup }

func (s entrySelection) equal(other entrySelection) bool {
	return s.kind == other.kind &&
		s.identity.Equal(other.identity) &&
		s.relationship == other.relationship &&
		s.targetType == other.targetType &&
		s.targetID == other.targetID
}

func (e entry) filterText() string {
	return strings.ToLower(e.label + " " + e.relationship + " " + e.extra)
}

// identity returns the destination identity when this row is opened, plus
// whether the destination was reached via a reference/back-reference jump (for
// the breadcrumb's ↦ marker) and whether the target still needs resolving.
func (e entry) identity() (id model.Identity, viaRef bool, unresolved bool) {
	switch e.kind {
	case entLB:
		return model.Identity{Type: model.TypeLoadBalancer, ID: e.lb.ID, OwningLBID: e.lb.ID, Label: lbLabel(e.lb)}, false, false
	case entListener:
		return model.Identity{Type: model.TypeListener, ID: e.listener.ID, OwningLBID: e.listener.LBID, Label: e.label}, false, false
	case entPool:
		return model.Identity{Type: model.TypePool, ID: e.pool.ID, OwningLBID: e.pool.LBID, Label: e.label}, false, false
	case entVIP:
		// The VIP is a node in its owning LB's tree; fall back to the LB itself
		// when the port id (its node key) is unknown.
		if e.vip.nodeID != "" {
			return model.Identity{Type: model.TypeVIP, ID: e.vip.nodeID, OwningLBID: e.vip.lbID, Label: e.label}, false, false
		}
		return lbIdentity(e.vip.lbID, e.lbName), false, false
	case entAmphora:
		// Amphorae are not part of the status tree fetched on drill-in, so open
		// the owning load balancer, whose overview lists them.
		return lbIdentity(e.node.OwningLBID, e.lbName), false, false
	case entChild:
		return e.node.Identity(), false, false
	case entRelated:
		return e.node.Identity(), false, false
	case entRef, entBackRef:
		if e.edge.Target != nil {
			return e.edge.Target.Identity(), true, false
		}
		return model.Identity{}, true, e.edge.Unresolved
	}
	return model.Identity{}, false, false
}

// lbEntries builds the top-level load balancer list rows. When showProject is
// set (all-projects mode) each row is prefixed with its owning project so the
// aggregated list stays legible.
func lbEntries(lbs []osclient.LB, showProject bool) []entry {
	es := make([]entry, 0, len(lbs))
	for _, lb := range lbs {
		extra := lb.Provider
		if lb.VipAddress != "" {
			extra = strings.TrimSpace(extra + " " + lb.VipAddress)
		}
		if showProject {
			proj := lb.ProjectName
			if proj == "" {
				proj = shortID(lb.ProjectID)
			}
			extra = strings.TrimSpace("@" + proj + "  " + extra)
		}
		es = append(es, entry{
			kind: entLB, lb: lb, label: lbLabel(lb),
			oper: lb.OperatingStatus, prov: lb.ProvisioningStatus, extra: extra,
		})
	}
	return es
}

// nodeEntries builds the rows for a node's location: its containment children,
// then its outgoing reference edges, then the back-references answering
// "who points at me?".
func nodeEntries(n *model.Node) []entry {
	children := n.Children
	if n.Type == model.TypeLoadBalancer {
		children = append([]*model.Node(nil), n.Children...)
		sort.SliceStable(children, func(i, j int) bool {
			left, right := children[i], children[j]
			leftRank, rightRank := relatedObjectRank(left.Type), relatedObjectRank(right.Type)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			if left.Type != right.Type {
				return left.Type < right.Type
			}
			if left.Type == model.TypeVIP {
				leftPrimary := left.Attrs["vip_kind"] != "additional"
				rightPrimary := right.Attrs["vip_kind"] != "additional"
				if leftPrimary != rightPrimary {
					return leftPrimary
				}
			}
			leftName := strings.ToLower(strings.TrimSpace(left.Name))
			rightName := strings.ToLower(strings.TrimSpace(right.Name))
			if leftName != rightName {
				return leftName < rightName
			}
			return left.ID < right.ID
		})
	}

	poolNameCounts := map[string]int{}
	for _, child := range children {
		if child.Type == model.TypePool && child.Name != "" {
			poolNameCounts[strings.ToLower(child.Name)]++
		}
	}
	var es []entry
	for _, c := range children {
		es = append(es, entry{
			kind: entChild, node: c, label: c.Label(),
			oper: c.OperatingStatus, prov: c.ProvisioningStatus, extra: inlineAttrs(c),
			showID: c.Type == model.TypePool && poolNameCounts[strings.ToLower(c.Name)] > 1,
		})
	}
	for _, ref := range n.Refs {
		es = append(es, refEntry(entRef, ref))
	}
	for _, br := range n.BackRefs {
		es = append(es, refEntry(entBackRef, br))
	}
	return es
}

// locationEntries adapts the graph to a resource-specific related-object
// surface. A VIP's floating IP is rendered as a detail field, leaving its
// owning load balancer as the sole navigable related object. Members and health
// monitors are details-only: their owning pool is already in the breadcrumb.
func locationEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	if n.Type == model.TypeVIP {
		if n.Parent == nil || n.Parent.Type != model.TypeLoadBalancer {
			return nil
		}
		lb := n.Parent
		return []entry{{
			kind: entRelated, node: lb, label: lb.Label(),
			oper: lb.OperatingStatus, prov: lb.ProvisioningStatus,
			extra: inlineAttrs(lb),
		}}
	}
	if n.Type == model.TypeListener && n.Parent != nil && n.Parent.Type == model.TypeLoadBalancer {
		lb := n.Parent
		entries := []entry{{
			kind: entRelated, node: lb, label: lb.Label(),
			oper: lb.OperatingStatus, prov: lb.ProvisioningStatus, extra: inlineAttrs(lb),
		}}
		related := nodeEntries(n)
		// Pools are graph references rather than listener children, but this view
		// presents them as related objects. Render resolved pool targets with the
		// same type label, status dot, name and summary used by the LB overview;
		// the graph edge remains attached for durable selection identity.
		poolNameCounts := map[string]int{}
		for i := range related {
			if related[i].kind != entRef || related[i].node == nil || related[i].node.Type != model.TypePool {
				continue
			}
			related[i].kind = entRelated
			poolNameCounts[strings.ToLower(related[i].node.Name)]++
		}
		for i := range related {
			if related[i].kind == entRelated && related[i].node != nil && related[i].node.Type == model.TypePool {
				related[i].showID = related[i].node.Name != "" && poolNameCounts[strings.ToLower(related[i].node.Name)] > 1
			}
		}
		return append(entries, related...)
	}
	if n.Type == model.TypePool {
		entries := make([]entry, 0, len(n.Children)+len(n.Refs)+len(n.BackRefs)+1)
		if n.Parent != nil && n.Parent.Type == model.TypeLoadBalancer {
			lb := n.Parent
			entries = append(entries, entry{
				kind: entRelated, node: lb, label: lb.Label(),
				oper: lb.OperatingStatus, prov: lb.ProvisioningStatus, extra: inlineAttrs(lb),
			})
		}
		related := nodeEntries(n)
		for i := range related {
			if related[i].kind == entBackRef && related[i].node != nil && related[i].node.Type == model.TypeListener {
				related[i].kind = entRelated
			}
		}
		sort.SliceStable(related, func(i, j int) bool {
			return poolRelatedObjectRank(related[i]) < poolRelatedObjectRank(related[j])
		})
		return append(entries, related...)
	}
	if n.Type == model.TypeHealthMonitor {
		return nil
	}
	if n.Type == model.TypeMember {
		return nil
	}
	return nodeEntries(n)
}

func poolRelatedObjectRank(e entry) int {
	if e.node == nil {
		return 5
	}
	switch e.node.Type {
	case model.TypeListener:
		return 0
	case model.TypeL7Policy:
		return 1
	case model.TypeHealthMonitor:
		return 2
	case model.TypeMember:
		return 3
	default:
		return 4
	}
}

// withRelatedGroupHeadings adds compact, non-selectable boundaries to overview
// related-object lists after filtering, so each count reflects visible rows.
func withRelatedGroupHeadings(entries []entry) []entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]entry, 0, len(entries)+4)
	for start := 0; start < len(entries); {
		key, title := relatedObjectGroup(entries[start])
		end := start + 1
		for end < len(entries) {
			nextKey, _ := relatedObjectGroup(entries[end])
			if nextKey != key {
				break
			}
			end++
		}
		errors, degraded := relatedIssueCounts(entries[start:end])
		out = append(out, entry{
			kind: entGroup, label: fmt.Sprintf("%s %d", title, end-start),
			issueErrors: errors, issueDegraded: degraded,
		})
		out = append(out, entries[start:end]...)
		start = end
	}
	return out
}

func relatedObjectGroup(e entry) (key, title string) {
	switch e.kind {
	case entRelated:
		if e.node != nil {
			switch e.node.Type {
			case model.TypeLoadBalancer:
				return "load-balancers", "LOAD BALANCERS"
			case model.TypeListener:
				return "listeners", "LISTENERS"
			case model.TypePool:
				return "pools", "POOLS"
			case model.TypeL7Policy:
				return "l7-policies", "L7 POLICIES"
			}
		}
		return "related", "RELATED"
	case entChild:
		if e.node != nil {
			switch e.node.Type {
			case model.TypeVIP:
				return "vips", "VIPS"
			case model.TypeListener:
				return "listeners", "LISTENERS"
			case model.TypePool:
				return "pools", "POOLS"
			case model.TypeAmphora:
				return "amphorae", "AMPHORAE"
			case model.TypeL7Policy:
				return "l7-policies", "L7 POLICIES"
			case model.TypeHealthMonitor:
				return "health-monitors", "HEALTH MONITORS"
			case model.TypeMember:
				return "members", "MEMBERS"
			}
		}
		return "other", "OTHER"
	case entRef:
		if e.edge != nil {
			switch e.edge.TargetType {
			case model.TypePool:
				return "pools", "POOLS"
			case model.TypeInstance:
				return "instances", "INSTANCES"
			}
		}
		return "references", "REFERENCES"
	case entBackRef:
		if e.node != nil {
			switch e.node.Type {
			case model.TypeListener:
				return "listeners", "LISTENERS"
			case model.TypeL7Policy:
				return "l7-policies", "L7 POLICIES"
			}
		}
		return "referenced-by", "REFERENCED BY"
	default:
		return "other", "OTHER"
	}
}

func selectableEntryCount(entries []entry) int {
	count := 0
	for _, e := range entries {
		if e.selectable() {
			count++
		}
	}
	return count
}

// relatedIssueCounts summarizes only rows in the visible related-object list.
// An object with both statuses is counted once at its highest severity.
func relatedIssueCounts(entries []entry) (errors, degraded int) {
	for _, e := range entries {
		if !e.selectable() {
			continue
		}
		switch {
		case strings.EqualFold(e.oper, "ERROR"), strings.EqualFold(e.prov, "ERROR"):
			errors++
		case strings.EqualFold(e.oper, "DEGRADED"), strings.EqualFold(e.prov, "DEGRADED"):
			degraded++
		}
	}
	return errors, degraded
}

func firstSelectableIndex(entries []entry) int {
	for i := range entries {
		if entries[i].selectable() {
			return i
		}
	}
	return -1
}

func lastSelectableIndex(entries []entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].selectable() {
			return i
		}
	}
	return -1
}

// nearestSelectableIndex prefers the row at or below cursor, which keeps the
// first object in a newly inserted group selected. It falls back upward at the
// end of the list.
func nearestSelectableIndex(entries []entry, cursor int) int {
	if len(entries) == 0 {
		return -1
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(entries) {
		cursor = len(entries) - 1
	}
	for i := cursor; i < len(entries); i++ {
		if entries[i].selectable() {
			return i
		}
	}
	for i := cursor - 1; i >= 0; i-- {
		if entries[i].selectable() {
			return i
		}
	}
	return -1
}

func relatedObjectRank(t model.NodeType) int {
	switch t {
	case model.TypeVIP:
		return 0
	case model.TypeListener:
		return 1
	case model.TypePool:
		return 2
	case model.TypeAmphora:
		return 3
	default:
		return 4
	}
}

func refEntry(kind entryKind, edge *model.Edge) entry {
	e := entry{kind: kind, edge: edge, relationship: edge.Label}
	switch {
	case edge.Target != nil:
		e.node = edge.Target
		e.label = edge.Target.Label()
		e.oper = edge.Target.OperatingStatus
		e.prov = edge.Target.ProvisioningStatus
		e.extra = inlineAttrs(edge.Target)
	case edge.Unresolved:
		e.label = string(edge.TargetType) // e.g. "floatingip", "instance"
	case edge.Missing:
		e.label = string(edge.TargetType) + " (unavailable)"
	default:
		e.label = model.NodeType(edge.TargetType).Short() + ":" + shortID(edge.TargetID)
	}
	return e
}

func lbLabel(lb osclient.LB) string {
	if lb.Name != "" {
		return "lb:" + lb.Name
	}
	return "lb:" + shortID(lb.ID)
}

// lbIdentity builds a load-balancer identity for drilling in from a resource row
// that only knows its owning LB's id and (maybe) name.
func lbIdentity(lbID, lbName string) model.Identity {
	label := "lb:" + lbName
	if lbName == "" {
		label = "lb:" + shortID(lbID)
	}
	return model.Identity{Type: model.TypeLoadBalancer, ID: lbID, OwningLBID: lbID, Label: label}
}

// inlineAttrs renders a node's most useful facts for the trailing column.
func inlineAttrs(n *model.Node) string {
	switch n.Type {
	case model.TypeLoadBalancer:
		return loadBalancerSummary(n)
	case model.TypeListener:
		return listenerSummary(n)
	case model.TypePool:
		listenerCount := n.Attrs["listener_count"]
		if listenerCount == "" {
			listenerCount = fmt.Sprintf("%d", poolListenerAttachmentCount(n))
		}
		return poolSummary(n.Attrs["protocol"], n.Attrs["lb_algorithm"], n.Attrs["member_count"], listenerCount)
	case model.TypeAmphora:
		return amphoraSummary(n)
	case model.TypeMember:
		return joinAttrs(n, "address", "port")
	case model.TypeHealthMonitor:
		return healthMonitorSummary(n)
	case model.TypeL7Policy:
		return joinAttrs(n, "action")
	case model.TypeL7Rule:
		return joinAttrs(n, "type")
	default:
		return ""
	}
}

func healthMonitorSummary(n *model.Node) string {
	parts := make([]string, 0, 6)
	monitorType := strings.TrimSpace(n.Attrs["type"])
	if monitorType != "" {
		parts = append(parts, monitorType)
	}
	if delay := strings.TrimSpace(n.Attrs["delay"]); delay != "" {
		parts = append(parts, "every "+delay+"s")
	}
	if timeout := strings.TrimSpace(n.Attrs["timeout"]); timeout != "" {
		parts = append(parts, "timeout "+timeout+"s")
	}
	up, down := strings.TrimSpace(n.Attrs["max_retries"]), strings.TrimSpace(n.Attrs["max_retries_down"])
	if up != "" && down != "" {
		parts = append(parts, "up/down "+up+"/"+down)
	}
	if strings.EqualFold(monitorType, "HTTP") || strings.EqualFold(monitorType, "HTTPS") {
		request := strings.TrimSpace(strings.TrimSpace(n.Attrs["http_method"]) + " " + strings.TrimSpace(n.Attrs["url_path"]))
		if expected := strings.TrimSpace(n.Attrs["expected_codes"]); request != "" && expected != "" {
			request += " → " + expected
		}
		if request != "" {
			parts = append(parts, request)
		}
	}
	if strings.EqualFold(strings.TrimSpace(n.Attrs["admin_state_up"]), "false") {
		parts = append(parts, "disabled")
	}
	return strings.Join(parts, " · ")
}

func loadBalancerSummary(n *model.Node) string {
	var parts []string
	if provider := strings.TrimSpace(n.Attrs["provider"]); provider != "" {
		parts = append(parts, provider)
	}
	listeners, pools := 0, 0
	for _, child := range n.Children {
		switch child.Type {
		case model.TypeListener:
			listeners++
		case model.TypePool:
			pools++
		}
	}
	parts = append(parts, countedNoun(listeners, "listener"), countedNoun(pools, "pool"))
	return strings.Join(parts, " · ")
}

func countedNoun(count int, noun string) string {
	if count != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s", count, noun)
}

func amphoraSummary(n *model.Node) string {
	var parts []string
	if managementIP := strings.TrimSpace(n.Attrs["lb_network_ip"]); managementIP != "" {
		parts = append(parts, "mgmt "+managementIP)
	}
	if computeID := strings.TrimSpace(n.Attrs["compute_id"]); computeID != "" {
		parts = append(parts, "vm "+shortID(computeID))
	}
	return strings.Join(parts, " · ")
}

func listenerSummary(n *model.Node) string {
	parts := []string{}
	if endpoint := listenerEndpoint(n.Attrs["protocol"], n.Attrs["port"]); endpoint != "" {
		parts = append(parts, endpoint)
	}

	poolIDs := map[string]struct{}{}
	for _, ref := range n.Refs {
		if ref.TargetType == model.TypePool && ref.TargetID != "" {
			poolIDs[ref.TargetID] = struct{}{}
		}
	}
	label := "pools"
	if len(poolIDs) == 1 {
		label = "pool"
	}
	parts = append(parts, fmt.Sprintf("%d %s", len(poolIDs), label))
	return strings.Join(parts, " · ")
}

func listenerEndpoint(protocol, port string) string {
	protocol, terminatedTLS := normalizedListenerProtocol(protocol)
	port = strings.TrimSpace(port)
	endpoint := protocol
	if endpoint != "" && port != "" {
		endpoint += "/" + port
	} else if endpoint == "" {
		endpoint = port
	}
	if terminatedTLS {
		endpoint += " (TLS termination)"
	}
	return endpoint
}

func listenerProtocolLabel(protocol string) string {
	protocol, terminatedTLS := normalizedListenerProtocol(protocol)
	if terminatedTLS {
		return protocol + " (TLS termination)"
	}
	return protocol
}

func normalizedListenerProtocol(protocol string) (string, bool) {
	protocol = strings.ToUpper(strings.TrimSpace(protocol))
	terminatedTLS := protocol == "TERMINATED_HTTPS"
	if terminatedTLS {
		protocol = "HTTPS"
	}
	return protocol, terminatedTLS
}

func poolListenerAttachmentCount(n *model.Node) int {
	listenerIDs := map[string]struct{}{}
	for _, ref := range n.BackRefs {
		if ref.TargetType == model.TypeListener && ref.TargetID != "" {
			listenerIDs[ref.TargetID] = struct{}{}
		}
	}
	return len(listenerIDs)
}

func poolSummary(protocol, algorithm, memberCount, listenerCount string) string {
	var parts []string
	if protocol = strings.ToUpper(strings.TrimSpace(protocol)); protocol != "" {
		parts = append(parts, protocol)
	}
	if algorithm = poolAlgorithmLabel(algorithm); algorithm != "" {
		parts = append(parts, algorithm)
	}
	if memberCount = strings.TrimSpace(memberCount); memberCount != "" {
		label := "members"
		if memberCount == "1" {
			label = "member"
		}
		parts = append(parts, memberCount+" "+label)
	}
	if listenerCount = strings.TrimSpace(listenerCount); listenerCount != "" {
		label := "listeners"
		if listenerCount == "1" {
			label = "listener"
		}
		parts = append(parts, listenerCount+" "+label)
	}
	return strings.Join(parts, " · ")
}

func poolAlgorithmLabel(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "ROUND_ROBIN":
		return "round robin"
	case "LEAST_CONNECTIONS":
		return "least connections"
	case "SOURCE_IP":
		return "source IP"
	case "SOURCE_IP_PORT":
		return "source IP+port"
	default:
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", " "))
	}
}

func joinAttrs(n *model.Node, keys ...string) string {
	var parts []string
	for _, k := range keys {
		if v := n.Attrs[k]; v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// statusFilter cycles all → error → degraded.
type statusFilter int

const (
	statusAll statusFilter = iota
	statusError
	statusDegraded
)

func (f statusFilter) String() string {
	switch f {
	case statusError:
		return "error"
	case statusDegraded:
		return "degraded"
	default:
		return "all"
	}
}

func (f statusFilter) next() statusFilter { return (f + 1) % 3 }

func (f statusFilter) match(oper, prov string) bool {
	switch f {
	case statusError:
		return oper == "ERROR" || prov == "ERROR"
	case statusDegraded:
		return oper == "DEGRADED" || oper == "ERROR" || prov == "ERROR"
	default:
		return true
	}
}
