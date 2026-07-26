package tui

import (
	"strings"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

// This file isolates the identity (auth) area's rendering, following the COE
// precedent (kubernetes.go). Users, domains, groups, and projects are the
// identity views; each is a list + detail, and their details cross-link through
// shared related objects (a user's/group's/project's domain, a group's members,
// a user's groups, a domain's projects/groups/users).

// domainContent is a domain's related objects, loaded lazily when it is opened.
type domainContent struct {
	projects []osclient.Project
	groups   []osclient.Group
	users    []osclient.User
}

// roleRelations are a role's related objects: the roles it implies and the
// assignments granting it, loaded lazily when the role is opened.
type roleRelations struct {
	implied     []osclient.Role
	assignments []osclient.RoleAssignment
}

// ownerKind identifies which side of an assignment a related-objects list is
// read from: a user, group, project, or domain detail showing the assignments
// that touch it (the mirror of the role→assignments view).
type ownerKind int

const (
	ownerUser ownerKind = iota
	ownerGroup
	ownerProject
	ownerDomain
)

// assignmentKey keys the shared assignments cache by owner kind + owner ID.
type assignmentKey struct {
	owner ownerKind
	id    string
}

// assignmentPivot is the dimension a related assignment row is being viewed
// from — the one already named by the detail, so the row elides it and shows the
// other two. See assignmentEntries.
type assignmentPivot int

const (
	pivotRole   assignmentPivot = iota // role detail: show actor + target, open the actor
	pivotActor                         // user/group detail: show role + target, open the role
	pivotTarget                        // project/domain detail: show actor + role, open the actor
)

// --- known-object caches --------------------------------------------------
//
// Every identity list — top-level, related, or domain contents — feeds these
// caches so a related-object row can resolve into its detail even when its own
// top-level list was never loaded.

func (m *Model) rememberUsers(us []osclient.User) {
	for _, u := range us {
		m.knownUsers[u.ID] = u
		m.rememberDomainRef(u.DomainID, u.DomainName)
	}
}

func (m *Model) rememberGroups(gs []osclient.Group) {
	for _, g := range gs {
		m.knownGroups[g.ID] = g
		m.rememberDomainRef(g.DomainID, g.DomainName)
	}
}

func (m *Model) rememberProjects(ps []osclient.Project) {
	for _, p := range ps {
		m.knownProjects[p.ID] = p
		m.knownProjectFull[p.ID] = true
		m.rememberDomainRef(p.DomainID, p.DomainName)
	}
}

// rememberProjectRef records a bare project reference (ID + resolved name)
// without clobbering a full project already cached from a project list.
func (m *Model) rememberProjectRef(id, name string) {
	if id == "" || m.knownProjectFull[id] {
		return
	}
	m.knownProjects[id] = osclient.Project{ID: id, Name: name}
}

// rememberAssignmentProjects records a bare reference for each project an
// assignment targets, so a project reached only through a role assignment (a
// user/group may hold a role on a project outside the current scope) still opens
// into its detail.
func (m *Model) rememberAssignmentProjects(as []osclient.RoleAssignment) {
	for _, a := range as {
		if a.TargetType == "project" {
			m.rememberProjectRef(a.TargetID, a.TargetName)
		}
	}
}

func (m *Model) rememberRoles(rs []osclient.Role) {
	for _, r := range rs {
		m.knownRoles[r.ID] = r
		m.rememberDomainRef(r.DomainID, r.DomainName)
	}
}

// rememberAssignmentActors records a bare user/group reference (ID + name) for
// each assignment's actor, so opening an assignment can resolve into that actor's
// detail even when its own list was never loaded.
func (m *Model) rememberAssignmentActors(as []osclient.RoleAssignment) {
	for _, a := range as {
		if a.ActorID == "" {
			continue
		}
		if a.ActorType == "group" {
			if _, ok := m.knownGroups[a.ActorID]; !ok {
				m.knownGroups[a.ActorID] = osclient.Group{ID: a.ActorID, Name: a.ActorName}
			}
			continue
		}
		if _, ok := m.knownUsers[a.ActorID]; !ok {
			m.knownUsers[a.ActorID] = osclient.User{ID: a.ActorID, Name: a.ActorName}
		}
	}
}

// rememberAssignmentRoles records a bare role reference (ID + name) for each
// assignment, so an actor-side assignment row can resolve into the role's detail
// even when the roles list was never loaded.
func (m *Model) rememberAssignmentRoles(as []osclient.RoleAssignment) {
	for _, a := range as {
		if a.RoleID == "" {
			continue
		}
		if _, ok := m.knownRoles[a.RoleID]; !ok {
			m.knownRoles[a.RoleID] = osclient.Role{ID: a.RoleID, Name: a.RoleName}
		}
	}
}

func (m *Model) rememberDomains(ds []osclient.Domain) {
	for _, d := range ds {
		m.knownDomains[d.ID] = d
		m.knownDomainFull[d.ID] = !d.Partial
	}
}

// rememberDomainRef records a bare domain reference (ID + resolved name) without
// clobbering a full domain already cached from a domain list.
func (m *Model) rememberDomainRef(id, name string) {
	if id == "" || m.knownDomainFull[id] {
		return
	}
	m.knownDomains[id] = osclient.Domain{ID: id, Name: name}
}

func (m Model) userNode(id string) *model.Node {
	if u, ok := m.knownUsers[id]; ok {
		return userToNode(u)
	}
	return nil
}

func (m Model) groupNode(id string) *model.Node {
	if g, ok := m.knownGroups[id]; ok {
		return groupToNode(g)
	}
	return nil
}

func (m Model) projectNode(id string) *model.Node {
	p, ok := m.knownProjects[id]
	if !ok {
		return nil
	}
	n := projectToNode(p)
	if !m.knownProjectFull[id] {
		// A bare reference: its enabled state is unknown (SetAttr ignores empty
		// values, so clear the map entry directly, mirroring domainNode).
		delete(n.Attrs, "enabled")
	}
	return n
}

func (m Model) domainNode(id string) *model.Node {
	d, ok := m.knownDomains[id]
	if !ok {
		return nil
	}
	n := domainToNode(d)
	if !m.knownDomainFull[id] {
		// A bare reference: its enabled state is unknown (SetAttr ignores empty
		// values, so clear the map entry directly).
		delete(n.Attrs, "enabled")
	}
	return n
}

// --- related-object builders ----------------------------------------------
//
// Each identity object's related list leads with its domain (when it has one),
// followed by type-specific relations. The lists carry group headings inserted
// by withRelatedGroupHeadings; see relatedObjectGroup.

// domainRefEntries renders an object's owning domain as a single related row,
// built from the object's cached domain attributes (no fetch needed).
func domainRefEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	id := n.Attrs["domain_id"]
	if id == "" {
		return nil
	}
	name := n.Attrs["domain_name"]
	label := "domain:" + name
	if name == "" {
		label = "domain:" + shortID(id)
	}
	return []entry{{kind: entDomain, domain: osclient.Domain{ID: id, Name: name}, label: label}}
}

