# olb — OpenStack Load Balancer CLI

`olb` is a CLI tool written in Go to manage OpenStack load balancer instances.

## Scope

The tool targets load balancers provided by the **Octavia** project, supporting both the **Amphora** and **OVN** provider drivers. It does not support the legacy Neutron LBaaS (removed from OpenStack since ~Ussuri).

### Access assumptions

The tool is designed to operate with **tenant / project-scoped API access** by default. Some data is only reachable with admin RBAC (notably the amphora objects — see below). Anything requiring elevated scope must **degrade gracefully** (show as unavailable / "requires admin") rather than error out or render a dead node.

### Provider differences

The two providers expose different object graphs, and the tool must not assume the Amphora shape everywhere:

- **Amphora** — full L4/L7, TLS termination, L7 policies. Backed by amphora VMs (HAProxy).
- **OVN** — load balancing lives in the OVN dataplane; **no amphora objects exist**, and it is L4-focused with limited/absent L7 support. The amphora branch is simply not applicable for OVN-backed LBs.

## Non-goals (v1)

v1 is deliberately bounded to keep the surface small:

- **Interactive-only.** v1 ships the TUI and nothing else. A non-interactive / scriptable mode (one-shot commands, `--output json|yaml|table`, exit codes for automation) is deferred to a later version. This is the natural point to introduce a subcommand framework (e.g. Cobra) — not before.
- **Read / inspect focus.** The navigation, search, and inspection model is the v1 deliverable. Mutating operations beyond what's needed for navigation are out of scope for now.
- **No admin features.** Admin-only surfaces (e.g. enumerating amphorae) are not a v1 goal; they degrade gracefully as described above.

Deferring the non-interactive mode also keeps the v1 dependency set minimal (see below) — the standard-library `flag` package covers the small, flat flag set v1 needs.

## Data model: a graph, not a tree

The load balancer object model is a **graph**. Containment (LB → listener → pool → member) is the backbone, but there are **reference edges** that a strict tree cannot represent:

- An **L7 policy** with action `REDIRECT_TO_POOL` points at *another* pool.
- A **pool** can be shared — referenced as the default pool of multiple listeners and/or the redirect target of multiple policies.
- Boundary-crossing references leave Octavia entirely: a **VIP** maps to a Neutron port (and optionally a floating IP); a **member** address usually corresponds to a Nova instance.

The tool therefore models nodes with **typed edges**, each edge being either *containment* or *reference*, and each traversable in **both directions**. The most valuable graph query during debugging is the backward reference — standing on a pool and asking "who points at me?" — which a tree view can never answer.

## Interactive mode

The primary mode is an interactive TUI with drill-down and cross-node navigation.

