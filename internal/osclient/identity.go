package osclient

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/roles"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/users"
)

// User is a Keystone user summary for the identity area's users list view.
type User struct {
	ID                 string
	Name               string
	DomainID           string
	DomainName         string // resolved from the shared domain-name map (best effort)
	Enabled            bool
	Email              string
	Description        string
	DefaultProjectID   string
	DefaultProjectName string // resolved from the shared project-name map (best effort)
	Service            bool   // heuristically a service/system account (see resolveUsers)
	Partial            bool   // true when only token identity was available
}

// IdentityList carries a collection plus a human-readable restriction when
// Keystone denied the full collection and a self-service fallback was used.
// Restriction is empty for a complete list.
type IdentityList[T any] struct {
	Items       []T
	Restriction string
}

// ListUsers lists Keystone users visible to the active token. A domain token
// applies its domain explicitly. If collection access is denied, it falls back
// to the authenticated user's self-readable record (or the partial identity
// embedded in the token).
func (c *Clients) ListUsers(ctx context.Context) (IdentityList[User], error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	identity := sc.identity
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()

	opts := users.ListOpts{}
	if scope.Kind == ScopeDomain {
		opts.DomainID = scope.ID
	}
	pages, err := users.List(identity, opts).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			items, fallbackErr := c.currentUser(ctx, identity)
			if fallbackErr != nil {
				return IdentityList[User]{}, fallbackErr
			}
			return IdentityList[User]{Items: items, Restriction: "current user only"}, nil
		}
		return IdentityList[User]{}, err
	}
	items, err := users.ExtractUsers(pages)
	if err != nil {
		return IdentityList[User]{}, err
	}
	return IdentityList[User]{Items: c.resolveUsers(ctx, items)}, nil
}

// currentUser reads the authenticated user's own record. Keystone's default
// policy permits this even when the user collection is forbidden. If an
// operator policy also denies the self endpoint, retain the useful identity
// embedded in the token and mark the record partial.
func (c *Clients) currentUser(ctx context.Context, identity *gophercloud.ServiceClient) ([]User, error) {
	token := c.CurrentToken()
	if !token.Available || token.UserID == "" {
		return nil, ErrAdminRequired
	}
	item, err := users.Get(ctx, identity, token.UserID).Extract()
	if err == nil && item != nil {
		domainName := ""
		if item.DomainID == token.UserDomainID {
			domainName = token.UserDomainName
		}
		defaultProjectName := ""
		if token.ScopeType == "project" && item.DefaultProjectID == token.ScopeID {
			defaultProjectName = token.ScopeName
		}
		return []User{{
			ID: item.ID, Name: item.Name, DomainID: item.DomainID, DomainName: domainName,
			Enabled: item.Enabled, Email: userEmail(*item), Description: item.Description,
			DefaultProjectID: item.DefaultProjectID, DefaultProjectName: defaultProjectName,
			Service: isServiceName(item.Name),
		}}, nil
	}
	return []User{{
		ID: token.UserID, Name: token.UserName, DomainID: token.UserDomainID,
		DomainName: token.UserDomainName, Partial: true,
	}}, nil
}