// expectedRelatedSections returns the ordered related-object sections an identity
// detail always shows once its data has loaded — including empty ones, so a
// group with no members still shows "USERS 0". A section backed by an async fetch
// appears only after that fetch completes, so the loading indicator shows first.
// Returns nil for non-identity overviews (which show only their present sections).
func (m Model) expectedRelatedSections() []relatedSection {
	n := m.loc.node
	if n == nil {
		return nil
	}
	assignments := relatedSection{"assignments", "ROLE ASSIGNMENTS"}
	switch {
	case m.isProjectOverview():
		assignments.title = m.assignmentSectionTitle(ownerProject, n.ID, assignments.title)
		secs := []relatedSection{{"domain", "DOMAIN"}}
		if m.assignmentsLoaded[assignmentKey{ownerProject, n.ID}] {
			secs = append(secs, assignments)
		}
		return secs
	case m.isUserOverview():
		secs := []relatedSection{{"domain", "DOMAIN"}}
		if m.userGroupsLoaded[n.ID] {
			secs = append(secs, relatedSection{"groups", "GROUPS"})
		}
		if m.assignmentsLoaded[assignmentKey{ownerUser, n.ID}] {
			// A user's assignments are effective (they include roles inherited through
			// group membership), so the heading says so; the other owners are direct.
			secs = append(secs, relatedSection{"projects", "PROJECTS"}, relatedSection{"assignments", "EFFECTIVE ROLE ASSIGNMENTS"})
		}
		return secs
	case m.isGroupOverview():
		secs := []relatedSection{{"domain", "DOMAIN"}}
		if m.groupMembersLoaded[n.ID] {
			secs = append(secs, relatedSection{"users", "USERS"})
		}
		if m.assignmentsLoaded[assignmentKey{ownerGroup, n.ID}] {
			secs = append(secs, relatedSection{"projects", "PROJECTS"}, assignments)
		}
		return secs
	case m.isDomainOverview():
		assignments.title = m.assignmentSectionTitle(ownerDomain, n.ID, assignments.title)
		haveContents := m.domainContentsLoaded[n.ID]
		haveAssignments := m.assignmentsLoaded[assignmentKey{ownerDomain, n.ID}]
		if !haveContents && !haveAssignments {
			return nil // nothing loaded yet — fall back to the loading indicator
		}
		var secs []relatedSection
		if haveContents {
			secs = append(secs, relatedSection{"projects", "PROJECTS"}, relatedSection{"groups", "GROUPS"}, relatedSection{"users", "USERS"})
		}
		if haveAssignments {
			secs = append(secs, assignments)
		}
		return secs
	case m.isRoleOverview():
		if n.Attrs["token_scoped"] == "true" {
			return nil
		}
		if !m.roleRelationsLoaded[n.ID] {
			return nil
		}
		return []relatedSection{{"roles", "IMPLIED ROLES"}, assignments}
	case m.isServiceOverview():
		if !m.endpointsLoaded {
			return nil
		}
		return []relatedSection{{"regions", "REGIONS"}, {"endpoints", "ENDPOINTS"}}
	case m.isEndpointOverview():
		return []relatedSection{{"services", "SERVICE"}, {"regions", "REGION"}}
	case m.isRegionOverview():
		secs := []relatedSection{{"regions", "PARENT REGION"}}
		if m.endpointsLoaded {
			secs = append(secs, relatedSection{"services", "SERVICES"}, relatedSection{"endpoints", "ENDPOINTS"})
		}
		return secs
	}
	return nil
}

