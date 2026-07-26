package osclient

import "github.com/gophercloud/gophercloud/v2"

// IsNotFound reports whether err is an OpenStack 404 — used to distinguish a
// since-deleted object (mark the history entry dead) from a transient failure.
func IsNotFound(err error) bool {
	return gophercloud.ResponseCodeIs(err, 404)
}

// effectiveScopeLocked returns the authoritative active scope.
func (c *Clients) effectiveScopeLocked() ScopeInfo {
	return c.scope
}