- With **no arguments**, list all load balancers in the current (or `--project`-specified) project. Allow filtering the list not just by name but by **status**, so "everything in `ERROR`" is one query.
- **Drill-down** descends containment edges. **Jumps** follow reference edges (e.g. from an L7 redirect policy to its target pool, or from a pool to its backing members' instances). Reference edges appear as **selectable list entries** (e.g. `→ pool:backend-v2`), so there is no separate "jump" key — `enter` follows whatever is selected, containment child or reference.
- A jump entry is **conditional on the edge existing** — e.g. only render `→ pool` when a policy's action is `REDIRECT_TO_POOL`; `REDIRECT_TO_URL` and `REJECT` have no target.
- **Search is substring match in v1.** Fuzzy search (fzf-style) is a nice-to-have for a later version — it swaps the match function only, no architectural change, since search is just a predicate over the in-memory node set.
- For v1, opening a node **reparents** the view (the target's subtree becomes the current tree) with a **breadcrumb** preserving the trail, e.g. `lb › listener:https › l7policy:api › ↦ pool:backend-v2`. A full graph-layout / cross-highlight view is a possible later enhancement.

### Keybindings

| Group | Keys | Action |
|---|---|---|
| Move | `↑` / `↓` | Selection up / down |
| | `PgUp` / `PgDn` | Page up / down |
| | `Home` / `End` | Top / bottom of list |
| Navigate | `enter` | Open selected — drill into a child, or follow a reference edge. The only "go in" key. |
| | `←` / `esc` / `⌫` | Back (history) |
| | `→` | Forward (history) |
| | `ctrl+Home` | Return to the load balancer list |
| | `h` | History picker overlay (see below) |
| Inspect | `d` | Toggle detail panel (lazy-loaded full config) |
| | `y` | Show raw API object as YAML |
| | `j` | Show raw API object as JSON |
| | `i` | Copy object ID to clipboard |
| | `n` | Copy object name to clipboard |
| | `o` | Copy the displayed raw object (YAML or JSON, whichever is shown) |
| Search | `/` | Filter current list (substring) |
| | `s` | Cycle status filter — all / error / degraded |
| Global | `p` | Project switcher |
| | `r` | Refresh — re-fetch current tree |
| | `?` | Help — keymap overlay |
| | `q` | Quit (back out, then exit) |
| | `ctrl+c` | Force quit |

Disambiguation rules (state these explicitly, they are not separate keys):

- `enter` is the **only** descent key; the arrows are reserved for history (`←` back, `→` forward). This split avoids overloading `→` with both "go in" and "forward," which are different actions — `enter` acts on the highlighted row, forward re-navigates to a specific history entry regardless of selection.
- `esc` is contextual: if a filter is active it **clears the filter**; otherwise it acts as **back**.
- `o` is a **no-op when no raw object is displayed** (neither `y` nor `j` pressed). Rather than silent, flash a one-line status hint ("no raw object shown — press y or j first"), since a dead key with zero feedback reads as a bug.
- `←` as back is unambiguous only because a list has no horizontal cursor movement. If a future side-by-side detail pane becomes independently focusable, `←`/`→` may need to mean "move focus between panes" while focused there.

### Navigation history

History is **browser-style**: a single ordered list of visited entries plus a **cursor**.

- **Back** (`←`) moves the cursor toward the past; **forward** (`→`) toward the future. Both are **history navigation** — they move the cursor only, never truncate.
- **Opening a node** (`enter`) is **new navigation**: if the cursor is not at the tip, everything after it is **discarded**, then the new entry is appended and the cursor moves to it. (This is the one invariant to get right: moving *within* history never discards; navigating *anew* always discards the forward portion.)
- Revisiting a previously-seen node via `enter` still counts as new navigation — it appends and truncates, exactly like a browser re-visiting a URL. No special-casing.
- **History picker** (`h`) opens an overlay listing the trail, reusing the list navigation keys (`↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End`) plus `/` search; `enter` on a highlighted entry moves the cursor there. Picker-select is history navigation (no truncation), so it subsumes multi-step back/forward.

**Entries are identities, not pointers or snapshots.** Because jumps can cross into other LB hierarchies (via fuzzy search or the list), and because other users/processes can create, change, or delete objects meanwhile, a history entry stores a lightweight identity — object type, object ID, owning LB ID, and a cached label for the picker — **not** a live graph pointer and **not** a full snapshot.

Revisiting (back, forward, or picker-select) is therefore a **re-resolution against live state**:

- If the owning LB's tree is cached and within TTL, render from cache.
- Otherwise re-fetch that LB's `status show` (so a back-press **can cost a round trip** — history navigation is not free) and locate the node.
- If the object is **gone**, mark the entry **dead**: show "this object was deleted since you last viewed it," and drop the cursor to its nearest surviving ancestor (or the LB list). Forward hops can land on dead entries too — same handling. Never render stale data as if current, never crash.

**Length policy:** entries are cheap in memory (a few bytes of identity), so RAM is not the constraint — the real costs are the possible re-fetch on revisit and the dead/changed-entry bookkeeping. Keep history **bounded** (a generous cap, e.g. a few hundred hops) and **prune dead entries on refresh** (`r`). The cap is for picker usability and correctness bookkeeping, not memory.

## Object model

```
Load Balancer
├── VIP  (vip_address, vip_port_id)
│   └── Floating IP           reference → Neutron; often absent (internal LBs have none)
├── Listener  (protocol + port)
│   ├── Pool  (algorithm + session persistence)
│   │   ├── Members           (backend servers; each → Nova instance by reference)
│   │   └── Health monitor    singular, optional (a pool has at most one)
│   └── L7 policy  (routing action)
│       ├── L7 rules          (match conditions)
│       └── → redirect pool   reference, only when action = REDIRECT_TO_POOL
└── Amphora                   ADMIN ONLY; Amphora provider only; hide/degrade otherwise
```

Notes on specific nodes:

- **Amphora** — an amphora *is* the HAProxy VM; there is no separate "amphora → VMs" nesting. Topology is `SINGLE` (one VM) or `ACTIVE_STANDBY` (two, VRRP failover). Steady-state max is two, but the count can **transiently exceed two** during failover/rotation (a replacement boots before the old one is torn down), with the extra amphorae in a pending/transitional status. Do not hardcode "1 or 2" — render whatever the API returns and rely on per-amphora status. Admin-only and Amphora-provider-only.
- **VIP / Floating IP** — the VIP is a property of the LB. Resolving a floating IP is a **Neutron** call against the VIP port, and many LBs (internal ones) have none. Supporting this node means the tool spans Octavia *and* Neutron and needs Neutron read scope.
- **Health monitor** — singular and optional; render as a single optional child, not a list.

## API strategy

The tool should feel snappy by fetching structure once and loading detail lazily.

- **Structure (one call):** `openstack loadbalancer status show <lb-id>` returns the entire nested `statuses` tree — listeners → pools → (members + health monitor) → l7policies → l7rules — with `provisioning_status` and `operating_status` on every node. Build the in-memory graph from this: containment edges from the nesting, reference edges from `default_pool_id` / `redirect_pool_id`. Avoids an N+1 fan-out of separate list calls.
- **Detail (lazy, on landing):** per-object `show` for deep config not present in the status tree (algorithm, persistence, member weights, monitor thresholds, etc.).
- **Traffic (leaf detail):** `openstack loadbalancer stats show` provides byte/connection counters — a good leaf-level detail panel. Not to be confused with `status show`.
- **Status everywhere:** use the per-node statuses for tree color-coding (e.g. ONLINE / DEGRADED / ERROR) and for the status-based filter in the list view.

## State, caching & freshness

The tool holds one LB's graph in memory at a time (built from that LB's `status show`), but navigation — jumps across hierarchies, history revisits — can touch multiple LBs. To keep this responsive without serving stale data:

- **LRU cache of LB trees.** Cache recently-loaded `status show` results keyed by LB ID, evicting least-recently-used beyond a small bound. History re-resolution checks this cache before re-fetching.
- **TTL per cached tree.** Each cached tree carries a short time-to-live to bound staleness against concurrent mutation by other users/processes. Within TTL, render from cache; past it, re-fetch on next access.
- **Explicit refresh.** `r` force-invalidates the current tree (and re-fetches), and prunes dead history entries. This is the user's escape hatch when they know reality has changed.
- **Honest posture for a read/inspect tool:** show last-known state, be clear about when it was fetched, and re-resolve on revisit rather than trusting an old pointer. Object *creation* by others doesn't invalidate any specific entry, but it does drift a cached tree from reality — which TTL and explicit refresh exist to bound.

Deep per-object detail (from lazy `show` calls) can be cached alongside its tree under the same TTL, so toggling the detail panel on a node you already inspected doesn't re-fetch.

## Behavioral notes

- Deletion in Octavia is **bottom-up**: a pool with members or an LB with listeners cannot be deleted unless `--cascade` is used on the LB. The tool should mirror this with clear, actionable messages rather than surfacing raw HTTP 409 conflicts.
- Command verbs mirror the object model:
  `openstack loadbalancer {listener,pool,member,healthmonitor,l7policy,l7rule} …`, always scoped to a parent. `olb`'s command structure can follow the same nesting.

## Authentication & project selection

### Authentication

Support all the standard OpenStack authentication sources, matching `python-openstackclient` conventions so existing credentials work unchanged:

- **Environment variables** — the `OS_*` family (`OS_AUTH_URL`, `OS_USERNAME`, `OS_PASSWORD`, `OS_PROJECT_NAME`/`OS_PROJECT_ID`, `OS_USER_DOMAIN_NAME`, `OS_PROJECT_DOMAIN_NAME`, `OS_REGION_NAME`, `OS_APPLICATION_CREDENTIAL_ID`/`_SECRET`, `OS_TOKEN`, …).
- **clouds.yaml** — selected via `--os-cloud` / `OS_CLOUD`, discovered in the usual locations (`./`, `~/.config/openstack/`, `/etc/openstack/`, or `OS_CLIENT_CONFIG_FILE`).
- **CLI flags** — `--os-auth-url`, `--os-username`, `--os-cloud`, etc., mirroring the openstackclient flag names.

Precedence: **CLI flags > environment variables > clouds.yaml**. Implementation-wise, gophercloud's `utils/openstack/clientconfig` package already parses clouds.yaml and the `OS_*` env vars into auth options, so most of this is wiring rather than bespoke parsing.

### Project selection

If the credentials/token are **already scoped to a project**, `olb` operates only on objects in that project by default.

A built-in **project selector** lets the user switch to another project without leaving the tool. The OpenStack wrinkle to design around: **a project-scoped token's scope is immutable** — "switching projects" is not mutating the current token but **re-authenticating** with a new scope. Concretely:

- To populate the selector, call Keystone `GET /v3/auth/projects` ("projects this token may access"). This works for **any** authenticated user — unlike `project list`, which needs admin. Use the former so the selector works for regular users.
- To switch, request a **new token scoped to the chosen project**, then rebuild the Octavia/Neutron/Nova service clients against it.
- Re-scoping requires retained **credentials** (username/password or an app cred with multi-project roles). The switcher's availability therefore **depends on auth method**, and must degrade gracefully:
  - Authenticated by bare token (`OS_TOKEN`) with no credentials → cannot re-scope; show current project only, selector disabled with a reason.
  - **Application credentials** are bound to a single project → project switching is unavailable; same graceful degradation.
- Region is a separate axis from project; if multi-region matters, treat it as its own selector rather than folding it into project switching.

### Project switching — capability detection and error messaging

Determine switching capability **up front, right after auth** — the auth method is known at that point, so decide whether switching is possible *before* the user attempts it. Show the selector in a visibly **disabled** state with the reason inline, rather than letting the user pick a project and then hard-failing mid-flow.

There are distinct failure points, and the tool must not conflate them — each gets a specific, actionable message (reason + what to do):

- **Bare token** (`OS_TOKEN`, no retained credentials) — can't re-scope, nothing to re-authenticate with.
  - Reason: "Authenticated with a pre-issued token, which can't be re-scoped to another project."
  - Suggestion: "To switch projects, authenticate with credentials (clouds.yaml or `OS_USERNAME`/`OS_PASSWORD`) or an application credential with access to multiple projects."
- **Application credential** — bound to a single project by design.
  - Reason: "Application credentials are locked to the project they were created for."
  - Suggestion: "To switch, use user credentials, or create separate app creds per project and select them via `--os-cloud`."
- **Enumeration failed** — `GET /v3/auth/projects` errored (usually a token/endpoint issue, not the auth method). Keep this distinct so the user doesn't chase the wrong fix.
  - Reason: "Couldn't list accessible projects from the identity service."
  - Suggestion: "Check that the token is valid and the Keystone endpoint is reachable."
- **No role on selected project** — enumeration and switching both work, but the re-scope auth request is rejected for this specific project. A per-project failure, not per-auth-method.
  - Reason: "Your account doesn't have a role on project `<name>`."
  - Suggestion: "Ask an administrator to grant access, or pick a different project."

Guiding principles: distinguish **can't enumerate** from **can enumerate but can't switch** (a user who was shown the list will be confused by a generic "can't switch"); prefer **capability detection over failure-on-attempt**; and make every message **specific to the user's situation** — name the concrete env var or `--os-cloud` mechanism rather than saying "authenticate differently."

## Clipboard

Copy actions (`i` ID, `n` name, `o` displayed raw object) must work across Linux, macOS, and Windows — and, critically, **when run over SSH on a bastion/jump host**, which is the norm for OpenStack tooling.

- **Primary: OSC 52.** Emit the clipboard via the OSC 52 terminal escape sequence, which hands the text to the *terminal emulator* to place on the **local** clipboard. This is OS-independent (the terminal does the work, not the binary), needs no external helpers, and **works over SSH and through tmux** because the sequence travels back over the terminal stream. Supported by modern terminals (iTerm2, kitty, WezTerm, Alacritty, recent xterm, Windows Terminal) and by tmux with `set-clipboard on`.
- **Why not native clipboard libraries as primary:** on Linux there is no OS-level clipboard — libraries shell out to `xclip`/`xsel` (X11) or `wl-copy` (Wayland), which are absent on minimal servers/containers where this tool often runs; cgo-based alternatives need X11 dev libs and a running display and complicate cross-compilation. Worse, over SSH they target the *remote* machine's clipboard, not the user's. macOS (`pbcopy`) and Windows (native) are fine, but the Linux + SSH gap makes native unsuitable as the default.
- **Fallbacks:** a native clipboard library for the rare local-desktop terminal that lacks OSC 52, and a `--print` / print-to-stdout escape hatch so the user can always pipe or hand-copy.
- **Caveats to surface:** OSC 52 must sometimes be enabled (terminal setting, tmux `set-clipboard on`), and some terminals cap the payload size — fine for IDs and names, but a large `o` (raw object) dump may be truncated. This is another reason `i`/`n` (small) are more reliable than `o` (potentially large).
- **Implementation note (verify against current versions):** the Charm ecosystem has been adding OSC 52 support (a standalone helper, and a clipboard command in recent Bubble Tea), so this may not need a hand-rolled escape sequence — check current `charmbracelet` docs for the exact API.

## Implementation notes

- **Language:** Go.
- **OpenStack SDK:** gophercloud (Octavia + Neutron + Nova service clients; `utils/openstack/clientconfig` for auth-source handling).
- **TUI:** the Charm stack — **Bubble Tea** (framework), **Bubbles** (components: `list` with built-in filtering for substring search, `textinput`, `viewport`, `table`, `spinner`), and **Lip Gloss** (styling: status color-coding, breadcrumb bar, layout). Bubble Tea's Elm architecture maps cleanly onto the navigation state (current node, history list + cursor, filter string), and its async `tea.Cmd` model is how the lazy per-object detail fetches and history re-resolution round trips run without blocking the UI.
- **Navigation state:** a history list of identity records (type, object ID, owning LB ID, cached label) plus a cursor; an LRU cache of `status show` trees keyed by LB ID with per-entry TTL; opening a node truncates the forward portion, back/forward/picker move the cursor and re-resolve against the cache or a fresh fetch.
- **Clipboard:** OSC 52 primary (verify Charm ecosystem support), native library + `--print` as fallbacks.

## Dependencies & licensing

Design goal: **minimize external dependencies** and keep every dependency's license **compatible with Apache-2.0** (the project's license).