func (m Model) assignmentSectionTitle(owner ownerKind, id, fallback string) string {
	for _, assignment := range m.assignments[assignmentKey{owner: owner, id: id}] {
		if assignment.TokenScoped {
			return "EFFECTIVE ROLE ASSIGNMENTS"
		}
	}
	return fallback
}

func (m Model) userRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	out := domainRefEntries(n)
	if m.userGroupsLoaded[n.ID] {
		out = append(out, groupEntries(m.userGroups[n.ID])...)
	}
	if as, ok := m.ownerAssignments(ownerUser, n.ID); ok {
		if projects, fallback := m.userProjects[n.ID]; fallback {
			out = append(out, projectEntries(projects)...)
		} else {
			out = append(out, assignmentProjectEntries(as)...)
		}
		out = append(out, assignmentEntries(as, pivotActor)...)
	}
	return out
}

func (m Model) groupRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	out := domainRefEntries(n)
	if m.groupMembersLoaded[n.ID] {
		out = append(out, userEntries(m.groupMembers[n.ID])...)
	}
	if as, ok := m.ownerAssignments(ownerGroup, n.ID); ok {
		out = append(out, assignmentProjectEntries(as)...)
		out = append(out, assignmentEntries(as, pivotActor)...)
	}
	return out
}

func (m Model) projectRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	out := domainRefEntries(n)
	if as, ok := m.ownerAssignments(ownerProject, n.ID); ok {
		out = append(out, assignmentEntries(as, pivotTarget)...)
	}
	return out
}

func (m Model) domainRelatedEntries(n *model.Node) []entry {
	if n == nil {
		return nil
	}
	var out []entry
	if m.domainContentsLoaded[n.ID] {
		c := m.domainContents[n.ID]
		out = make([]entry, 0, len(c.projects)+len(c.groups)+len(c.users))
		out = append(out, projectEntries(c.projects)...)
		out = append(out, groupEntries(c.groups)...)
		out = append(out, userEntries(c.users)...)
	}
	if as, ok := m.ownerAssignments(ownerDomain, n.ID); ok {
		out = append(out, assignmentEntries(as, pivotTarget)...)
	}
	return out
}

// ownerAssignments returns the cached role assignments for one identity object
// and whether they have loaded (so callers append the section only once the
// header should show).
func (m Model) ownerAssignments(owner ownerKind, id string) ([]osclient.RoleAssignment, bool) {
	key := assignmentKey{owner: owner, id: id}
	if !m.assignmentsLoaded[key] {
		return nil, false
	}
	return m.assignments[key], true
}

// ownerRelatedEntries dispatches to the related-object builder for an owner kind,
// used when an assignments load completes and the open detail must be rebuilt.
func (m Model) ownerRelatedEntries(owner ownerKind, n *model.Node) []entry {
	switch owner {
	case ownerUser:
		return m.userRelatedEntries(n)
	case ownerGroup:
		return m.groupRelatedEntries(n)
	case ownerProject:
		return m.projectRelatedEntries(n)
	case ownerDomain:
		return m.domainRelatedEntries(n)
	}
	return nil
}

// assignmentOwnerOpen reports whether the detail currently open is exactly the
// object an assignments load was keyed to.
func (m Model) assignmentOwnerOpen(key assignmentKey) bool {
	n := m.loc.node
	if n == nil || n.ID != key.id {
		return false
	}
	switch key.owner {
	case ownerUser:
		return m.isUserOverview()
	case ownerGroup:
		return m.isGroupOverview()
	case ownerProject:
		return m.isProjectOverview()
	case ownerDomain:
		return m.isDomainOverview()
	}
	return false
}

