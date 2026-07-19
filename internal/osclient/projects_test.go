package osclient

import (
	"context"
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

func TestProjectSelectionKeepsOriginalAuthenticationClients(t *testing.T) {
	original := &serviceClients{project: ProjectInfo{ID: "admin-scope", Name: "admin"}}
	c := &Clients{
		Switch:   SwitchCapability{CanSwitch: true},
		services: original,
		selected: original.project,
		allMode:  true,
	}

	target := ProjectInfo{ID: "tenant-a", Name: "tenant-a"}
	if err := c.SwitchProject(context.Background(), target); err != nil {
		t.Fatalf("SwitchProject: %v", err)
	}
	if c.services != original {
		t.Fatal("project selection replaced the original authentication clients")
	}
	if got, err := c.clientsForLB(context.Background(), "lb-any-project"); err != nil || got != original {
		t.Fatalf("drill-in clients = %p, %v; want original %p", got, err, original)
	}
	if got := c.CurrentProject(); got != target {
		t.Fatalf("CurrentProject = %+v, want %+v", got, target)
	}
	if c.AllProjects() {
		t.Fatal("concrete project selection should disable the all-projects filter")
	}

	if err := c.EnterAllProjects(context.Background()); err != nil {
		t.Fatalf("EnterAllProjects: %v", err)
	}
	if c.services != original {
		t.Fatal("returning to all projects replaced the original authentication clients")
	}
	if !c.AllProjects() {
		t.Fatal("EnterAllProjects should enable the all-projects filter")
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
