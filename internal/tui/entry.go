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
	entLB      entryKind = iota // a load balancer in the top-level list
	entChild                    // a containment child of the current node
	entRef                      // an outgoing reference edge ("→")
	entBackRef                  // an incoming back-reference ("←")
	entGroup                    // a non-selectable related-object group heading
)

// entry is one visible row. Selectable rows follow their containment child or
// reference when opened; group headings only divide the related-object list.
type entry struct {
	kind entryKind

	lb   osclient.LB // set for entLB
	node *model.Node // child node, or resolved edge target
	edge *model.Edge // set for entRef / entBackRef

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
	case entChild:
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

// withRelatedGroupHeadings adds compact, non-selectable boundaries to the LB
// overview after filtering, so each count reflects only the visible rows.
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
			}
		}
		return "other", "OTHER"
	case entRef:
		return "references", "REFERENCES"
	case entBackRef:
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

// inlineAttrs renders a node's most useful facts for the trailing column.
func inlineAttrs(n *model.Node) string {
	switch n.Type {
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
		return joinAttrs(n, "type")
	case model.TypeL7Policy:
		return joinAttrs(n, "action")
	case model.TypeL7Rule:
		return joinAttrs(n, "type")
	default:
		return ""
	}
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
	protocol = strings.ToUpper(strings.TrimSpace(protocol))
	port = strings.TrimSpace(port)
	terminatedTLS := protocol == "TERMINATED_HTTPS"
	if terminatedTLS {
		protocol = "HTTPS"
	}
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