// --- users list -----------------------------------------------------------

func userEntries(users []osclient.User) []entry {
	es := make([]entry, 0, len(users))
	for _, u := range users {
		name := u.Name
		if name == "" {
			name = shortID(u.ID)
		}
		extraParts := []string{u.Description, u.Email, u.DomainName}
		if u.Service {
			// A "service" tag on related-object rows and, via filterText, makes
			// service accounts filterable by that word in the list.
			extraParts = append(extraParts, "service")
		}
		es = append(es, entry{
			kind: entUser, user: u, label: "user:" + name,
			extra: joinRelatedRowAttrs(extraParts...),
		})
	}
	return es
}

// serviceMarker is the fixed-width gutter shown before a user's name in the list:
// a marker for a service/system account, blanks otherwise, so names stay aligned.
// The gear reads as "machine/service" and is deliberately not a round glyph, so it
// isn't confused with the ● / · dots used elsewhere.
func serviceMarker(service bool) string {
	if service {
		return "⚙ "
	}
	return "  "
}

// userColumnTitles are the table headers for the users list. The d toggle
// relabels the name column to its id form, matching the other list views.
func userColumnTitles(showIDs bool) []string {
	obj := "NAME"
	if showIDs {
		obj = "USER ID"
	}
	return []string{obj, "DESCRIPTION", "EMAIL", "DOMAIN", "ENABLED"}
}

func userRowCells(e entry, showIDs bool) []string {
	u := e.user
	enabled := "—"
	if u.Enabled {
		enabled = "yes"
	} else if !u.Partial {
		enabled = "no"
	}
	name := serviceMarker(u.Service) + lbNameCell(u.Name, u.ID, showIDs)
	return []string{name, displayValue(u.Description), displayValue(u.Email), lbNameCell(u.DomainName, u.DomainID, showIDs), enabled}
}

// --- groups list ----------------------------------------------------------

func groupEntries(groupList []osclient.Group) []entry {
	es := make([]entry, 0, len(groupList))
	for _, g := range groupList {
		name := g.Name
		if name == "" {
			name = shortID(g.ID)
		}
		es = append(es, entry{
			kind: entUserGroup, group: g, label: "group:" + name,
			extra: joinRelatedRowAttrs(g.Description, g.DomainName),
		})
	}
	return es
}

func groupColumnTitles(showIDs bool) []string {
	obj := "NAME"
	if showIDs {
		obj = "GROUP ID"
	}
	return []string{obj, "DESCRIPTION", "DOMAIN"}
}

func groupRowCells(e entry, showIDs bool) []string {
	g := e.group
	return []string{lbNameCell(g.Name, g.ID, showIDs), displayValue(g.Description), lbNameCell(g.DomainName, g.DomainID, showIDs)}
}

// --- group detail ---------------------------------------------------------

func groupToNode(g osclient.Group) *model.Node {
	n := model.NewNode(model.TypeGroup, g.ID, g.Name)
	n.SetAttr("description", g.Description)
	n.SetAttr("domain_id", g.DomainID)
	n.SetAttr("domain_name", g.DomainName)
	n.DetailLoaded = true
	n.Raw = map[string]any{
		"id": g.ID, "name": g.Name, "description": g.Description, "domain_id": g.DomainID,
	}
	return n
}

func (m Model) isGroupOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeGroup
}

// identityOverviewParts splits the body into a fixed detail summary and a
// scrollable related list, mirroring the pool overview's allocation so the
// related list keeps at least a few rows. summary renders the detail panel to a
// budget of lines.
func (m Model) identityOverviewParts(h int, summary func(budget int) []string) (sum []string, relatedHeight int) {
	const fixedChrome = 2 // top gap + gap before the related list (headings live in the rows)
	if h <= fixedChrome {
		return nil, 0
	}
	minRelated := 1
	if len(m.entries) > 0 {
		selectable := 0
		for i, e := range m.entries {
			if e.selectable() {
				selectable++
			}
			minRelated = i + 1
			if selectable == 3 {
				break
			}
		}
	}
	if minRelated > h-fixedChrome {
		minRelated = h - fixedChrome
	}
	sum = summary(h - fixedChrome - minRelated)
	relatedHeight = h - len(sum) - fixedChrome
	if relatedHeight < 0 {
		relatedHeight = 0
	}
	return sum, relatedHeight
}

