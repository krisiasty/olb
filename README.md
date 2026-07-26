# OLB — OpenStack Live Browser

**OLB — Browse your OpenStack cloud live.**

`olb` is an interactive terminal UI for exploring live OpenStack resources and
the relationships between them. Its load-balancer area supports OpenStack
**Octavia** load balancers (both the **Amphora** and **OVN** provider drivers).
It fetches a load balancer's structure in a single `status show` call, then lets
you drill down containment edges and jump along reference edges — including the
backward query a tree view can never answer: *"who points at this pool?"*

This is the v1 deliverable: **read / inspect, interactive-only**. A
non-interactive scriptable mode (`--output json|yaml`, exit codes) is deferred.

## Install

### Homebrew (macOS)

```sh
brew install krisiasty/tap/olb     # auto-taps krisiasty/homebrew-tap
brew upgrade olb                   # later, to update
```

The cask clears the Gatekeeper quarantine attribute on install, so the binary
runs without any right-click-to-open dance.

### Release archive (Linux, macOS, Windows)

Download the archive for your platform from the
[releases page](https://github.com/krisiasty/olb/releases/latest). Archives are
published for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, and
`windows/amd64` (`.tar.gz` for Linux/macOS, `.zip` for Windows) and bundle the
`olb` binary with `LICENSE`, `NOTICE`, and `THIRD_PARTY_NOTICES`; `checksums.txt`
carries SHA-256 sums for every asset.

On **Linux / macOS** (substitute your os/arch — `linux_amd64`, `linux_arm64`,
`darwin_amd64`, `darwin_arm64`):

```sh
VERSION=0.9.0   # pick the version from the releases page
curl -fLO "https://github.com/krisiasty/olb/releases/download/v${VERSION}/olb_${VERSION}_linux_amd64.tar.gz"
tar -xzf "olb_${VERSION}_linux_amd64.tar.gz"
sudo mv olb /usr/local/bin/olb
```

On **macOS**, the extracted binary is not notarized, so Gatekeeper quarantines
it. Clear the attribute once (Homebrew does this for you):

```sh
xattr -d com.apple.quarantine /usr/local/bin/olb
```

On **Windows**, download `olb_<version>_windows_amd64.zip`, extract it, and put
`olb.exe` somewhere on your `PATH`.

Each platform also ships a bare binary (`olb_<version>_<os>_<arch>`, `.exe` on
Windows) on the same page if you would rather skip extraction — the embedded
`olb --licenses` still reproduces the third-party notices.

### From source

Requires Go 1.26.5+.

```sh
go install github.com/krisiasty/olb@latest
```

## Usage

With no arguments, `olb` lists the load balancers in your current project:

```sh
olb                        # uses OS_* env / clouds.yaml
olb --os-cloud mycloud     # pick a clouds.yaml entry
olb --project other-proj   # select an initial project filter (name or ID)
olb --print                # copy actions show the value instead of OSC 52
olb --api-log api.jsonl    # append sanitized API metadata for debugging
olb --licenses             # print embedded third-party notices
olb --version
```

### Authentication

Auth sources mirror `python-openstackclient`, so existing credentials work
unchanged: `OS_*` environment variables, `clouds.yaml` (via `--os-cloud` /
`OS_CLOUD`), and CLI flags (`--os-auth-url`, `--os-username`, …). Precedence is
**CLI flags > environment > clouds.yaml**.

`--project` selects the initial TUI project. In regular mode this exchanges the
startup token for a token scoped to that project. With `--global-admin`, it first
tries to re-scope the startup credential to that project and otherwise falls back
to a server-side filter (see [Project switching](#project-switching)); a global
administrator that omits `--project` starts in the all-projects view. Use
`--os-project-name` or `--os-project-id` to set the startup authentication
scope itself.

### API debugging log

`--api-log PATH` appends one JSON Lines request event and one correlated
response event for every OpenStack HTTP call, including authentication and
automatic reauthentication. Each completed response records its duration,
HTTP status, OpenStack request ID header, telemetry-compatible `outcome`
(`success`, `timeout`, or `error`), and whether it crossed the one-second slow
threshold. The generated `call_id` connects each request to its response. The
file is created with owner-only (`0600`) permissions.

By default the log contains sanitized URLs, headers, and request/response
metadata but no bodies. `--api-log-bodies` additionally captures valid JSON
bodies up to 64 KiB, recursively redacts fields whose names indicate passwords,
tokens, secrets, credentials, keys, cookies, or signatures, and suppresses
Keystone authentication bodies completely. Oversized, non-JSON, and partially
read bodies are marked but not written. The option requires `--api-log PATH`.

Redaction is deliberately conservative but cannot prove that an unusually
named application field is harmless. API logs can also contain tenant names,
resource addresses, and other operational data, so treat them as sensitive and
remove them when debugging is complete. Problem calls can be selected without
a second log file, for example:

```sh
jq 'select(.event == "response" and (.slow or .outcome != "success"))' api.jsonl
```

### Project switching

Press `p` to change the active project without leaving the tool. Regular mode
populates the selector from Keystone `GET /v3/auth/projects`. A selection
exchanges the startup token for a fresh project-scoped token and creates
matching Octavia, Neutron, Nova, Barbican, and optional Magnum clients.

Pass `--global-admin` to explicitly treat the startup credentials as globally
privileged. This mode validates administrative Keystone project enumeration and
a bounded cross-project Octavia read, and always populates the selector from
`GET /v3/projects` using the startup credentials. Selecting a project first tries
to **re-scope** the credential to it — a real project-scoped token, so
project-scoped resources such as listener TLS certificates become readable. When
the credential cannot be re-scoped to the target (typically a policy-only
administrator with no role assignment on it), the selection falls back to an
Octavia `project_id` **filter** on the retained global token: the scope header
marks it `(filtered)` and certificate details are unavailable for that project.

In `--global-admin` mode, the switcher's first entry is
**⟨ all projects ⟩**. Regular mode omits that row and shows a footer hint to
restart with `--global-admin` for the global view. A global administrator starts
in this all-projects view unless `--project` requests a specific project.
Changing the selected project clears all five workspace histories and returns to
the load-balancer list because previous objects may not exist in the new view.

### Keybindings

| Group | Keys | Action |
| --- | --- | --- |
| Move | `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` | Move / page / jump selection |
| Navigate | `enter` | Open selected — drill into a child **or** follow a reference edge (the only "go in" key) |
| | `←` / `esc` / `⌫` | Back (history) |
| | `→` | Forward (history) |
| | `ctrl+home` | Jump to the active view's pinned root history entry |
| | `h` | History picker overlay |
| Areas | `0` | Area/view switcher — a searchable overlay of every area and view |
| | `A` / `L` | Jump to the auth / load-balancer area (uppercase accelerators) |
| | `1`–`9` | Switch view within the active area |
| Inspect | `y` / `j` | Raw API object as YAML / JSON |
| | `i` / `n` | Copy id / name |
| | `c` | Copy the displayed raw object (inside the YAML/JSON view) |
| Search | `/` | Filter the current list when it contains selectable objects |
| | `s` | Cycle all/error/degraded when the current objects expose status |
| | `o` | Sort a top-level list by a name/id/IP column, ascending (esc cancels, enter selects) |
| Global | `p` `r` `a` `t` `?` `q` | Project · refresh · auto-refresh · telemetry · help · quit |
| Stats views | `+`/`-` | Adjust the load-balancer/listener statistics refresh interval |
| | `ctrl+c` | Force quit |

`enter` is the only descent key; the arrows are reserved for history. `esc`
clears an active filter first, otherwise it is *back*. Reference edges render as
`→` rows and back-references as `←` rows; `↦` in the breadcrumb marks a
reference jump.

olb opens on a **home overview** that orients you before you dive in: your
current scope, the authenticated identity and roles (read from the token, no API
call), and the browsable areas with their accelerators. Press an area key
(`S`/`A`/`L`), `0` for the switcher, or `` ` `` to return to the overview at any
time.

Views are grouped into **areas**. The load-balancer area (`L`) holds the load
balancer, VIP, listener, pool, and amphora views; the identity & access area
(`A`) holds the Keystone identity views (users, domains, groups, a browsable
projects list, roles); and the service catalog area (`S`) holds the services,
endpoints, and regions. That projects view is for exploring/inspecting projects
and is distinct
from the global project selector (`p`), which re-scopes the load-balancer and
other resource views. Uppercase accelerators jump straight to an area, `0` opens a searchable
switcher over every area and view, and number keys `1`–`9` select a view *within
the active area* (so the same digit means a different view per area). Returning
to an area restores the view you last used there. The area is shown as a chip at
the start of the breadcrumb, and the project selector stays global across every
area. Each view is a persistent workspace with its own back/forward history,
selection, scroll position, and filters, switched without adding history
entries; cross-area reference edges (an LB member → its Nova instance, an LB →
its COE cluster) open in place and never change the active area. The `?` overlay
includes a status-color legend matching dots, status fields, and issue counts
throughout the TUI.

The identity views cross-link through **related objects**, so you can walk the
permission graph by drilling in from any side. A **domain** lists its projects,
groups, and users; a **user** shows its domain and the groups it belongs to; a
**group** shows its domain and its member users; a **project** shows its domain;
and a **role** shows the roles it implies and its **assignments** (which user or
group holds it on which project/domain).

When Keystone policy denies full identity collections, olb falls back to
self-service data instead of leaving the domains, users, groups, and roles views
empty: the domains view shows the authenticated user's domain, the users view
shows the authenticated user, the groups view shows that user's memberships,
and the roles view shows the roles effective in the active token scope. Each
restricted view labels its source explicitly. Group members, role assignments,
and the complete role catalog can still require elevated RBAC. Token-backed role
rows therefore use **source** and **scope** columns, and their detail view omits
the unavailable implied-role and assignment sections.

**Role assignments** are surfaced from every side, so both "what can this user
do" and "who has access here" are one drill-in away:

- a **user** and **group** list their **role assignments** as `role:X on
  <target>`, opening the role. A user's list is *effective* (headed **effective
  role assignments**), so roles inherited through group membership appear
  alongside direct grants; a group's list is direct. Each row's marker shows how
  the grant is held: a solid `●` for a role held **directly**, a hollow `○` for
  one **inherited** (via a group or a parent/domain scope). The distinct projects
  they hold a role on are also listed as a **projects** section, so you can jump
  straight to a project you have access to — even one outside the current scope;
- a **project** and **domain** list their assignments as `<actor> as role:X`,
  opening the actor;
- a role from the full **role catalog** lists its assignments as `<actor> on
  <target>`, opening the actor.

Related objects load lazily on landing and degrade gracefully when RBAC or scope
is missing (an empty section still shows its header, e.g. `ROLE ASSIGNMENTS 0`).
Token-backed role details are the exception: the token contains effective role
and scope information but no implication or assignment graph, so those sections
are omitted rather than shown as empty.

The **service catalog** views cross-link the same way: a **service** lists its
**endpoints**, an **endpoint** opens its service and region, and a **region**
lists the endpoints located in it (and its parent region). The endpoints list is
shared, so a service's and a region's related endpoints are derived from it
rather than re-fetched.

Press `*` at any time for the **current token** pop-up (whoami): the
authenticated user, the token's scope (project / domain / system), the roles it
carries, and its expiry. It is a local read of the cached auth result — no API
call — and reflects the token actually in use.

**Service accounts** (the per-service Keystone users like `glance`, `cinder`, or
`barbican`) are flagged so they stand apart from human users: in the users list
they are dimmed and carry a `⚙` marker, the detail labels them a *service
account*, and they can be filtered by the word `service`. Keystone has no native
flag for this, so olb infers it — a user is treated as a service account if it
holds a role on the **service project** (which catches deployment-specific names
like `cepg_rgw_crypt`) or matches a well-known OpenStack service name (the
fallback when the service project can't be enumerated). Both are best-effort.

## How it works

- **Structure in one call.** `loadbalancer status show` returns the whole nested
  tree with `provisioning_status`/`operating_status` on every node; the in-memory
  graph is built from it, avoiding an N+1 fan-out of list calls.
- **Load-balancer overview.** Opening an LB immediately shows a responsive
  details/stats dashboard above its selectable related objects. Details include
  the owning project name and ID, which disambiguates LBs opened from the global
  scope, the flavor ID, creation/update timestamps, an optional non-empty
  description, and the primary VIP with its associated floating IP when one
  exists. Additional VIPs are selectable related objects; because all VIPs
  share one Neutron port, floating IPs are matched to them by fixed address. Full
  LB config and traffic counters load independently; Amphora-backed LBs also
  list each backing VM directly by ID and role, with its management IP and a
  shortened compute ID shown as `mgmt 10.0.3.20 · vm a1b2c3d4`. The results are
  cached with the status tree. Listener rows include normalized endpoints such
  as `TCP/443` or `HTTPS/8443 (TLS termination)`, always using the listener's
  actual protocol port, so duplicate listener names remain legible.
  Pool rows similarly include protocol, a readable balancing algorithm, and
  member and listener-attachment counts, for example
  `HTTP · round robin · 4 members · 2 listeners`; duplicate sibling names gain
  a short ID suffix. A zero-listener count makes unattached pools explicit.
  Non-selectable headings divide related objects into VIP, listener, pool,
  Amphora, COE cluster, and Kubernetes service groups; their visible counts
  update with the active text and status filters. The panel and individual group
  headings also roll up ERROR and DEGRADED counts from their currently visible
  rows.
- **Kubernetes relationships without N+1 requests.** Kubernetes Service load
  balancers are recognized as
  `kube_service_<cluster UUID>_<namespace>_<service>`, while CAPI API-server load
  balancers are matched through the Magnum cluster's `stack_id`. One asynchronous
  Magnum cluster-list request is indexed and reused for every matching load
  balancer in the active scope and cached for 60 seconds; manual refresh bypasses
  the cache. COE clusters and inferred Kubernetes services open as lightweight
  detail views; missing, inaccessible, or slow Magnum data never blocks Octavia
  rendering.
- **Other detail is lazy.** Per-object `show` calls used for raw inspection and
  precise reference resolution are fetched only when needed.
- **Readable traffic statistics.** Byte totals use IEC units and show throughput,
  cumulative connections show a signed per-second rate, and request errors show
  the exact increase since the previous successful sample. Large counters use
  digit grouping. Counter resets become a new baseline instead of producing a
  negative rate or delta.
- **Application and API telemetry.** Press `t` for Go runtime health including
  uptime, CPU concurrency, and current/max-observed goroutines, OS threads, and
  memory usage, alongside OpenStack request totals and per-endpoint
  min/average/median/p95/p99/max latency. Requests are classified as successful,
  timed out, or failed; calls taking at least one second also count as slow. The
  snapshot display defaults to five-second auto-refresh,
  with `r`, `a`, `+`/`-` (`=` is `+`), and `z` providing manual refresh, cadence,
  and reset controls. The overlay does not pause normal API auto-refresh.
  Telemetry collection itself is in-memory only and never stores or exports
  bodies, credentials, query values, or full resource UUIDs. The independent,
  explicitly enabled `--api-log` debugging facility can persist sanitized
  request metadata and, only with `--api-log-bodies`, size-limited JSON bodies.
- **A graph, not a tree.** Nodes carry typed **containment** and **reference**
  edges, both traversable in either direction, so shared pools and boundary
  crossings (VIP → floating IP, member → Nova instance) are first-class and the
  backward "who points at me?" query works.
- **Caching & freshness.** An LRU of `status show` trees, each with a short TTL,
  bounds staleness; history entries are re-resolved against live/cached state on
  every revisit (a back-press can cost a round trip); `r` force-refreshes while
  retaining the last-known view and selected object until the new responses are
  ready, and prunes dead history entries. Automatic refresh is enabled by
  default: visible load-balancer/listener stats update every 5 seconds
  (adjustable from those views with `+` and `-` through
  1/2/5/10/30/60-second steps), while Octavia lists, details, and related
  objects update every 30 seconds. COE cluster and Kubernetes service details
  use their independent 60-second Magnum cache. Details and related objects show
  their last-successful update age. Fresh automatic stats instead show a moving
  `Points` cadence indicator; after the interval and a short grace window they
  switch to an advancing age and a `stale` marker (manual mode always shows
  age). These display animations make no API calls, and failed refreshes retain
  old values. `a` pauses or resumes
  all automatic requests; overlays and active text filters pause them
  temporarily. Status filters remain applied while refresh continues normally.
  Load-balancer and listener detail headers summarize both cadences as
  `refresh: auto (stats/full)`, for example `refresh: auto (5s/30s)`. Views
  without statistics show only their fixed cadence as `refresh: auto (30s)`.
- **Graceful degradation.** Admin-only (amphorae) and cross-service (floating IP,
  Nova instance, Magnum cluster) surfaces degrade with a clear reason when scope
  or RBAC is missing, rather than erroring out or rendering a dead node.
  OVN-backed LBs have no amphora branch at all.

See [docs/DECISIONS.md](docs/DECISIONS.md) for implementation decisions the spec
deferred (clipboard/OSC 52, reference-edge resolution, platform notes).

## Development

Build locally (all targets are cgo-free: `windows/amd64`, `darwin/amd64`,
`darwin/arm64`, `linux/amd64`, `linux/arm64`):

```sh
go build -o olb .                      # quick local binary
goreleaser build --snapshot --clean    # cross-compile every target -> dist/
```

Day-to-day checks are plain Go tools:

```sh
go test -race ./...                    # tests
go vet ./... && gofmt -l .             # lint (gofmt -l prints unformatted files)
```

Releases are cut by pushing a `v*` tag. The
[release workflow](.github/workflows/release.yml) runs those same checks,
plus the authoritative `go-licenses` gate and regeneration of the embedded
`THIRD_PARTY_NOTICES`, as steps before invoking GoReleaser. To dry-run the build
side locally without publishing:

```sh
goreleaser release --snapshot --clean --skip=publish
```

## Author

Krzysztof Ciepłucha

## Disclaimer

This tool was designed and built with the assistance of AI tools. The design
decisions, architecture, and all code have been reviewed and verified by a
human. The project goes through automated security checks, vulnerability
scanning, and static code analysis on every commit.

That said, this software is provided as-is with no guarantees. It may contain
bugs. **Use at your own risk.**

## License

Apache-2.0 (see [LICENSE](LICENSE)). Third-party dependencies are all permissive
(MIT / BSD / ISC / Apache-2.0); their notices are embedded in the binary and
printed by `olb --licenses`. CI enforces the license policy with
`google/go-licenses` over the full transitive tree.