// ListGroupMembers lists the users that belong to a group — its most useful
// related objects, since a group's whole purpose is to carry role assignments
// that its members inherit. A 403 degrades to ErrAdminRequired.
func (c *Clients) ListGroupMembers(ctx context.Context, groupID string) ([]User, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	identity := sc.identity
	c.mu.Unlock()

	pages, err := users.ListInGroup(identity, groupID, users.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := users.ExtractUsers(pages)
	if err != nil {
		return nil, err
	}
	return c.resolveUsers(ctx, items), nil
}

// resolveUsers maps gophercloud users to our summary type, resolving default-
// project and domain IDs to names through the shared, cached name maps — the
// same mechanism that labels cross-project load-balancer rows. Each map is looked
// up only when at least one user references it; best effort, so an unresolvable
// ID simply leaves the name empty. Results are sorted by name.
func (c *Clients) resolveUsers(ctx context.Context, items []users.User) []User {
	var projNames, domNames map[string]string
	needProj, needDom := false, false
	for _, u := range items {
		needProj = needProj || u.DefaultProjectID != ""
		needDom = needDom || u.DomainID != ""
	}
	if needProj {
		projNames = c.projectNameMap(ctx)
	}
	if needDom {
		domNames = c.domainNameMap(ctx)
	}
	// A user is treated as a service/system account if it holds a role on the
	// service project (the universal convention) or its name is a well-known
	// OpenStack service. The former catches deployment-specific accounts a name
	// list can't; the latter still flags the obvious ones when the service project
	// can't be enumerated (e.g. a non-admin scope). Both are best-effort.
	serviceIDs := c.serviceAccountIDs(ctx)
	out := make([]User, 0, len(items))
	for _, u := range items {
		out = append(out, User{
			ID: u.ID, Name: u.Name, DomainID: u.DomainID, DomainName: domNames[u.DomainID],
			Enabled: u.Enabled, Email: userEmail(u), Description: u.Description,
			DefaultProjectID: u.DefaultProjectID, DefaultProjectName: projNames[u.DefaultProjectID],
			Service: serviceIDs[u.ID] || isServiceName(u.Name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// serviceAccountNames are the conventional usernames OpenStack services register
// in Keystone. Matching one flags a user as a service account even when the
// service project can't be enumerated to confirm it.
var serviceAccountNames = map[string]bool{
	"nova": true, "glance": true, "cinder": true, "cinderv2": true, "cinderv3": true,
	"neutron": true, "keystone": true, "swift": true, "heat": true, "heat_domain_admin": true,
	"placement": true, "octavia": true, "barbican": true, "designate": true, "ironic": true,
	"ironic-inspector": true, "magnum": true, "manila": true, "manilav2": true, "gnocchi": true,
	"aodh": true, "ceilometer": true, "panko": true, "trove": true, "zaqar": true, "sahara": true,
	"senlin": true, "watcher": true, "masakari": true, "mistral": true, "cloudkitty": true,
	"tacker": true, "blazar": true, "zun": true, "cyborg": true, "vitrage": true, "ec2api": true,
}

// isServiceName reports whether a username matches a well-known OpenStack service.
func isServiceName(name string) bool {
	return serviceAccountNames[strings.ToLower(strings.TrimSpace(name))]
}

// isServiceProjectName reports whether a project name is the conventional service
// project that holds service accounts.
func isServiceProjectName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "service", "services":
		return true
	}
	return false
}

// serviceAccountIDs returns the set of user IDs holding a role on the service
// project — the deployment-agnostic marker of a service account. It finds the
// service project through the shared project-name map, then lists that project's
// role assignments; results are cached for projNamesTTL. Every step degrades to
// the cached/empty set (leaving name-based detection as the fallback) when the
// credential can't enumerate the service project or its assignments.
func (c *Clients) serviceAccountIDs(ctx context.Context) map[string]bool {
	c.mu.Lock()
	if c.serviceUserIDs != nil && time.Since(c.serviceUserIDsAt) < projNamesTTL {
		cached := c.serviceUserIDs
		c.mu.Unlock()
		return cached
	}
	cached := c.serviceUserIDs
	c.mu.Unlock()

	var serviceProjectIDs []string
	for id, name := range c.projectNameMap(ctx) {
		if isServiceProjectName(name) {
			serviceProjectIDs = append(serviceProjectIDs, id)
		}
	}
	if len(serviceProjectIDs) == 0 {
		return cached // service project not visible; fall back to name heuristic
	}
	ids := map[string]bool{}
	for _, pid := range serviceProjectIDs {
		as, err := c.listAssignments(ctx, roles.ListAssignmentsOpts{ScopeProjectID: pid})
		if err != nil {
			continue // best effort — a denial on one project shouldn't drop the rest
		}
		for _, a := range as {
			if a.ActorType == "user" && a.ActorID != "" {
				ids[a.ActorID] = true
			}
		}
	}
	c.mu.Lock()
	c.serviceUserIDs = ids
	c.serviceUserIDsAt = time.Now()
	c.mu.Unlock()
	return ids
}

// domainNameMap returns a best-effort domain ID→name map, cached for the same
// TTL as the project-name map. The active token supplies the authenticated
// user's domain and, independently, the owning domain of a project scope. This
// lets project-scoped users see names for those domains even when the domain
// collection itself is forbidden.
func (c *Clients) domainNameMap(ctx context.Context) map[string]string {
	tokenNames := tokenDomainNameMap(c.CurrentToken())

	c.mu.Lock()
	if c.domainNames != nil && time.Since(c.domainNamesAt) < projNamesTTL {
		cached := mergeDomainNames(c.domainNames, tokenNames)
		c.mu.Unlock()
		return cached
	}
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	cached := mergeDomainNames(c.domainNames, tokenNames)
	c.mu.Unlock()

	pages, err := domains.List(client, domains.ListOpts{}).AllPages(ctx)
	if err != nil {
		return cached
	}
	ds, err := domains.ExtractDomains(pages)
	if err != nil {
		return cached
	}
	names := make(map[string]string, len(ds)+len(tokenNames))
	for id, name := range tokenNames {
		names[id] = name
	}
	for _, d := range ds {
		if d.Name != "" {
			names[d.ID] = d.Name
		}
	}
	if len(names) > 0 {
		c.mu.Lock()
		c.domainNames = names
		c.domainNamesAt = time.Now()
		c.mu.Unlock()
	}
	return names
}

func tokenDomainNameMap(token TokenInfo) map[string]string {
	names := make(map[string]string, 2)
	if token.UserDomainID != "" && token.UserDomainName != "" {
		names[token.UserDomainID] = token.UserDomainName
	}
	if token.ScopeType == "project" && token.ScopeDomainID != "" && token.ScopeDomainName != "" {
		names[token.ScopeDomainID] = token.ScopeDomainName
	}
	if token.ScopeType == "domain" && token.ScopeID != "" && token.ScopeName != "" {
		names[token.ScopeID] = token.ScopeName
	}
	return names
}

func mergeDomainNames(cached, tokenNames map[string]string) map[string]string {
	names := make(map[string]string, len(cached)+len(tokenNames))
	for id, name := range cached {
		names[id] = name
	}
	for id, name := range tokenNames {
		names[id] = name
	}
	return names
}

// Group is a Keystone group summary for the identity area's groups list view.
type Group struct {
	ID          string
	Name        string
	DomainID    string
	DomainName  string // resolved from the shared domain-name map (best effort)
	Description string
}

// ListGroups lists Keystone groups visible to the active credential, resolving
// each group's domain name through the shared, cached domain-name map. If the
// full collection is forbidden, it falls back to the groups containing the
// authenticated user, which Keystone's default owner policy permits.
func (c *Clients) ListGroups(ctx context.Context) (IdentityList[Group], error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()

	opts := groups.ListOpts{}
	if scope.Kind == ScopeDomain {
		opts.DomainID = scope.ID
	}
	pages, err := groups.List(client, opts).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			token := c.CurrentToken()
			if !token.Available || token.UserID == "" {
				return IdentityList[Group]{}, ErrAdminRequired
			}
			items, fallbackErr := c.listCurrentUserGroups(ctx, client, token)
			if fallbackErr != nil {
				return IdentityList[Group]{}, fallbackErr
			}
			return IdentityList[Group]{Items: items, Restriction: "current user's groups"}, nil
		}
		return IdentityList[Group]{}, err
	}
	items, err := groups.ExtractGroups(pages)
	if err != nil {
		return IdentityList[Group]{}, err
	}
	return IdentityList[Group]{Items: c.resolveGroups(ctx, items)}, nil
}

// listCurrentUserGroups uses Keystone's owner-readable membership endpoint
// without attempting privileged domain enumeration merely to decorate names.
func (c *Clients) listCurrentUserGroups(ctx context.Context, client *gophercloud.ServiceClient, token TokenInfo) ([]Group, error) {
	pages, err := users.ListGroups(client, token.UserID).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := groups.ExtractGroups(pages)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(items))
	for _, g := range items {
		domainName := ""
		if g.DomainID == token.UserDomainID {
			domainName = token.UserDomainName
		}
		out = append(out, Group{
			ID: g.ID, Name: g.Name, DomainID: g.DomainID,
			DomainName: domainName, Description: g.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListUserGroups lists the groups a user belongs to — the inverse of a group's
// member list, and a user's most useful related objects (a user inherits every
// role assigned to its groups). A 403 degrades to ErrAdminRequired.
func (c *Clients) ListUserGroups(ctx context.Context, userID string) ([]Group, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	pages, err := users.ListGroups(client, userID).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := groups.ExtractGroups(pages)
	if err != nil {
		return nil, err
	}
	return c.resolveGroups(ctx, items), nil
}

// resolveGroups maps gophercloud groups to our summary type, resolving each
// group's domain name through the shared, cached domain-name map (best effort).
// Results are sorted by name.
func (c *Clients) resolveGroups(ctx context.Context, items []groups.Group) []Group {
	var domNames map[string]string
	for _, g := range items {
		if g.DomainID != "" {
			domNames = c.domainNameMap(ctx)
			break
		}
	}
	out := make([]Group, 0, len(items))
	for _, g := range items {
		out = append(out, Group{
			ID: g.ID, Name: g.Name, DomainID: g.DomainID,
			DomainName: domNames[g.DomainID], Description: g.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Project is a Keystone project summary for the identity area's projects list
// view. This is the browsable/inspectable list; it is distinct from ScopeInfo,
// which drives authentication-scope switching.
type Project struct {
	ID          string
	Name        string
	Description string
	DomainID    string
	DomainName  string // resolved from the shared domain-name map (best effort)
	ParentID    string
	ParentName  string // resolved from the shared project-name map (best effort)
	Enabled     bool
}

// ListProjectsDetailed lists projects visible in the active token scope. System
// scope uses the normal collection, domain scope restricts that collection to
// the active domain, and project scope uses the user's available-project list.
func (c *Clients) ListProjectsDetailed(ctx context.Context) ([]Project, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()

	pager := projects.ListAvailable(client)
	switch scope.Kind {
	case ScopeSystem:
		pager = projects.List(client, projects.ListOpts{})
	case ScopeDomain:
		pager = projects.List(client, projects.ListOpts{DomainID: scope.ID})
	}
	pages, err := pager.AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	ps, err := projects.ExtractProjects(pages)
	if err != nil {
		return nil, err
	}
	return c.resolveProjects(ctx, ps), nil
}

// ListProjectsInDomain lists the projects belonging to a domain — the domain's
// related projects. If the domain-filtered collection is forbidden, it falls
// back to the projects available to the active token and filters them locally.
func (c *Clients) ListProjectsInDomain(ctx context.Context, domainID string) ([]Project, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	pages, err := projects.List(client, projects.ListOpts{DomainID: domainID}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			available, fallbackErr := c.ListProjectsDetailed(ctx)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			filtered := make([]Project, 0, len(available))
			for _, project := range available {
				if project.DomainID == domainID {
					filtered = append(filtered, project)
				}
			}
			return filtered, nil
		}
		return nil, err
	}
	ps, err := projects.ExtractProjects(pages)
	if err != nil {
		return nil, err
	}
	return c.resolveProjects(ctx, ps), nil
}

// resolveProjects maps gophercloud projects to our summary type, excluding domain
// entries (is_domain) and resolving domain and parent names through the shared,
// cached name maps. Results are sorted by name.
func (c *Clients) resolveProjects(ctx context.Context, ps []projects.Project) []Project {
	var domNames, projNames map[string]string
	for _, p := range ps {
		if p.IsDomain {
			continue
		}
		if p.DomainID != "" {
			domNames = c.domainNameMap(ctx)
		}
		if p.ParentID != "" {
			projNames = c.projectNameMap(ctx)
		}
		if domNames != nil && projNames != nil {
			break
		}
	}
	out := make([]Project, 0, len(ps))
	for _, p := range ps {
		if p.IsDomain {
			continue
		}
		out = append(out, Project{
			ID: p.ID, Name: p.Name, Description: p.Description,
			DomainID: p.DomainID, DomainName: domNames[p.DomainID],
			ParentID: p.ParentID, ParentName: projNames[p.ParentID], Enabled: p.Enabled,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListUsersInDomain lists the users belonging to a domain — the domain's related
// users. A 403 degrades to ErrAdminRequired.
func (c *Clients) ListUsersInDomain(ctx context.Context, domainID string) ([]User, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	identity := sc.identity
	c.mu.Unlock()

	pages, err := users.List(identity, users.ListOpts{DomainID: domainID}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := users.ExtractUsers(pages)
	if err != nil {
		return nil, err
	}
	return c.resolveUsers(ctx, items), nil
}

// ListGroupsInDomain lists the groups belonging to a domain — the domain's
// related groups. A 403 degrades to ErrAdminRequired.
func (c *Clients) ListGroupsInDomain(ctx context.Context, domainID string) ([]Group, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	identity := sc.identity
	c.mu.Unlock()

	pages, err := groups.List(identity, groups.ListOpts{DomainID: domainID}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := groups.ExtractGroups(pages)
	if err != nil {
		return nil, err
	}
	return c.resolveGroups(ctx, items), nil
}

// Role is a Keystone role summary for the identity area's roles list view. A
// role is global unless DomainID is set (a domain-scoped role).
type Role struct {
	ID          string
	Name        string
	Description string
	DomainID    string
	DomainName  string // resolved from the shared domain-name map (best effort)
	TokenScoped bool   // true when sourced from the active token, not the role catalog
	ScopeType   string
	ScopeName   string
	ScopeID     string
}

// ListRoles lists Keystone roles visible to the active credential, resolving the
// domain name for any domain-scoped roles. If the catalog is forbidden, it
// returns the effective roles embedded in the active token and marks their scope
// explicitly.
func (c *Clients) ListRoles(ctx context.Context) (IdentityList[Role], error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()

	opts := roles.ListOpts{}
	if scope.Kind == ScopeDomain {
		opts.DomainID = scope.ID
	}
	pages, err := roles.List(client, opts).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			token := c.CurrentToken()
			if !token.Available {
				return IdentityList[Role]{}, ErrAdminRequired
			}
			items := make([]Role, 0, len(token.RoleDetails))
			for _, r := range token.RoleDetails {
				items = append(items, Role{
					ID: r.ID, Name: r.Name, TokenScoped: true,
					ScopeType: token.ScopeType, ScopeName: token.ScopeName, ScopeID: token.ScopeID,
				})
			}
			return IdentityList[Role]{Items: items, Restriction: "roles in active token"}, nil
		}
		return IdentityList[Role]{}, err
	}
	items, err := roles.ExtractRoles(pages)
	if err != nil {
		return IdentityList[Role]{}, err
	}
	var domNames map[string]string
	for _, r := range items {
		if r.DomainID != "" {
			domNames = c.domainNameMap(ctx)
			break
		}
	}
	out := make([]Role, 0, len(items))
	for _, r := range items {
		out = append(out, Role{
			ID: r.ID, Name: r.Name, Description: r.Description,
			DomainID: r.DomainID, DomainName: domNames[r.DomainID],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return IdentityList[Role]{Items: out}, nil
}

// RoleAssignment is one grant of a role: an actor (user or group) holding a role
// on a target (project, domain, or cluster-wide "system"). It carries all three
// dimensions so it can be read from any side — "who has this role, and where"
// (role view), "what roles does this user have" (actor view), or "who has a role
// here" (target view).
type RoleAssignment struct {
	RoleID      string
	RoleName    string
	ActorType   string // "user" or "group"
	ActorID     string
	ActorName   string
	TargetType  string // "project", "domain", or "system"
	TargetID    string
	TargetName  string
	Inherited   bool // effective grant not held directly (via a group or a parent/domain scope)
	TokenScoped bool // effective in the active token; direct/inherited origin is unavailable
}

// ListRoleAssignments lists the assignments of a single role — who holds it and
// where.
func (c *Clients) ListRoleAssignments(ctx context.Context, roleID string) ([]RoleAssignment, error) {
	return c.listAssignments(ctx, roles.ListAssignmentsOpts{RoleID: roleID})
}

// ListUserAssignments lists the role assignments a user holds — the mirror of
// the role→assignments view. Effective is requested so roles inherited through
// group membership (or a domain/parent OS-INHERIT grant) are included ("what this
// user can actually do"). Each grant is then classified direct vs inherited by
// differencing against the user's direct (non-effective) grants: gophercloud's
// assignment result carries no origin/links, so this two-query diff is the only
// way to tell them apart. If the direct query fails the effective list is
// returned unclassified (all treated as direct) rather than failing the view.
func (c *Clients) ListUserAssignments(ctx context.Context, userID string) ([]RoleAssignment, error) {
	effective := true
	eff, err := c.listAssignments(ctx, roles.ListAssignmentsOpts{UserID: userID, Effective: &effective})
	if err != nil {
		return nil, err
	}
	direct, derr := c.listAssignments(ctx, roles.ListAssignmentsOpts{UserID: userID})
	if derr != nil {
		return eff, nil // can't classify; leave every grant marked direct
	}
	grantKey := func(a RoleAssignment) string { return a.RoleID + "|" + a.TargetType + "|" + a.TargetID }
	isDirect := make(map[string]bool, len(direct))
	for _, a := range direct {
		isDirect[grantKey(a)] = true
	}
	for i := range eff {
		eff[i].Inherited = !isDirect[grantKey(eff[i])]
	}
	return eff, nil
}

// ListGroupAssignments lists the role assignments a group holds — the roles
// every member of the group inherits. Groups are not members of anything, so
// these are direct (no Effective).
func (c *Clients) ListGroupAssignments(ctx context.Context, groupID string) ([]RoleAssignment, error) {
	return c.listAssignments(ctx, roles.ListAssignmentsOpts{GroupID: groupID})
}

// ListProjectAssignments lists the role assignments scoped to a project — who
// (user or group) holds a role there. Direct grants, so a group grant shows as
// the group rather than being expanded per-member.
func (c *Clients) ListProjectAssignments(ctx context.Context, projectID string) ([]RoleAssignment, error) {
	return c.listAssignments(ctx, roles.ListAssignmentsOpts{ScopeProjectID: projectID})
}

// ListDomainAssignments lists the role assignments scoped to a domain itself
// (not to its projects) — who holds a role on the domain.
func (c *Clients) ListDomainAssignments(ctx context.Context, domainID string) ([]RoleAssignment, error) {
	return c.listAssignments(ctx, roles.ListAssignmentsOpts{ScopeDomainID: domainID})
}

// listAssignments fetches the assignments matching opts and maps them to our
// summary type. It requests include_names so roles, actors, and targets carry
// names where the cloud supports it, and resolves target project/domain names
// through the shared caches otherwise. A 403 degrades to ErrAdminRequired.
// Results are sorted by role, then target, then actor, so any owning-side view
// reads consistently.
func (c *Clients) listAssignments(ctx context.Context, opts roles.ListAssignmentsOpts) ([]RoleAssignment, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	withNames := true
	opts.IncludeNames = &withNames
	pages, err := roles.ListAssignments(client, opts).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, err
	}
	items, err := roles.ExtractRoleAssignments(pages)
	if err != nil {
		return nil, err
	}

	var projNames, domNames map[string]string
	for _, a := range items {
		if a.Scope.Project.ID != "" && a.Scope.Project.Name == "" {
			projNames = c.projectNameMap(ctx)
		}
		if a.Scope.Domain.ID != "" && a.Scope.Domain.Name == "" {
			domNames = c.domainNameMap(ctx)
		}
	}

	// An effective query (used for a user) returns one entry per grant path, so the
	// same role on the same target can arrive several times — once directly and
	// once per group or inheritance chain that also grants it. Those paths collapse
	// to the same summary here, so dedupe by (role, actor, target); a plain query
	// can't produce duplicates, making this harmless there.
	type dedupKey struct{ role, actorType, actorID, targetType, targetID string }
	seen := make(map[dedupKey]struct{}, len(items))
	out := make([]RoleAssignment, 0, len(items))
	for _, a := range items {
		ra := RoleAssignment{RoleID: a.Role.ID, RoleName: a.Role.Name}
		switch {
		case a.User.ID != "":
			ra.ActorType, ra.ActorID, ra.ActorName = "user", a.User.ID, a.User.Name
		case a.Group.ID != "":
			ra.ActorType, ra.ActorID, ra.ActorName = "group", a.Group.ID, a.Group.Name
		}
		switch {
		case a.Scope.Project.ID != "":
			ra.TargetType, ra.TargetID = "project", a.Scope.Project.ID
			ra.TargetName = firstNonEmpty(a.Scope.Project.Name, projNames[a.Scope.Project.ID])
		case a.Scope.Domain.ID != "":
			ra.TargetType, ra.TargetID = "domain", a.Scope.Domain.ID
			ra.TargetName = firstNonEmpty(a.Scope.Domain.Name, domNames[a.Scope.Domain.ID])
		default:
			ra.TargetType = "system"
		}
		if ra.ActorID == "" {
			continue // malformed assignment
		}
		key := dedupKey{ra.RoleID, ra.ActorType, ra.ActorID, ra.TargetType, ra.TargetID}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ra)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RoleName != out[j].RoleName {
			return out[i].RoleName < out[j].RoleName
		}
		if out[i].TargetType != out[j].TargetType {
			return out[i].TargetType < out[j].TargetType
		}
		if out[i].TargetName != out[j].TargetName {
			return out[i].TargetName < out[j].TargetName
		}
		return out[i].ActorName < out[j].ActorName
	})
	return out, nil
}

// ListImpliedRoles returns the roles a given role implies, from the cloud's role
// inference rules. A 403 degrades to ErrAdminRequired.
func (c *Clients) ListImpliedRoles(ctx context.Context, roleID string) ([]Role, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	c.mu.Unlock()

	res := roles.ListRoleInferenceRules(ctx, client)
	if res.Err != nil {
		if gophercloud.ResponseCodeIs(res.Err, 403) {
			return nil, ErrAdminRequired
		}
		return nil, res.Err
	}
	list, err := res.Extract()
	if err != nil {
		return nil, err
	}
	var out []Role
	for _, rule := range list.RoleInferenceRuleList {
		if rule.PriorRole.ID != roleID {
			continue
		}
		for _, ir := range rule.ImpliedRoles {
			out = append(out, Role{ID: ir.ID, Name: ir.Name, Description: ir.Description})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Domain is a Keystone domain summary for the identity area's domains list view.
type Domain struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	Partial     bool // true when only token identity was available
}

// ListDomains lists Keystone domains visible to the active credential. If the
// collection is forbidden, it falls back to the authenticated user's domain
// identity embedded in the active token.
func (c *Clients) ListDomains(ctx context.Context) (IdentityList[Domain], error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	client := sc.identity
	scope := c.effectiveScopeLocked()
	c.mu.Unlock()

	// A domain-scoped token represents exactly one identity boundary. Fetch that
	// object directly rather than depending on a deployment to filter a
	// collection response consistently.
	if scope.Kind == ScopeDomain {
		domain, err := domains.Get(ctx, client, scope.ID).Extract()
		if err == nil && domain != nil {
			name := firstNonEmpty(domain.Name, scope.Name)
			return IdentityList[Domain]{Items: []Domain{{
				ID: domain.ID, Name: name, Description: domain.Description,
				Enabled: domain.Enabled,
			}}}, nil
		}
		if err != nil && !gophercloud.ResponseCodeIs(err, 403) {
			return IdentityList[Domain]{}, err
		}
		return IdentityList[Domain]{Items: []Domain{{
			ID: scope.ID, Name: scope.Name, Partial: true,
		}}, Restriction: "active domain"}, nil
	}

	pages, err := domains.List(client, domains.ListOpts{}).AllPages(ctx)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 403) {
			token := c.CurrentToken()
			names := tokenDomainNameMap(token)
			partial := make(map[string]Domain)
			if token.UserDomainID != "" {
				partial[token.UserDomainID] = Domain{
					ID: token.UserDomainID, Name: token.UserDomainName, Partial: true,
				}
			}
			// Project-access discovery is self-service and reveals every owning
			// domain relevant to this user, including domains other than the
			// user's identity domain.
			if availablePages, availableErr := projects.ListAvailable(client).AllPages(ctx); availableErr == nil {
				if available, extractErr := projects.ExtractProjects(availablePages); extractErr == nil {
					for _, project := range available {
						if project.DomainID == "" {
							continue
						}
						partial[project.DomainID] = Domain{
							ID: project.DomainID, Name: names[project.DomainID], Partial: true,
						}
					}
				}
			}
			if len(partial) == 0 {
				return IdentityList[Domain]{}, ErrAdminRequired
			}
			items := make([]Domain, 0, len(partial))
			for _, domain := range partial {
				items = append(items, domain)
			}
			sort.Slice(items, func(i, j int) bool {
				li := firstNonEmpty(items[i].Name, items[i].ID)
				lj := firstNonEmpty(items[j].Name, items[j].ID)
				return li < lj
			})
			return IdentityList[Domain]{
				Items: items, Restriction: "domains available to current user",
			}, nil
		}
		return IdentityList[Domain]{}, err
	}
	ds, err := domains.ExtractDomains(pages)
	if err != nil {
		return IdentityList[Domain]{}, err
	}
	out := make([]Domain, 0, len(ds))
	names := make(map[string]string, len(ds))
	for _, d := range ds {
		out = append(out, Domain{ID: d.ID, Name: d.Name, Description: d.Description, Enabled: d.Enabled})
		if d.Name != "" {
			names[d.ID] = d.Name
		}
	}
	// Warm the shared domain-name cache from this listing so user rows resolve
	// their domain without a second round trip.
	if len(names) > 0 {
		c.mu.Lock()
		c.domainNames = names
		c.domainNamesAt = time.Now()
		c.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return IdentityList[Domain]{Items: out}, nil
}

// userEmail reads the conventional email address from a user's Extra map, which
// is where Keystone returns it.
func userEmail(u users.User) string {
	if u.Extra == nil {
		return ""
	}
	if v, ok := u.Extra["email"].(string); ok {
		return v
	}
	return ""
}