// identityOverviewLines renders an identity detail (summary) above a scrollable
// related-object list. The related rows carry their own group headings (DOMAIN,
// GROUPS, MEMBERS, PROJECTS, USERS) inserted by withRelatedGroupHeadings.
func (m Model) identityOverviewLines(h int, summary func(int) []string, emptyMsg string) []string {
	sum, relatedHeight := m.identityOverviewParts(h, summary)
	lines := make([]string, 0, h)
	lines = append(lines, "")
	lines = append(lines, sum...)
	if len(lines) < h {
		lines = append(lines, "")
	}
	lines = append(lines, m.resourceLines(relatedHeight, emptyMsg)...)
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// identityDetailSummary renders a titled detail panel from labelled field
// groups, pairing groups two-up on wide terminals.
func (m Model) identityDetailSummary(budget int, title string, groups []overviewGroup) []string {
	if budget <= 0 || m.loc.node == nil {
		return nil
	}
	lines := []string{m.clip(m.st.panelTitle.Render(title))}
	if m.width >= 90 {
		gap := 3
		available := m.width - gap
		leftWidth := available / 2
		rightWidth := available - leftWidth
		i := 0
		for ; i+1 < len(groups); i += 2 {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, strings.Split(m.renderOverviewGroupPair(groups[i], groups[i+1], leftWidth, rightWidth, gap, m.subsectionHeading), "\n")...)
		}
		if i < len(groups) {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, strings.Split(m.renderOverviewGroup(groups[i], m.width, m.subsectionHeading), "\n")...)
		}
		return limitLines(lines, budget)
	}
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(m.renderOverviewGroup(group, m.width, m.subsectionHeading), "\n")...)
	}
	return limitLines(lines, budget)
}

func (m Model) groupOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.groupOverviewSummary, m.identityRelatedEmptyMsg("members", m.groupMembersLoading))
}

func (m Model) groupOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "GROUP DETAILS", m.groupDetailGroups())
}

// identityRelatedEmptyMsg is shown only when the whole related list is empty: a
// loading note while the async part is in flight, otherwise the plain marker.
func (m Model) identityRelatedEmptyMsg(what string, loading map[string]bool) string {
	if n := m.loc.node; n != nil && loading[n.ID] {
		return "loading " + what + "…"
	}
	return "— no related objects —"
}

func (m Model) groupDetailGroups() []overviewGroup {
	n := m.loc.node
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Name", value: displayValue(n.Name)},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Description", value: displayValue(n.Attrs["description"])},
		}},
		{title: "DOMAIN", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["domain_name"])},
			{label: "ID", value: displayValue(n.Attrs["domain_id"])},
		}},
	}
}

// --- projects list --------------------------------------------------------

func projectEntries(projectList []osclient.Project) []entry {
	es := make([]entry, 0, len(projectList))
	for _, p := range projectList {
		name := p.Name
		if name == "" {
			name = shortID(p.ID)
		}
		es = append(es, entry{
			kind: entProject, project: p, label: "project:" + name,
			extra: joinRelatedRowAttrs(p.Description, p.DomainName),
		})
	}
	return es
}

func projectColumnTitles(showIDs bool) []string {
	obj := "NAME"
	if showIDs {
		obj = "PROJECT ID"
	}
	return []string{obj, "DESCRIPTION", "DOMAIN", "ENABLED"}
}

func projectRowCells(e entry, showIDs bool) []string {
	p := e.project
	enabled := "no"
	if p.Enabled {
		enabled = "yes"
	}
	return []string{lbNameCell(p.Name, p.ID, showIDs), displayValue(p.Description), lbNameCell(p.DomainName, p.DomainID, showIDs), enabled}
}

// --- project detail -------------------------------------------------------

func projectToNode(p osclient.Project) *model.Node {
	n := model.NewNode(model.TypeProject, p.ID, p.Name)
	n.SetAttr("description", p.Description)
	n.SetAttr("domain_id", p.DomainID)
	n.SetAttr("domain_name", p.DomainName)
	n.SetAttr("parent_id", p.ParentID)
	n.SetAttr("parent_name", p.ParentName)
	if p.Enabled {
		n.SetAttr("enabled", "true")
	} else {
		n.SetAttr("enabled", "false")
	}
	n.DetailLoaded = true
	n.Raw = map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description, "domain_id": p.DomainID,
		"parent_id": p.ParentID, "enabled": p.Enabled,
	}
	return n
}

func (m Model) isProjectOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeProject
}

func (m Model) projectOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.projectOverviewSummary, "— no related objects —")
}

func (m Model) projectOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "PROJECT DETAILS", m.projectDetailGroups())
}