### Licensing policy

- **Allowed (permissive):** MIT, BSD-2-Clause, BSD-3-Clause, ISC, Apache-2.0.
- **Avoid (copyleft / source-available):** GPL, LGPL, BUSL, SSPL. MPL-2.0 is file-level copyleft and generally combinable, but avoided by preference unless there's no alternative.
- **CI enforcement:** run `google/go-licenses` (Apache-2.0) in CI to walk the *full transitive* dependency tree and fail the build on a disallowed or unknown license. This is authoritative — do not rely on a hand-maintained list.

(Not legal advice; licenses can change — the CI check is the source of truth.)

### Direct dependencies (v1)

| Dependency | Purpose | License |
|---|---|---|
| `gophercloud/gophercloud` (v2) | OpenStack API + Keystone auth / projects | Apache-2.0 |
| `gophercloud/utils` (clientconfig) | clouds.yaml + `OS_*` env parsing | Apache-2.0 |
| `charmbracelet/bubbletea` | TUI framework | MIT |
| `charmbracelet/bubbles` | list, textinput, viewport, spinner | MIT |
| `charmbracelet/lipgloss` | styling / layout | MIT |
| `gopkg.in/yaml.v3` | the `y` raw-as-YAML view | MIT + Apache-2.0 |

Verify current module paths — gophercloud's current major is v2 (`github.com/gophercloud/gophercloud/v2`), with a matching v2 of `gophercloud/utils`; confirm `clientconfig` still lives there, as these have moved across versions.

