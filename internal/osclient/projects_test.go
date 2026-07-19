package osclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMergeProjectNamesPrefersAdminAndFillsGaps(t *testing.T) {
	admin := []ProjectInfo{
		{ID: "a", Name: "admin-name-a"},
		{ID: "b", Name: "admin-name-b"},
		{ID: "c", Name: ""}, // no name: skipped
	}
	accessible := []ProjectInfo{
		{ID: "a", Name: "accessible-name-a"}, // overlaps: admin must win
		{ID: "d", Name: "accessible-name-d"}, // gap: filled from accessible
		{ID: "e", Name: ""},                  // no name: skipped
	}

	got := mergeProjectNames(admin, accessible)
	want := map[string]string{
		"a": "admin-name-a",
		"b": "admin-name-b",
		"d": "accessible-name-d",
	}
	if len(got) != len(want) {
		t.Fatalf("merged map = %v, want %v", got, want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("merged[%q] = %q, want %q", id, got[id], name)
		}
	}
}

func TestProjectNameMapServesCachedWithinTTL(t *testing.T) {
	// A nil services would nil-panic on any Keystone call, so completing without
	// panic proves the fresh cache short-circuits enumeration entirely.
	cached := map[string]string{"a": "project-a"}
	c := &Clients{projNames: cached, projNamesAt: time.Now()}

	got := c.projectNameMap(context.Background())
	if got["a"] != "project-a" || len(got) != 1 {
		t.Fatalf("cached project map = %v, want %v", got, cached)
	}
}

func TestProjectSelectionScopesClientsAndRestoresOriginalForAllProjects(t *testing.T) {
	original := &serviceClients{project: ProjectInfo{ID: "admin-scope", Name: "admin"}}
	tenant := &serviceClients{project: ProjectInfo{ID: "tenant-a", Name: "tenant-a"}}
	scopeCalls := 0
	c := &Clients{
		Switch: SwitchCapability{
			CanSwitch: true, AllProjectsChecked: true, CanAllProjects: true,
		},
		services:       original,
		activeServices: original,
		scopeProject: func(_ context.Context, target ProjectInfo) (*serviceClients, error) {
			scopeCalls++
			if target.ID != tenant.project.ID {
				t.Fatalf("scope target = %+v, want %+v", target, tenant.project)
			}
			return tenant, nil
		},
		selected: original.project,
		allMode:  true,
	}

	target := ProjectInfo{ID: "tenant-a", Name: "tenant-a"}
	if err := c.SwitchProject(context.Background(), target); err != nil {
		t.Fatalf("SwitchProject: %v", err)
	}
	if c.services != original {
		t.Fatal("project selection replaced the retained startup clients")
	}
	if got, err := c.clientsForLB(context.Background(), "lb-in-tenant"); err != nil || got != tenant {
		t.Fatalf("drill-in clients = %p, %v; want scoped %p", got, err, tenant)
	}
	if got := c.CurrentProject(); got != target {
		t.Fatalf("CurrentProject = %+v, want %+v", got, target)
	}
	if c.AllProjects() {
		t.Fatal("concrete project selection should disable all-projects mode")
	}
	if scopeCalls != 1 {
		t.Fatalf("project authentication calls = %d, want 1", scopeCalls)
	}

	if err := c.SwitchProject(context.Background(), target); err != nil {
		t.Fatalf("second SwitchProject: %v", err)
	}
	if scopeCalls != 2 {
		t.Fatalf("second project switch made %d authentication calls, want 2", scopeCalls)
	}

	if err := c.EnterAllProjects(context.Background()); err != nil {
		t.Fatalf("EnterAllProjects: %v", err)
	}
	if c.services != original {
		t.Fatal("returning to all projects replaced the original authentication clients")
	}
	if got, err := c.clientsForLB(context.Background(), "lb-any-project"); err != nil || got != original {
		t.Fatalf("all-project drill-in clients = %p, %v; want original %p", got, err, original)
	}
	if !c.AllProjects() {
		t.Fatal("EnterAllProjects should enable the all-projects filter")
	}
}