func (m Model) projectDetailGroups() []overviewGroup {
	n := m.loc.node
	enabled := enabledDisplay(n.Attrs["enabled"])
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Name", value: displayValue(n.Name)},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Description", value: displayValue(n.Attrs["description"])},
		}},
		{title: "DOMAIN", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["domain_name"])},
			{label: "ID", value: displayValue(n.Attrs["domain_id"])},
		}},
		{title: "PARENT", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["parent_name"])},
			{label: "ID", value: displayValue(n.Attrs["parent_id"])},
		}},
		{title: "STATE", fields: []overviewField{
			{label: "Enabled", value: enabled},
		}},
	}
}

// --- roles list -----------------------------------------------------------

func roleEntries(roleList []osclient.Role) []entry {
	es := make([]entry, 0, len(roleList))
	for _, r := range roleList {
		name := r.Name
		if name == "" {
			name = shortID(r.ID)
		}
		es = append(es, entry{
			kind: entRole, role: r, label: "role:" + name,
			extra: joinRelatedRowAttrs(r.Description, r.DomainName),
		})
	}
	return es
}

func roleColumnTitles(showIDs, tokenScoped bool) []string {
	obj := "NAME"
	if showIDs {
		obj = "ROLE ID"
	}
	if tokenScoped {
		return []string{obj, "SOURCE", "SCOPE"}
	}
	return []string{obj, "DESCRIPTION", "DOMAIN"}
}

func roleRowCells(e entry, showIDs bool) []string {
	r := e.role
	if r.TokenScoped {
		return []string{lbNameCell(r.Name, r.ID, showIDs), "active token", tokenRoleScope(r)}
	}
	domain := "—" // global role
	if r.DomainID != "" {
		domain = lbNameCell(r.DomainName, r.DomainID, showIDs)
	}
	return []string{lbNameCell(r.Name, r.ID, showIDs), displayValue(r.Description), domain}
}

func tokenRoleScope(r osclient.Role) string {
	scope := r.ScopeType
	target := r.ScopeName
	if target == "" {
		target = r.ScopeID
	}
	if target != "" {
		scope += ":" + target
	}
	return displayValue(scope)
}

// --- role detail ----------------------------------------------------------

func (m Model) roleNode(id string) *model.Node {
	if r, ok := m.knownRoles[id]; ok {
		return roleToNode(r)
	}
	return nil
}

func roleToNode(r osclient.Role) *model.Node {
	n := model.NewNode(model.TypeRole, r.ID, r.Name)
	n.SetAttr("description", r.Description)
	n.SetAttr("domain_id", r.DomainID)
	n.SetAttr("domain_name", r.DomainName)
	if r.TokenScoped {
		n.SetAttr("token_scoped", "true")
		n.SetAttr("scope_type", r.ScopeType)
		n.SetAttr("scope_name", r.ScopeName)
		n.SetAttr("scope_id", r.ScopeID)
	}
	n.DetailLoaded = true
	raw := map[string]any{
		"id": r.ID, "name": r.Name, "description": r.Description, "domain_id": r.DomainID,
	}
	if r.TokenScoped {
		raw["source"] = "active token"
		raw["scope"] = map[string]string{"type": r.ScopeType, "name": r.ScopeName, "id": r.ScopeID}
	}
	n.Raw = raw
	return n
}

func (m Model) isRoleOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeRole
}

func (m Model) roleOverviewLines(h int) []string {
	if m.loc.node != nil && m.loc.node.Attrs["token_scoped"] == "true" {
		lines := []string{""}
		lines = append(lines, m.roleOverviewSummary(h-1)...)
		for len(lines) < h {
			lines = append(lines, "")
		}
		return lines[:h]
	}
	return m.identityOverviewLines(h, m.roleOverviewSummary, m.identityRelatedEmptyMsg("relations", m.roleRelationsLoading))
}

func (m Model) roleOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "ROLE DETAILS", m.roleDetailGroups())
}

// roleRelatedEntries builds a role's related objects: the roles it implies and
// the assignments granting it. Section order/headings come from
// expectedRelatedSections.
func (m Model) roleRelatedEntries(n *model.Node) []entry {
	if n == nil || n.Attrs["token_scoped"] == "true" || !m.roleRelationsLoaded[n.ID] {
		return nil
	}
	r := m.roleRelations[n.ID]
	out := roleEntries(r.implied)
	out = append(out, assignmentEntries(r.assignments, pivotRole)...)
	return out
}

