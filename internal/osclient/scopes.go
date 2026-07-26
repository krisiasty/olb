package osclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/domains"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/projects"
)

// ScopeSwitchError deliberately keeps the low-level authentication failure out
// of user-facing text. The cause remains available through errors.Is/As for
// diagnostics and tests.
type ScopeSwitchError struct {
	Target ScopeInfo
	reason string
	cause  error
}

func (e *ScopeSwitchError) Error() string {
	return fmt.Sprintf("Could not switch to %s. %s", scopeTargetLabel(e.Target), e.reason)
}

func (e *ScopeSwitchError) Unwrap() error {
	return e.cause
}

// ListScopes returns the system, domain, and project scopes to which the
// authenticated user can obtain a token. The current scope is always retained
// even when a deployment does not expose one of the discovery endpoints.
func (c *Clients) ListScopes(ctx context.Context) ([]ScopeInfo, error) {
	c.mu.Lock()
	sc := c.activeServices
	if sc == nil {
		sc = c.services
	}
	current := c.scope
	c.mu.Unlock()
	if sc == nil || sc.identity == nil {
		return nil, fmt.Errorf("identity service is unavailable")
	}

	byKey := map[string]ScopeInfo{}
	add := func(scope ScopeInfo) {
		if scope.Kind == "" || scope.ID == "" {
			return
		}
		byKey[scopeKey(scope)] = scope
	}
	add(current)

	domainNames := map[string]string{}
	if current.DomainID != "" && current.DomainName != "" {
		domainNames[current.DomainID] = current.DomainName
	}
	if current.Kind == ScopeDomain && current.Name != "" {
		domainNames[current.ID] = current.Name
	}

	var successes int
	if pages, err := domains.ListAvailable(sc.identity).AllPages(ctx); err == nil {
		successes++
		if values, err := domains.ExtractDomains(pages); err == nil {
			for _, domain := range values {
				domainNames[domain.ID] = domain.Name
				add(ScopeInfo{Kind: ScopeDomain, ID: domain.ID, Name: domain.Name})
			}
		}
	}
	if pages, err := projects.ListAvailable(sc.identity).AllPages(ctx); err == nil {
		successes++
		if values, err := projects.ExtractProjects(pages); err == nil {
			for _, project := range values {
				if project.IsDomain {
					continue
				}
				add(ScopeInfo{
					Kind: ScopeProject, ID: project.ID, Name: project.Name,
					DomainID: project.DomainID,
				})
			}
		}
	}
	var systems struct {
		Systems []struct {
			All bool `json:"all"`
		} `json:"system"`
	}
	if _, err := sc.identity.Get(ctx, sc.identity.ServiceURL("auth", "system"), &systems, nil); err == nil {
		successes++
		for _, system := range systems.Systems {
			if system.All {
				add(ScopeInfo{Kind: ScopeSystem, ID: "all", Name: "all"})
			}
		}
	}

	if len(byKey) == 0 && successes == 0 {
		return nil, fmt.Errorf("couldn't discover any available authentication scopes")
	}
	out := make([]ScopeInfo, 0, len(byKey))
	for _, scope := range byKey {
		if scope.Kind == ScopeProject && scope.DomainName == "" {
			scope.DomainName = domainNames[scope.DomainID]
		}
		out = append(out, scope)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return scopeOrder(out[i].Kind) < scopeOrder(out[j].Kind)
		}
		if out[i].Kind == ScopeProject {
			di := strings.ToLower(scopeDomainLabel(out[i]))
			dj := strings.ToLower(scopeDomainLabel(out[j]))
			if di != dj {
				return di < dj
			}
		}
		ni, nj := strings.ToLower(out[i].Label()), strings.ToLower(out[j].Label())
		if ni != nj {
			return ni < nj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// SwitchScope obtains a token for target and atomically replaces every service
// client only after authentication and catalog discovery have succeeded.
func (c *Clients) SwitchScope(ctx context.Context, target ScopeInfo) error {
	c.mu.Lock()
	scopeAuth := c.scopeAuth
	c.mu.Unlock()
	if scopeAuth == nil {
		return &ScopeSwitchError{
			Target: target,
			reason: "Authentication-scope switching is unavailable for the current credentials.",
		}
	}
	scoped, err := scopeAuth(ctx, target)
	if err != nil {
		return newScopeSwitchError(target, err)
	}
	c.mu.Lock()
	c.activeServices = scoped
	c.scope = scoped.scope
	if c.scope.Kind == "" {
		c.scope = target
	}
	c.projNames = nil
	c.domainNames = nil
	c.serviceUserIDs = nil
	c.mu.Unlock()
	return nil
}

func newScopeSwitchError(target ScopeInfo, cause error) error {
	reason := "The identity service could not authenticate that scope."
	switch {
	case gophercloud.ResponseCodeIs(cause, http.StatusUnauthorized),
		gophercloud.ResponseCodeIs(cause, http.StatusForbidden):
		reason = "The current credentials are not authorized for that scope."
	case gophercloud.ResponseCodeIs(cause, http.StatusNotFound):
		reason = "That scope is no longer available."
	case errors.Is(cause, context.DeadlineExceeded):
		reason = "The identity service did not respond in time."
	case errors.Is(cause, context.Canceled):
		reason = "The authentication attempt was canceled."
	}
	return &ScopeSwitchError{Target: target, reason: reason, cause: cause}
}

func scopeTargetLabel(scope ScopeInfo) string {
	label := scope.Label()
	switch scope.Kind {
	case ScopeSystem:
		return "system scope"
	case ScopeDomain:
		return fmt.Sprintf("domain %q", label)
	case ScopeProject:
		return fmt.Sprintf("project %q", label)
	default:
		return "the selected scope"
	}
}

// CurrentScope returns the single scope carried by the active token.
func (c *Clients) CurrentScope() ScopeInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scope
}

func scopeKey(scope ScopeInfo) string {
	return string(scope.Kind) + "\x00" + scope.ID
}

func scopeOrder(kind ScopeKind) int {
	switch kind {
	case ScopeSystem:
		return 0
	case ScopeDomain:
		return 1
	case ScopeProject:
		return 2
	default:
		return 3
	}
}

func scopeDomainLabel(scope ScopeInfo) string {
	if scope.DomainName != "" {
		return scope.DomainName
	}
	if scope.DomainID != "" {
		return scope.DomainID
	}
	return "unknown"
}
