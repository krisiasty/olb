package osclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
)

// A project member cannot enumerate identity collections under Keystone's
// default policy, but can read itself and its own group memberships. Its scoped
// token also carries the effective roles for that scope.
func TestIdentityListsFallBackToCurrentToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/users", "/v3/domains", "/v3/groups", "/v3/roles":
			http.Error(w, `{"error":{"message":"Forbidden"}}`, http.StatusForbidden)
		case "/v3/users/u1":
			_, _ = w.Write([]byte(`{"user":{
				"id":"u1","name":"alice","domain_id":"default","enabled":true,
				"description":"cloud user","default_project_id":"p1"
			}}`))
		case "/v3/users/u1/groups":
			_, _ = w.Write([]byte(`{"groups":[
				{"id":"g2","name":"operators","domain_id":"default"},
				{"id":"g1","name":"developers","domain_id":"default","description":"application developers"}
			],"links":{"next":null}}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := &gophercloud.ProviderClient{}
	auth := tokens.CreateResult{}
	auth.Body = map[string]any{"token": map[string]any{
		"user": map[string]any{
			"id": "u1", "name": "alice",
			"domain": map[string]any{"id": "default", "name": "Default"},
		},
		"project": map[string]any{
			"id": "p1", "name": "alpha",
			"domain": map[string]any{"id": "default", "name": "Default"},
		},
		"roles": []map[string]any{
			{"id": "r2", "name": "member"},
			{"id": "r1", "name": "reader"},
		},
	}}
	auth.Header = http.Header{"X-Subject-Token": []string{"token-1"}}
	if err := provider.SetTokenAndAuthResult(auth); err != nil {
		t.Fatal(err)
	}
	identity := &gophercloud.ServiceClient{ProviderClient: provider, Endpoint: server.URL + "/v3/"}
	c := &Clients{services: &serviceClients{provider: provider, identity: identity}}

	userList, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if userList.Restriction != "current user only" || len(userList.Items) != 1 {
		t.Fatalf("user fallback = %+v, want one current user", userList)
	}
	if u := userList.Items[0]; u.ID != "u1" || u.Name != "alice" || u.DomainName != "Default" || !u.Enabled || u.DefaultProjectName != "alpha" {
		t.Fatalf("current user mapped incorrectly: %+v", u)
	}

	domainList, err := c.ListDomains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if domainList.Restriction != "current user's domain" || len(domainList.Items) != 1 {
		t.Fatalf("domain fallback = %+v, want current user's domain", domainList)
	}
	if d := domainList.Items[0]; d.ID != "default" || d.Name != "Default" || !d.Partial {
		t.Fatalf("token domain mapped incorrectly: %+v", d)
	}

	groupList, err := c.ListGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if groupList.Restriction != "current user's groups" || len(groupList.Items) != 2 {
		t.Fatalf("group fallback = %+v, want current user's two groups", groupList)
	}
	if groupList.Items[0].Name != "developers" || groupList.Items[0].DomainName != "Default" {
		t.Fatalf("current-user groups should be sorted and resolve the token domain: %+v", groupList.Items)
	}

	roleList, err := c.ListRoles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if roleList.Restriction != "roles in active token" || len(roleList.Items) != 2 {
		t.Fatalf("role fallback = %+v, want active-token roles", roleList)
	}
	if r := roleList.Items[0]; r.ID != "r2" || r.Name != "member" || !r.TokenScoped || r.ScopeType != "project" || r.ScopeName != "alpha" {
		t.Fatalf("token role mapped incorrectly: %+v", r)
	}
}

// A user's identity domain and the active project's owning domain need not be
// the same. The domains fallback should show the former, while project rows
// should resolve the latter from the scoped token when domain listing is denied.
func TestUnprivilegedDomainAndProjectDomainStayDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/domains":
			http.Error(w, `{"error":{"message":"Forbidden"}}`, http.StatusForbidden)
		case "/v3/projects":
			http.Error(w, `{"error":{"message":"Forbidden"}}`, http.StatusForbidden)
		case "/v3/auth/projects":
			_, _ = w.Write([]byte(`{"projects":[
				{"id":"p1","name":"alpha","domain_id":"project-domain","enabled":true},
				{"id":"p2","name":"personal","domain_id":"user-domain","enabled":true}
			],"links":{"next":null}}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := &gophercloud.ProviderClient{}
	auth := tokens.CreateResult{}
	auth.Body = map[string]any{"token": map[string]any{
		"user": map[string]any{
			"id": "u1", "name": "alice",
			"domain": map[string]any{"id": "user-domain", "name": "Users"},
		},
		"project": map[string]any{
			"id": "p1", "name": "alpha",
			"domain": map[string]any{"id": "project-domain", "name": "Projects"},
		},
	}}
	auth.Header = http.Header{"X-Subject-Token": []string{"token-1"}}
	if err := provider.SetTokenAndAuthResult(auth); err != nil {
		t.Fatal(err)
	}
	identity := &gophercloud.ServiceClient{ProviderClient: provider, Endpoint: server.URL + "/v3/"}
	c := &Clients{services: &serviceClients{provider: provider, identity: identity}}

	domainList, err := c.ListDomains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(domainList.Items) != 1 || domainList.Items[0].ID != "user-domain" || domainList.Items[0].Name != "Users" {
		t.Fatalf("domain fallback = %+v, want authenticated user's domain", domainList)
	}

	projectList, err := c.ListProjectsDetailed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projectList) != 2 || projectList[0].DomainID != "project-domain" || projectList[0].DomainName != "Projects" {
		t.Fatalf("project domain = %+v, want scoped project's owning domain name", projectList)
	}

	relatedProjects, err := c.ListProjectsInDomain(context.Background(), "project-domain")
	if err != nil {
		t.Fatal(err)
	}
	if len(relatedProjects) != 1 || relatedProjects[0].ID != "p1" || relatedProjects[0].DomainName != "Projects" {
		t.Fatalf("related projects = %+v, want available projects filtered to selected domain", relatedProjects)
	}
}