// assignmentEntries renders each assignment relative to a pivot — the dimension
// the current detail already names, which the row therefore elides. The
// remaining two dimensions form the label and the dimmed trailing fact:
//
//	pivotRole   (role detail)          "user:alice"   "on project:alpha"
//	pivotActor  (user/group detail)    "role:admin"   "on project:alpha"
//	pivotTarget (project/domain detail) "user:alice"   "as role:admin"
//
// The type prefix is kept so mixed rows read clearly (renderIdentityRow leaves
// entAssignment labels unstripped).
func assignmentEntries(as []osclient.RoleAssignment, pivot assignmentPivot) []entry {
	es := make([]entry, 0, len(as))
	for _, a := range as {
		actor := a.ActorType + ":" + orShort(a.ActorName, a.ActorID)
		role := "role:" + orShort(a.RoleName, a.RoleID)
		e := entry{kind: entAssignment, assignment: a, assignmentPivot: pivot}
		switch pivot {
		case pivotActor:
			e.label, e.extra = role, "on "+assignmentTarget(a)
		case pivotTarget:
			e.label, e.extra = actor, "as "+role
		default: // pivotRole
			e.label, e.extra = actor, "on "+assignmentTarget(a)
		}
		es = append(es, e)
	}
	return es
}

// assignmentProjectEntries lists the distinct projects an actor holds any role
// on, derived from its assignments, so they appear as navigable related objects
// alongside the detailed ROLE ASSIGNMENTS rows. Rows are bare references (id +
// name); opening one resolves through the known-project cache.
func assignmentProjectEntries(as []osclient.RoleAssignment) []entry {
	var es []entry
	seen := map[string]bool{}
	for _, a := range as {
		if a.TargetType != "project" || a.TargetID == "" || seen[a.TargetID] {
			continue
		}
		seen[a.TargetID] = true
		es = append(es, projectEntries([]osclient.Project{{ID: a.TargetID, Name: a.TargetName}})...)
	}
	return es
}

func assignmentTarget(a osclient.RoleAssignment) string {
	switch a.TargetType {
	case "project":
		return "project:" + orShort(a.TargetName, a.TargetID)
	case "domain":
		return "domain:" + orShort(a.TargetName, a.TargetID)
	default:
		return "system (cluster-wide)"
	}
}

func orShort(name, id string) string {
	if name != "" {
		return name
	}
	return shortID(id)
}

func (m Model) roleDetailGroups() []overviewGroup {
	n := m.loc.node
	scope := "global"
	fields := []overviewField{
		{label: "Name", value: displayValue(n.Name)},
		{label: "ID", value: displayValue(n.ID)},
	}
	if n.Attrs["token_scoped"] == "true" {
		fields = append(fields, overviewField{label: "Source", value: "active token"})
	} else {
		fields = append(fields, overviewField{label: "Description", value: displayValue(n.Attrs["description"])})
	}
	groups := []overviewGroup{{title: "IDENTITY", fields: fields}}
	if n.Attrs["token_scoped"] == "true" {
		groups = append(groups, overviewGroup{title: "TOKEN SCOPE", fields: []overviewField{
			{label: "Type", value: displayValue(n.Attrs["scope_type"])},
			{label: "Name", value: displayValue(n.Attrs["scope_name"])},
			{label: "ID", value: displayValue(n.Attrs["scope_id"])},
		}})
	} else if n.Attrs["domain_id"] != "" {
		scope = "domain"
		groups = append(groups, overviewGroup{title: "DOMAIN", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["domain_name"])},
			{label: "ID", value: displayValue(n.Attrs["domain_id"])},
		}})
	}
	if n.Attrs["token_scoped"] != "true" {
		groups[0].fields = append(groups[0].fields, overviewField{label: "Scope", value: scope})
	}
	return groups
}

// --- domains list ---------------------------------------------------------

func domainEntries(domainList []osclient.Domain) []entry {
	es := make([]entry, 0, len(domainList))
	for _, d := range domainList {
		name := d.Name
		if name == "" {
			name = shortID(d.ID)
		}
		es = append(es, entry{
			kind: entDomain, domain: d, label: "domain:" + name,
			extra: joinRelatedRowAttrs(d.Description, d.ID),
		})
	}
	return es
}

// joinRelatedRowAttrs keeps free-form values (especially names and
// descriptions containing spaces) visually distinct in related-object rows.
func joinRelatedRowAttrs(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " · ")
}

func domainColumnTitles(showIDs bool) []string {
	obj := "NAME"
	if showIDs {
		obj = "DOMAIN ID"
	}
	return []string{obj, "DESCRIPTION", "ENABLED"}
}

func domainRowCells(e entry, showIDs bool) []string {
	d := e.domain
	enabled := "—"
	if d.Enabled {
		enabled = "yes"
	} else if !d.Partial {
		enabled = "no"
	}
	return []string{lbNameCell(d.Name, d.ID, showIDs), displayValue(d.Description), enabled}
}

// --- domain detail --------------------------------------------------------

