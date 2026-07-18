package osclient

import "github.com/gophercloud/gophercloud/v2"

// IsNotFound reports whether err is an OpenStack 404 — used to distinguish a
// since-deleted object (mark the history entry dead) from a transient failure.
func IsNotFound(err error) bool {
	return gophercloud.ResponseCodeIs(err, 404)
}

// CurrentProject returns the project the clients are currently scoped to.
func (c *Clients) CurrentProject() ProjectInfo { return c.Project }

// SwitchCapability reports whether — and if not, why not — the current auth
// method permits switching to another project.
func (c *Clients) SwitchCapability() SwitchCapability { return c.Switch }