func TestEnterAllProjectsRequiresAdminCapability(t *testing.T) {
	original := &serviceClients{project: ProjectInfo{ID: "startup"}}
	tenant := &serviceClients{project: ProjectInfo{ID: "tenant"}}
	c := &Clients{
		Switch:         SwitchCapability{CanSwitch: true},
		services:       original,
		activeServices: tenant,
		selected:       tenant.project,
		probeAll: func(context.Context) error {
			return errors.New("forbidden")
		},
	}

	err := c.EnterAllProjects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires admin permissions") {
		t.Fatalf("EnterAllProjects error = %v", err)
	}
	if c.activeServices != tenant || c.AllProjects() {
		t.Fatal("denied all-projects entry changed the active scope")
	}
	capability := c.SwitchCapability()
	if !capability.AllProjectsChecked || capability.CanAllProjects || capability.AllProjectsReason == "" {
		t.Fatalf("all-projects capability = %+v", capability)
	}
}

func TestProjectScopedAuthOptionsExchangeSubjectTokenForTargetScope(t *testing.T) {
	target := ProjectInfo{ID: "target-id", Name: "target-name"}

	got := projectScopedAuthOptions("https://identity.example/v3", "subject-token", target)
	if got.IdentityEndpoint != "https://identity.example/v3" || got.TokenID != "subject-token" {
		t.Fatalf("scoped auth does not use startup subject token: %+v", got)
	}
	if got.Scope == nil || got.Scope.ProjectID != target.ID {
		t.Fatalf("scoped auth options = %+v", got)
	}
	if !got.AllowReauth {
		t.Fatalf("scoped auth disabled reauthentication: %+v", got)
	}
	if got.Username != "" || got.Password != "" || got.ApplicationCredentialID != "" {
		t.Fatalf("scoped exchange mixed incompatible auth methods: %+v", got)
	}
}

func TestFailedProjectScopeLeavesCurrentSelectionUntouched(t *testing.T) {
	originalProject := ProjectInfo{ID: "startup", Name: "startup"}
	original := &serviceClients{project: originalProject}
	wantErr := errors.New("scope denied")
	c := &Clients{
		services:       original,
		activeServices: original,
		scopeProject: func(context.Context, ProjectInfo) (*serviceClients, error) {
			return nil, wantErr
		},
		selected: originalProject,
		allMode:  true,
	}

	err := c.SwitchProject(context.Background(), ProjectInfo{ID: "denied", Name: "denied"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SwitchProject error = %v, want %v", err, wantErr)
	}
	if c.activeServices != original || c.CurrentProject() != originalProject || !c.AllProjects() {
		t.Fatalf("failed scope changed client state: active=%p project=%+v all=%v", c.activeServices, c.CurrentProject(), c.AllProjects())
	}
}

func TestResolveProjectSelectorByNameOrID(t *testing.T) {
	projects := []ProjectInfo{
		{ID: "project-a-id", Name: "project-a"},
		{ID: "project-b-id", Name: "project-b"},
	}
	for _, selector := range []string{"project-b", "project-b-id"} {
		got, err := resolveProjectSelector(projects, selector)
		if err != nil {
			t.Fatalf("resolveProjectSelector(%q): %v", selector, err)
		}
		if got.ID != "project-b-id" {
			t.Fatalf("resolveProjectSelector(%q) = %+v, want project-b", selector, got)
		}
	}
}

func TestResolveProjectSelectorRejectsMissingAndAmbiguousNames(t *testing.T) {
	projects := []ProjectInfo{
		{ID: "one", Name: "duplicate"},
		{ID: "two", Name: "duplicate"},
	}
	if _, err := resolveProjectSelector(projects, "missing"); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("missing selector error = %v", err)
	}
	if _, err := resolveProjectSelector(projects, "duplicate"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous selector error = %v", err)
	}
}