func domainToNode(d osclient.Domain) *model.Node {
	n := model.NewNode(model.TypeDomain, d.ID, d.Name)
	n.SetAttr("description", d.Description)
	if d.Partial {
		// The token identifies the user's domain but does not carry its state.
	} else if d.Enabled {
		n.SetAttr("enabled", "true")
	} else {
		n.SetAttr("enabled", "false")
	}
	n.DetailLoaded = true
	raw := map[string]any{
		"id": d.ID, "name": d.Name, "description": d.Description,
	}
	if !d.Partial {
		raw["enabled"] = d.Enabled
	}
	n.Raw = raw
	return n
}

func (m Model) isDomainOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeDomain
}

func (m Model) domainOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.domainOverviewSummary, m.identityRelatedEmptyMsg("contents", m.domainContentsLoading))
}

func (m Model) domainOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "DOMAIN DETAILS", m.domainDetailGroups())
}

func (m Model) domainDetailGroups() []overviewGroup {
	n := m.loc.node
	enabled := enabledDisplay(n.Attrs["enabled"])
	return []overviewGroup{
		{title: "IDENTITY", fields: []overviewField{
			{label: "Name", value: displayValue(n.Name)},
			{label: "ID", value: displayValue(n.ID)},
			{label: "Description", value: displayValue(n.Attrs["description"])},
		}},
		{title: "STATE", fields: []overviewField{
			{label: "Enabled", value: enabled},
		}},
	}
}

// enabledDisplay renders the enabled attribute: yes / no, or "—" when unknown
// (a bare domain reference whose full state has not been loaded).
func enabledDisplay(attr string) string {
	switch {
	case attr == "":
		return "—"
	case strings.EqualFold(attr, "true"):
		return "yes"
	default:
		return "no"
	}
}

// --- user detail ----------------------------------------------------------
//
// Keystone identity objects are not part of any load-balancer tree, so detail
// views synthesize a node from the known-object caches (see userNode etc. above)
// rather than resolving against a status-show tree.

func userToNode(u osclient.User) *model.Node {
	n := model.NewNode(model.TypeUser, u.ID, u.Name)
	n.SetAttr("domain_id", u.DomainID)
	if u.Partial {
		// A token-only fallback does not carry account state.
	} else if u.Enabled {
		n.SetAttr("enabled", "true")
	} else {
		n.SetAttr("enabled", "false")
	}
	n.SetAttr("domain_name", u.DomainName)
	n.SetAttr("email", u.Email)
	n.SetAttr("description", u.Description)
	n.SetAttr("default_project_id", u.DefaultProjectID)
	n.SetAttr("default_project_name", u.DefaultProjectName)
	if u.Service {
		n.SetAttr("service", "true")
	}
	n.DetailLoaded = true
	raw := map[string]any{
		"id": u.ID, "name": u.Name, "domain_id": u.DomainID,
		"email": u.Email, "description": u.Description, "default_project_id": u.DefaultProjectID,
	}
	if !u.Partial {
		raw["enabled"] = u.Enabled
	}
	n.Raw = raw
	return n
}

func (m Model) isUserOverview() bool {
	return m.loc.node != nil && m.loc.node.Type == model.TypeUser
}

func (m Model) userOverviewLines(h int) []string {
	return m.identityOverviewLines(h, m.userOverviewSummary, m.identityRelatedEmptyMsg("groups", m.userGroupsLoading))
}

func (m Model) userOverviewSummary(budget int) []string {
	return m.identityDetailSummary(budget, "USER DETAILS", m.userDetailGroups())
}

// userDetailGroups partitions the user fields into labelled sections. Domain and
// default-project names are resolved through the shared, cached name maps and
// render as "—" when the credential can't enumerate them.
func (m Model) userDetailGroups() []overviewGroup {
	n := m.loc.node
	enabled := enabledDisplay(n.Attrs["enabled"])
	identity := []overviewField{
		{label: "Name", value: displayValue(n.Name)},
		{label: "ID", value: displayValue(n.ID)},
		{label: "Description", value: displayValue(n.Attrs["description"])},
		{label: "Email", value: displayValue(n.Attrs["email"])},
	}
	if strings.EqualFold(n.Attrs["service"], "true") {
		identity = append(identity, overviewField{label: "Type", value: "service account"})
	}
	return []overviewGroup{
		{title: "IDENTITY", fields: identity},
		{title: "DOMAIN", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["domain_name"])},
			{label: "ID", value: displayValue(n.Attrs["domain_id"])},
		}},
		{title: "DEFAULT PROJECT", fields: []overviewField{
			{label: "Name", value: displayValue(n.Attrs["default_project_name"])},
			{label: "ID", value: displayValue(n.Attrs["default_project_id"])},
		}},
		{title: "STATE", fields: []overviewField{
			{label: "Enabled", value: enabled},
		}},
	}
}
