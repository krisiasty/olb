// Package cache holds an LRU of load-balancer status trees, each stamped with a
// fetch time so callers can bound staleness against concurrent mutation by
// other users or processes.
//
// The tool holds one LB's graph at a time, but navigation — jumps across
// hierarchies and history revisits — can touch several LBs, so recently-loaded
// trees are kept and re-used. Deep per-object detail is cached inside the tree's
// own nodes, so it lives and dies with the tree under the same policy.
package cache

import (
	"container/list"
	"sync"
	"time"

	"github.com/krisiasty/olb/internal/model"
)

// Entry is a cached tree plus the moment it was fetched.
type Entry struct {
	Tree    *model.Tree
	Fetched time.Time
}

// TreeCache is a bounded, TTL-aware LRU keyed by load balancer ID.
type TreeCache struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	now   func() time.Time
	ll    *list.List // front = most recently used
	items map[string]*list.Element
}

type node struct {
	key   string
	entry Entry
}

// New returns a cache holding at most capacity trees, each fresh for ttl.
func New(capacity int, ttl time.Duration) *TreeCache {
	if capacity < 1 {
		capacity = 1
	}
	return &TreeCache{
		cap:   capacity,
		ttl:   ttl,
		now:   time.Now,
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}
}

// Get returns the cached tree for id and whether it is still within TTL.
// A cache miss returns (nil, false); a hit past its TTL returns the stale tree
// with fresh=false so callers can choose to re-render it while re-fetching, or
// re-fetch outright.
func (c *TreeCache) Get(id string) (entry Entry, fresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return Entry{}, false
	}
	c.ll.MoveToFront(el)
	n := el.Value.(*node)
	return n.entry, c.now().Sub(n.entry.Fetched) < c.ttl
}

// Peek returns the cached entry without affecting LRU order or freshness.
func (c *TreeCache) Peek(id string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return Entry{}, false
	}
	return el.Value.(*node).entry, true
}

// Put stores tree under id, stamping it with the current time and evicting the
// least-recently-used entry if the cache is over capacity.
func (c *TreeCache) Put(id string, tree *model.Tree) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := Entry{Tree: tree, Fetched: c.now()}
	if el, ok := c.items[id]; ok {
		el.Value.(*node).entry = entry
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&node{key: id, entry: entry})
	c.items[id] = el
	for c.ll.Len() > c.cap {
		c.evictOldest()
	}
}

// Invalidate drops the cached tree for id (the r-refresh escape hatch).
func (c *TreeCache) Invalidate(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		c.ll.Remove(el)
		delete(c.items, id)
	}
}

func (c *TreeCache) evictOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.ll.Remove(el)
	delete(c.items, el.Value.(*node).key)
}
