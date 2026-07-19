package tui

import "github.com/krisiasty/olb/internal/model"

// histEntry is one visited location. Entries are identities, not pointers or
// snapshots: they are re-resolved against live (or cached) state on every
// revisit, so a back-press can cost a round trip and can land on a since-deleted
// object (marked dead).
type histEntry struct {
	id     model.Identity
	viaRef bool // reached by following a reference / back-reference edge (breadcrumb ↦)
	dead   bool // object was gone at last resolution
}

// history is a browser-style single ordered list plus a cursor.
//
//   - Back / forward move the cursor only, never truncate.
//   - Opening a node is new navigation: if the cursor is not at the tip, the
//     forward portion is discarded, then the new entry is appended and the
//     cursor advances to it. This holds even when revisiting a node — append and
//     truncate, exactly like a browser re-visiting a URL.
type history struct {
	entries []histEntry
	cursor  int // -1 when empty
	cap     int
}

func newHistory(capacity int) *history {
	if capacity < 2 {
		capacity = 2
	}
	return &history{cursor: -1, cap: capacity}
}

func (h *history) empty() bool { return len(h.entries) == 0 }

func (h *history) current() (histEntry, bool) {
	if h.cursor < 0 || h.cursor >= len(h.entries) {
		return histEntry{}, false
	}
	return h.entries[h.cursor], true
}

// navigate performs new navigation: truncate the forward portion, append, and
// move the cursor to the new tip. The generous cap bounds picker size and the
// dead-entry bookkeeping, not memory.
func (h *history) navigate(e histEntry) {
	if h.cursor < len(h.entries)-1 {
		h.entries = h.entries[:h.cursor+1]
	}
	h.entries = append(h.entries, e)
	h.cursor = len(h.entries) - 1
	if len(h.entries) > h.cap {
		drop := len(h.entries) - h.cap
		h.entries = append(h.entries[:0], h.entries[drop:]...)
		h.cursor -= drop
		if h.cursor < 0 {
			h.cursor = 0
		}
	}
}

func (h *history) canBack() bool    { return h.cursor > 0 }
func (h *history) canForward() bool { return h.cursor >= 0 && h.cursor < len(h.entries)-1 }

// back moves the cursor toward the past and returns the now-current entry.
func (h *history) back() (histEntry, bool) {
	if !h.canBack() {
		return histEntry{}, false
	}
	h.cursor--
	return h.entries[h.cursor], true
}

// forward moves the cursor toward the future and returns the now-current entry.
func (h *history) forward() (histEntry, bool) {
	if !h.canForward() {
		return histEntry{}, false
	}
	h.cursor++
	return h.entries[h.cursor], true
}

// moveTo sets the cursor to an explicit index (history picker select). This is
// history navigation, not new navigation: it never truncates, so it subsumes
// multi-step back/forward.
func (h *history) moveTo(index int) (histEntry, bool) {
	if index < 0 || index >= len(h.entries) {
		return histEntry{}, false
	}
	h.cursor = index
	return h.entries[h.cursor], true
}

// markDead flags the entry at the cursor as pointing at a deleted object.
func (h *history) markDead() {
	if h.cursor >= 0 && h.cursor < len(h.entries) {
		h.entries[h.cursor].dead = true
	}
}

// pruneDead removes dead entries (on explicit refresh), keeping the cursor on
// the same surviving entry where possible.
func (h *history) pruneDead() {
	if h.empty() {
		return
	}
	kept := make([]histEntry, 0, len(h.entries))
	newCursor := 0
	for i, e := range h.entries {
		if e.dead {
			if i <= h.cursor && newCursor > 0 {
				newCursor--
			}
			continue
		}
		kept = append(kept, e)
		if i == h.cursor {
			newCursor = len(kept) - 1
		}
	}
	h.entries = kept
	if len(kept) == 0 {
		h.cursor = -1
		return
	}
	if newCursor >= len(kept) {
		newCursor = len(kept) - 1
	}
	h.cursor = newCursor
}

// trail returns the identities from the most recent top-level-list boundary up
// to and including the cursor, used to render the breadcrumb. The boundary entry
// itself is excluded; its label is the breadcrumb root (see rootIdentity).
func (h *history) trail() []histEntry {
	if h.cursor < 0 {
		return nil
	}
	start := 0
	for i := h.cursor; i >= 0; i-- {
		if h.entries[i].id.IsTopLevelList() {
			start = i + 1
			break
		}
	}
	return h.entries[start : h.cursor+1]
}

// rootIdentity is the most recent top-level-list boundary at or before the
// cursor; it names the breadcrumb root. Defaults to the LB list.
func (h *history) rootIdentity() model.Identity {
	for i := h.cursor; i >= 0; i-- {
		if h.entries[i].id.IsTopLevelList() {
			return h.entries[i].id
		}
	}
	return model.LBListIdentity
}
