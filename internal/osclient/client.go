package osclient

import "github.com/gophercloud/gophercloud/v2"

// IsNotFound reports whether err is an OpenStack 404 — used to distinguish a
// since-deleted object (mark the history entry dead) from a transient failure.
func IsNotFound(err error) bool {
	return gophercloud.ResponseCodeIs(err, 404)
}

// CurrentProject returns the project selected in the TUI. In global-admin mode
// this is a target filter; otherwise the active clients are scoped to it.
func (c *Clients) CurrentProject() ProjectInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selected
}

// AllProjects reports whether global-admin mode currently has no concrete
// project filter.
func (c *Clients) AllProjects() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.allMode
}

// Filtered reports whether the current concrete project selection is served by
// filtering the retained global-admin token rather than a project-scoped
// re-authentication. Filtered selections cannot read the project's Barbican
// secrets, so listener certificate details are unavailable. It is meaningful
// only in global-admin mode with a concrete project selected.
func (c *Clients) Filtered() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.filtered
}

// SwitchCapability reports the configured project-switching strategy and
// whether the global view is available.
func (c *Clients) SwitchCapability() SwitchCapability {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Switch
}
