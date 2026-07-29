package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/krisiasty/olb/internal/model"
	"github.com/krisiasty/olb/internal/osclient"
)

func TestComputeAreaListsInstances(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))

	if m.activeWorkspace != kindInstance || areaOf(m.activeWorkspace) != areaCompute {
		t.Fatalf("C should open the compute instances workspace; active=%v area=%v", m.activeWorkspace, areaOf(m.activeWorkspace))
	}
	if !m.loc.id.Equal(model.InstanceListIdentity) {
		t.Fatalf("compute root = %+v, want instance list", m.loc.id)
	}
	if len(m.entries) != 1 || m.entries[0].instance.ID != "instance-1" {
		t.Fatalf("instance rows = %+v", m.entries)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{"C-1", "instances", "NAME", "STATUS", "FLAVOR", "ADDRESSES", "CREATED", "api-1", "ACTIVE", "m1.small", "private=10.0.0.12"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compute instance list should contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(lineContaining(view, "STATUS"), "PROJECT") {
		t.Fatalf("project-scoped instance list should omit the redundant project column:\n%s", view)
	}
}

func TestSystemScopedInstanceListShowsOwningProject(t *testing.T) {
	backend := &fakeBackend{
		all: true,
		instances: []osclient.Instance{
			{
				ID: "instance-2", Name: "worker-1", Status: "SHUTOFF",
				ProjectID: "p2", ProjectName: "beta",
				FlavorID: "flavor-2", FlavorName: "m1.medium",
				Addresses: []string{"private=10.0.1.20"}, PrimaryAddress: "10.0.1.20",
				Created: time.Date(2026, 7, 29, 6, 30, 0, 0, time.UTC),
			},
		},
	}
	m := New(backend, Config{PrintMode: true, HistoryCap: 50})
	m.Init()
	m = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.home = false
	m = updExec(t, m, press("C"))

	header := strings.Join(m.columnTitles(), " ")
	if !strings.Contains(header, "PROJECT") {
		t.Fatalf("system-scoped instance columns should include project: %q", header)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "beta") || !strings.Contains(view, "worker-1") {
		t.Fatalf("system-scoped instance row should show its project name:\n%s", view)
	}
	m = upd(t, m, press("d"))
	if header := strings.Join(m.columnTitles(), " "); !strings.Contains(header, "PROJECT ID") {
		t.Fatalf("ID mode should relabel the owning project column: %q", header)
	}
	if row := strings.Join(m.rowCells(m.entries[0]), " "); !strings.Contains(row, "p2") {
		t.Fatalf("ID mode should display the owning project ID: %q", row)
	}
}

func TestInstanceIDModeControlsColumnsAndFiltering(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = upd(t, m, press("d"))
	if got := m.columnTitles()[0]; got != "INSTANCE ID" {
		t.Fatalf("ID-mode first column = %q", got)
	}
	m.filter.SetValue("instance-1")
	m.applyFilters()
	if len(m.entries) != 1 {
		t.Fatalf("ID-mode filter should match the displayed instance ID; rows=%d", len(m.entries))
	}
	m.filter.SetValue("api-1")
	m.applyFilters()
	if len(m.entries) != 0 {
		t.Fatalf("ID-mode filter should not match a hidden instance name; rows=%d", len(m.entries))
	}
}

func TestInstanceDetailUsesStandaloneComputeData(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.isInstanceOverview() || m.loc.node == nil || m.loc.node.ID != "instance-1" {
		t.Fatalf("enter should open standalone instance detail: loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	for _, want := range []string{
		"INSTANCE DETAILS", "api-1", "alpha", "m1.small", "Ubuntu 24.04",
		"private=10.0.0.12", "Libvirt name", "instance-0000012a",
		"ACCESS", "Key pair", "operator", "2026-07-30 08:15:00 UTC",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("instance detail should contain %q:\n%s", want, view)
		}
	}
}

func TestRoleTreeKeyIsInactiveInInstanceDetail(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m = updExec(t, m, press("C"))
	m = upd(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.isInstanceOverview() {
		t.Fatal("setup should open instance detail")
	}

	m = upd(t, m, press("t"))
	if m.overlay != overlayNone || m.flash != "" {
		t.Fatalf("t should be inactive in instance detail: overlay=%v flash=%q", m.overlay, m.flash)
	}
	if !m.isInstanceOverview() {
		t.Fatal("t should leave the instance detail unchanged")
	}
}

func TestInstanceListRBACDenialStaysInViewWithFriendlyMessage(t *testing.T) {
	m := start(t, switchCapability{CanSwitch: true})
	m.backend.(*fakeBackend).instancesErr = osclient.ErrAdminRequired
	m = updExec(t, m, press("C"))

	if !m.loc.isTopLevelList() || m.loc.listKind() != kindInstance {
		t.Fatalf("RBAC denial should leave the instances view active: loc=%+v", m.loc)
	}
	view := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "Nova does not authorize instance listing for this token scope") {
		t.Fatalf("instances view should explain the RBAC denial:\n%s", view)
	}
	if strings.Contains(view, "Expected HTTP response code") || strings.Contains(view, `"forbidden"`) {
		t.Fatalf("instances view must not expose the raw HTTP error:\n%s", view)
	}
	if m.flashErr {
		t.Fatalf("RBAC denial should be an explanatory empty view, not an error flash")
	}
}