// A deployment may override Keystone's default self-read policy. Even then,
// retain the partial identity embedded in the token rather than showing no user.
func TestUserListFallsBackToTokenWhenSelfReadIsDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"Forbidden"}}`, http.StatusForbidden)
	}))
	defer server.Close()

	provider := &gophercloud.ProviderClient{}
	auth := tokens.CreateResult{}
	auth.Body = map[string]any{"token": map[string]any{
		"user": map[string]any{
			"id": "u1", "name": "alice",
			"domain": map[string]any{"id": "default", "name": "Default"},
		},
	}}
	auth.Header = http.Header{"X-Subject-Token": []string{"token-1"}}
	if err := provider.SetTokenAndAuthResult(auth); err != nil {
		t.Fatal(err)
	}
	identity := &gophercloud.ServiceClient{ProviderClient: provider, Endpoint: server.URL + "/v3/"}
	c := &Clients{services: &serviceClients{provider: provider, identity: identity}}

	got, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || !got.Items[0].Partial || got.Items[0].Name != "alice" {
		t.Fatalf("token-only user fallback = %+v", got)
	}
}

// ListUserAssignments classifies each effective grant as direct or inherited by
// differencing against the user's direct (non-effective) grants.
func TestListUserAssignmentsClassifiesInherited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/role_assignments" {
			t.Errorf("request path = %q, want /v3/role_assignments", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("effective") != "" {
			// effective = direct (admin@alpha) + inherited (member@beta).
			_, _ = w.Write([]byte(`{"role_assignments":[
				{"role":{"id":"r1","name":"admin"},"user":{"id":"u1","name":"alice"},"scope":{"project":{"id":"p1","name":"alpha"}}},
				{"role":{"id":"r2","name":"member"},"user":{"id":"u1","name":"alice"},"scope":{"project":{"id":"p2","name":"beta"}}}
			],"links":{"next":null}}`))
			return
		}
		// direct grants only: admin@alpha.
		_, _ = w.Write([]byte(`{"role_assignments":[
			{"role":{"id":"r1","name":"admin"},"user":{"id":"u1","name":"alice"},"scope":{"project":{"id":"p1","name":"alpha"}}}
		],"links":{"next":null}}`))
	}))
	defer server.Close()

	identity := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v3/",
	}
	c := &Clients{services: &serviceClients{identity: identity}}

	got, err := c.ListUserAssignments(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	inherited := map[string]bool{}
	for _, a := range got {
		inherited[a.RoleName] = a.Inherited
	}
	if inherited["admin"] {
		t.Errorf("admin is held directly, should not be marked inherited")
	}
	if !inherited["member"] {
		t.Errorf("member appears only in the effective set, should be marked inherited")
	}
}

func TestIsServiceName(t *testing.T) {
	for _, name := range []string{"glance", "cinder", "GLANCE", " neutron ", "octavia", "barbican"} {
		if !isServiceName(name) {
			t.Errorf("%q should be recognized as a service name", name)
		}
	}
	for _, name := range []string{"alice", "admin", "cepg_rgw_crypt", ""} {
		if isServiceName(name) {
			t.Errorf("%q should not be recognized as a service name", name)
		}
	}
}

// ListUsers flags service accounts two ways: by well-known name, and by holding a
// role on the service project (which catches deployment-specific names a list
// can't, like cepg_rgw_crypt).
func TestListUsersFlagsServiceAccounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/users":
			_, _ = w.Write([]byte(`{"users":[
				{"id":"u-alice","name":"alice"},
				{"id":"u-cinder","name":"cinder"},
				{"id":"u-rgw","name":"cepg_rgw_crypt"}
			],"links":{"next":null}}`))
		case "/v3/role_assignments":
			if got := r.URL.Query().Get("scope.project.id"); got != "svc" {
				t.Errorf("service-account lookup scoped to %q, want svc", got)
			}
			// Only cepg_rgw_crypt holds a role on the service project.
			_, _ = w.Write([]byte(`{"role_assignments":[
				{"role":{"id":"r1","name":"admin"},"user":{"id":"u-rgw"},"scope":{"project":{"id":"svc","name":"service"}}}
			],"links":{"next":null}}`))
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	identity := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v3/",
	}
	c := &Clients{
		services: &serviceClients{identity: identity},
		// Pre-seed the project-name cache so the service project is found without a
		// projects round trip; the users have no domain/default-project, so no other
		// resolution runs.
		projNames:   map[string]string{"svc": "service", "p1": "alpha"},
		projNamesAt: time.Now(),
	}

	got, err := c.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	svc := map[string]bool{}
	for _, u := range got.Items {
		svc[u.Name] = u.Service
	}
	if svc["alice"] {
		t.Errorf("alice is a human account, should not be flagged")
	}
	if !svc["cinder"] {
		t.Errorf("cinder should be flagged by its well-known name")
	}
	if !svc["cepg_rgw_crypt"] {
		t.Errorf("cepg_rgw_crypt should be flagged by its service-project membership")
	}
}

// An effective user-assignment query returns one entry per grant path, so the
// same role on the same project can arrive several times (directly and via one
// or more groups). listAssignments must collapse those identical summaries.
func TestListUserAssignmentsDedupesEffectivePaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/role_assignments" {
			t.Errorf("request path = %q, want /v3/role_assignments", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// ListUserAssignments issues the effective query and a direct one to
		// classify inheritance; only the effective response needs the duplicates.
		if r.URL.Query().Get("effective") == "" {
			_, _ = w.Write([]byte(`{"role_assignments":[],"links":{"next":null}}`))
			return
		}
		// admin@alpha appears twice (direct + inherited via a group); member@beta once.
		_, _ = w.Write([]byte(`{"role_assignments":[
			{"role":{"id":"r1","name":"admin"},"user":{"id":"u1","name":"alice"},"scope":{"project":{"id":"p1","name":"alpha"}}},
			{"role":{"id":"r1","name":"admin"},"user":{"id":"u1","name":"alice"},"scope":{"project":{"id":"p1","name":"alpha"}}},
			{"role":{"id":"r2","name":"member"},"user":{"id":"u1","name":"alice"},"scope":{"project":{"id":"p2","name":"beta"}}}
		],"links":{"next":null}}`))
	}))
	defer server.Close()

	identity := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v3/",
	}
	c := &Clients{services: &serviceClients{identity: identity}}

	got, err := c.ListUserAssignments(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("duplicate effective paths should collapse to 2 assignments; got %d: %+v", len(got), got)
	}
	byRole := map[string]RoleAssignment{}
	for _, a := range got {
		byRole[a.RoleName] = a
	}
	if a := byRole["admin"]; a.TargetType != "project" || a.TargetName != "alpha" || a.ActorType != "user" {
		t.Fatalf("admin assignment mapped wrong: %+v", a)
	}
	if a := byRole["member"]; a.TargetName != "beta" {
		t.Fatalf("member assignment mapped wrong: %+v", a)
	}
}