### Standard library covers the rest

- `encoding/json` — the `j` raw-as-JSON view (no dependency).
- `flag` — the small, flat v1 flag set (`--os-cloud`, `--project`, `OS_*` passthroughs, `--print`). Deliberately not Cobra/pflag in v1; introduce those with the non-interactive mode later.
- `net/http`, `log`/`log/slog` — as needed.

### Notes reducing footprint further

- **OSC 52 clipboard** is likely already transitive via the Charm stack (`aymanbagabas/go-osc52`, MIT, through termenv; recent Bubble Tea exposes a clipboard command). Don't add it as an explicit direct dependency without checking.
- **Fuzzy search** (the later nice-to-have) is effectively free — the Bubbles `list` component already fuzzy-filters internally (`sahilm/fuzzy`, MIT). It's a configuration of a component already in use, not a new library.
- Transitive deps inherited from the Charm stack (`muesli/termenv`, `mattn/go-runewidth`, `mattn/go-isatty`, `rivo/uniseg`, `charmbracelet/x/*`) are MIT or similarly permissive — the `go-licenses` CI check confirms the whole set.

### Attribution & distribution

The permissive licenses in the tree (MIT, BSD, ISC, Apache-2.0) require that redistributions — including **binary** distributions — reproduce each dependency's copyright notice and license text, and that Apache-2.0 `NOTICE` file contents are propagated. Approach:

- **Generate** a single aggregated `THIRD_PARTY_NOTICES` file from the real module graph using `go-licenses` (`go-licenses save` / `report`) as part of the release build, so it regenerates on every release and can't drift from what's actually linked.
- **Embed** it into the binary via `//go:embed` and expose it through an `olb --licenses` command. Embedding is the most robust option: the attributions travel *inside* the binary regardless of how it's distributed (tarball, deb/rpm, Homebrew, container image, or a bare copied binary on a bastion), so no packaging channel can accidentally drop them.
- The file must contain the **full license texts and copyright lines** (not merely a list of module names), and must include any dependency **`NOTICE`** contents — verify `go-licenses` captured `NOTICE` files, not just `LICENSE` files, and add any it missed.
- This is separate from the project's own `LICENSE` (Apache-2.0) and optional own `NOTICE`, which are still shipped normally.

Because the tree is entirely permissive (no GPL/LGPL/MPL), there are **no source-disclosure or object-file-provision obligations** — reproducing notices is the whole requirement.
