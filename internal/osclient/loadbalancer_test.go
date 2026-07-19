package osclient

import "testing"

func TestFilterLoadBalancersCanReturnToOriginalAllProjectsList(t *testing.T) {
	all := []LB{
		{ID: "lb-a1", ProjectID: "a"},
		{ID: "lb-b1", ProjectID: "b"},
		{ID: "lb-c1", ProjectID: "c"},
	}

	filtered := filterLoadBalancers(all, ProjectInfo{ID: "b", Name: "project-b"})
	if len(filtered) != 1 || filtered[0].ID != "lb-b1" {
		t.Fatalf("project filter returned %+v", filtered)
	}
	if filtered[0].ProjectName != "project-b" {
		t.Fatalf("filtered row project name = %q", filtered[0].ProjectName)
	}
	if len(all) != 3 {
		t.Fatalf("project filtering mutated the original all-projects list: %+v", all)
	}

	restored := filterLoadBalancers(all, ProjectInfo{})
	if len(restored) != len(all) {
		t.Fatalf("clearing the filter returned %d rows, want %d", len(restored), len(all))
	}
}
