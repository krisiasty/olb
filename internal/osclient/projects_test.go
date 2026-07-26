package osclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
)

func TestProjectNameMapUsesActiveTokenScope(t *testing.T) {
	tests := []struct {
		name      string
		scope     ScopeInfo
		wantPath  string
		wantQuery string
	}{
		{
			name: "project scope uses self-service projects",
			scope: ScopeInfo{
				Kind: ScopeProject, ID: "p1",
			},
			wantPath: "/v3/auth/projects",
		},
		{
			name:      "domain scope lists its domain",
			scope:     ScopeInfo{Kind: ScopeDomain, ID: "d1"},
			wantPath:  "/v3/projects",
			wantQuery: "domain_id=d1",
		},
		{
			name:     "system scope lists the collection",
			scope:    ScopeInfo{Kind: ScopeSystem, ID: "all"},
			wantPath: "/v3/projects",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.wantPath {
					t.Errorf("path = %q, want %q", r.URL.Path, test.wantPath)
				}
				if r.URL.RawQuery != test.wantQuery {
					t.Errorf("query = %q, want %q", r.URL.RawQuery, test.wantQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"projects":[
					{"id":"p1","name":"alpha","domain_id":"d1"}
				],"links":{"next":null}}`))
			}))
			defer server.Close()

			identity := &gophercloud.ServiceClient{
				ProviderClient: &gophercloud.ProviderClient{},
				Endpoint:       server.URL + "/v3/",
			}
			sc := &serviceClients{identity: identity, scope: test.scope}
			c := &Clients{services: sc, activeServices: sc, scope: test.scope}
			got := c.projectNameMap(context.Background())
			if got["p1"] != "alpha" || len(got) != 1 {
				t.Fatalf("project names = %v", got)
			}
		})
	}
}

func TestMergeProjectNamesPrefersPrimaryAndFillsGaps(t *testing.T) {
	primary := []ProjectInfo{
		{ID: "a", Name: "primary-a"},
		{ID: "b", Name: "primary-b"},
		{ID: "c"},
	}
	fallback := []ProjectInfo{
		{ID: "a", Name: "fallback-a"},
		{ID: "d", Name: "fallback-d"},
	}
	got := mergeProjectNames(primary, fallback)
	want := map[string]string{"a": "primary-a", "b": "primary-b", "d": "fallback-d"}
	if len(got) != len(want) {
		t.Fatalf("merged map = %v, want %v", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("merged[%q] = %q, want %q", id, got[id], name)
		}
	}
}

func TestProjectNameMapServesFreshCache(t *testing.T) {
	cached := map[string]string{"a": "project-a"}
	c := &Clients{projNames: cached, projNamesAt: time.Now()}
	got := c.projectNameMap(context.Background())
	if got["a"] != "project-a" || len(got) != 1 {
		t.Fatalf("cached project map = %v, want %v", got, cached)
	}
}

func TestResolveProjectSelector(t *testing.T) {
	projects := []ProjectInfo{
		{ID: "p1", Name: "same"},
		{ID: "p2", Name: "same"},
		{ID: "p3", Name: "unique"},
	}
	if got, err := resolveProjectSelector(projects, "p2"); err != nil || got.ID != "p2" {
		t.Fatalf("ID resolution = %+v, %v", got, err)
	}
	if got, err := resolveProjectSelector(projects, "unique"); err != nil || got.ID != "p3" {
		t.Fatalf("name resolution = %+v, %v", got, err)
	}
	if _, err := resolveProjectSelector(projects, "same"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous name error = %v", err)
	}
	if _, err := resolveProjectSelector(projects, "missing"); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("missing project error = %v", err)
	}
}
