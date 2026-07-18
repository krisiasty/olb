package osclient

import "github.com/gophercloud/gophercloud/v2"

// IsNotFound reports whether err is an OpenStack 404 — used to distinguish a
// since-deleted object (mark the history entry dead) from a transient failure.
func IsNotFound(err error) bool {
	return gophercloud.ResponseCodeIs(err, 404)
}

// CurrentProject returns the project the current selection is scoped to.
func (c *Clients) CurrentProject() ProjectInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sel != nil {
		return c.sel.project
	}
	return ProjectInfo{}
}

// AllProjects reports whether the tool is listing across all accessible
// projects rather than a single selected project.
func (c *Clients) AllProjects() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.allMode
}

// SwitchCapability reports whether — and if not, why not — the current auth
// method permits switching to another project (and, by extension, listing
// across all of them).
func (c *Clients) SwitchCapability() SwitchCapability { return c.Switch }
