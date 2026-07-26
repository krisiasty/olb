package osclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestListScopesCombinesAndLabelsAvailableScopeKinds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/auth/system":
			_, _ = w.Write([]byte(`{"system":[{"all":true}]}`))
		case "/v3/auth/domains":
			_, _ = w.Write([]byte(`{"domains":[
				{"id":"d2","name":"Second","enabled":true},
				{"id":"default","name":"Default","enabled":true}
			],"links":{"next":null}}`))
		case "/v3/auth/projects":
			_, _ = w.Write([]byte(`{"projects":[
				{"id":"p2","name":"beta","domain_id":"d2","enabled":true},
				{"id":"p1","name":"alpha","domain_id":"default","enabled":true}
			],"links":{"next":null}}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	identity := &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       server.URL + "/v3/",
	}
	current := ScopeInfo{
		Kind: ScopeProject, ID: "p1", Name: "alpha",
		DomainID: "default", DomainName: "Default",
	}
	sc := &serviceClients{identity: identity, scope: current}
	c := &Clients{services: sc, activeServices: sc, scope: current}

	got, err := c.ListScopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("scopes = %+v, want system + 2 domains + 2 projects", got)
	}
	want := []struct {
		kind       ScopeKind
		id         string
		domainName string
	}{
		{ScopeSystem, "all", ""},
		{ScopeDomain, "default", ""},
		{ScopeDomain, "d2", ""},
		{ScopeProject, "p1", "Default"},
		{ScopeProject, "p2", "Second"},
	}
	for i, item := range want {
		if got[i].Kind != item.kind || got[i].ID != item.id || got[i].DomainName != item.domainName {
			t.Errorf("scope[%d] = %+v, want kind=%s id=%s domain=%s", i, got[i], item.kind, item.id, item.domainName)
		}
	}
}

func TestSwitchScopeReplacesClientsAtomically(t *testing.T) {
	originalScope := ScopeInfo{Kind: ScopeProject, ID: "p1", Name: "alpha"}
	original := &serviceClients{scope: originalScope}
	target := ScopeInfo{Kind: ScopeDomain, ID: "default", Name: "Default"}
	scoped := &serviceClients{scope: target}
	c := &Clients{
		services: original, activeServices: original, scope: originalScope,
		scopeAuth: func(_ context.Context, got ScopeInfo) (*serviceClients, error) {
			if !got.Equal(target) {
				t.Fatalf("scope target = %+v, want %+v", got, target)
			}
			return scoped, nil
		},
	}

	if err := c.SwitchScope(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if c.activeServices != scoped || !c.CurrentScope().Equal(target) {
		t.Fatalf("active scope was not replaced: clients=%p scope=%+v", c.activeServices, c.CurrentScope())
	}
	wantErr := errors.New("denied")
	c.scopeAuth = func(context.Context, ScopeInfo) (*serviceClients, error) {
		return nil, wantErr
	}
	if err := c.SwitchScope(context.Background(), ScopeInfo{Kind: ScopeSystem, ID: "all"}); !errors.Is(err, wantErr) {
		t.Fatalf("SwitchScope error = %v, want %v", err, wantErr)
	}
	if c.activeServices != scoped || !c.CurrentScope().Equal(target) {
		t.Fatal("failed scope authentication changed active state")
	}
}

func TestSwitchScopeHidesLowLevelAuthenticationFailure(t *testing.T) {
	originalScope := ScopeInfo{Kind: ScopeProject, ID: "p1", Name: "alpha"}
	original := &serviceClients{scope: originalScope}
	target := ScopeInfo{Kind: ScopeDomain, ID: "default", Name: "Default"}
	cause := gophercloud.ErrUnexpectedResponseCode{
		URL:      "https://identity.example/v3/auth/tokens",
		Method:   http.MethodPost,
		Expected: []int{http.StatusCreated},
		Actual:   http.StatusForbidden,
		Body:     []byte(`{"error":{"message":"policy details that should stay private"}}`),
	}
	c := &Clients{
		services: original, activeServices: original, scope: originalScope,
		scopeAuth: func(context.Context, ScopeInfo) (*serviceClients, error) {
			return nil, cause
		},
	}

	err := c.SwitchScope(context.Background(), target)
	var switchErr *ScopeSwitchError
	if !errors.As(err, &switchErr) {
		t.Fatalf("SwitchScope error = %T %v, want ScopeSwitchError", err, err)
	}
	got := err.Error()
	if !strings.Contains(got, `Could not switch to domain "Default"`) ||
		!strings.Contains(got, "not authorized") {
		t.Fatalf("friendly error = %q", got)
	}
	for _, leaked := range []string{"HTTP", "403", "identity.example", "policy details"} {
		if strings.Contains(got, leaked) {
			t.Errorf("friendly error leaked %q: %q", leaked, got)
		}
	}
	var responseErr gophercloud.ErrUnexpectedResponseCode
	if !errors.As(err, &responseErr) || responseErr.Actual != http.StatusForbidden {
		t.Fatal("ScopeSwitchError should retain the original cause for diagnostics")
	}
	if c.activeServices != original || !c.CurrentScope().Equal(originalScope) {
		t.Fatal("failed scope authentication changed active state")
	}
}

func TestScopedAuthOptions(t *testing.T) {
	tests := []struct {
		scope ScopeInfo
		check func(*gophercloud.AuthScope) bool
	}{
		{ScopeInfo{Kind: ScopeSystem, ID: "all"}, func(s *gophercloud.AuthScope) bool { return s.System }},
		{ScopeInfo{Kind: ScopeDomain, ID: "d1"}, func(s *gophercloud.AuthScope) bool { return s.DomainID == "d1" }},
		{ScopeInfo{Kind: ScopeProject, ID: "p1"}, func(s *gophercloud.AuthScope) bool { return s.ProjectID == "p1" }},
	}
	for _, test := range tests {
		got, err := scopedAuthOptions("https://identity.example/v3", "subject-token", test.scope)
		if err != nil {
			t.Fatal(err)
		}
		if got.TokenID != "subject-token" || got.Scope == nil || !test.check(got.Scope) {
			t.Errorf("auth options for %s = %+v", test.scope.Kind, got)
		}
	}
}
