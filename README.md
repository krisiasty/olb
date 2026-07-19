# olb — OpenStack Load Balancer CLI

`olb` is an interactive terminal UI for inspecting OpenStack **Octavia** load
balancers (both the **Amphora** and **OVN** provider drivers). It fetches a load
balancer's structure in a single `status show` call, then lets you drill down
containment edges and jump along reference edges — including the backward query a
tree view can never answer: *"who points at this pool?"*

This is the v1 deliverable: **read / inspect, interactive-only**. A
non-interactive scriptable mode (`--output json|yaml`, exit codes) is deferred.

## Install / build

Requires Go 1.24+.

```sh
make build        # -> ./olb   (cgo-free)
make dist         # -> dist/olb-<os>-<arch> for all five supported targets
go install github.com/krisiasty/olb@latest
```

Supported targets (all built cgo-free from one machine): `windows/amd64`,
`darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`.

## Usage

With no arguments, `olb` lists the load balancers in your current project:

```sh
olb                        # uses OS_* env / clouds.yaml
olb --os-cloud mycloud     # pick a clouds.yaml entry
olb --project other-proj   # select an initial project filter (name or ID)
olb --print                # copy actions show the value instead of OSC 52
olb --licenses             # print embedded third-party notices
olb --version
```

### Authentication

Auth sources mirror `python-openstackclient`, so existing credentials work
unchanged: `OS_*` environment variables, `clouds.yaml` (via `--os-cloud` /
`OS_CLOUD`), and CLI flags (`--os-auth-url`, `--os-username`, …). Precedence is
**CLI flags > environment > clouds.yaml**.

`--project` selects the initial TUI project filter without changing the
authentication scope. Use `--os-project-name` or `--os-project-id` when the
authentication request itself must be scoped to a particular project.

### Project switching

Press `p` to filter the load-balancer view by project without leaving the tool.
The selection is deliberately **not** an authentication operation: `olb` keeps
the token and service clients created at startup and locally filters the
load-balancers visible through that original authorization context. This means
an admin does not lose cluster-wide visibility after viewing one project, and
the selector also works with bare tokens and application credentials.

The switcher's first entry, **⟨ all accessible projects ⟩** (or start with
`--all-projects`), shows the original unfiltered Octavia result, with each row
tagged by project where a name is known. For an admin this is Octavia's global
list (the whole cluster, including projects on which the admin has no explicit
role). For a tenant, it remains constrained by the original token's policy and
scope. Drilling into a load balancer likewise continues to use the original
service clients.

### Keybindings

| Group | Keys | Action |
|---|---|---|
| Move | `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` | Move / page / jump selection |
| Navigate | `enter` | Open selected — drill into a child **or** follow a reference edge (the only "go in" key) |
| | `←` / `esc` / `⌫` | Back (history) |
| | `→` | Forward (history) |
| | `ctrl+home` | Return to the load balancer list |
| | `h` | History picker overlay |
| Inspect | `y` / `j` | Raw API object as YAML / JSON |
| | `i` / `n` / `o` | Copy id / name / displayed raw object |
| Search | `/` | Filter current list (substring) |
| | `s` | Cycle status filter — all / error / degraded |
| Global | `p` `r` `a` `+`/`-` `?` `q` | Project · refresh · auto-refresh toggle/interval · help · quit (back out, then exit) |
| | `ctrl+c` | Force quit |

`enter` is the only descent key; the arrows are reserved for history. `esc`
clears an active filter first, otherwise it is *back*. Reference edges render as
`→` rows and back-references as `←` rows; `↦` in the breadcrumb marks a
reference jump.

## How it works

- **Structure in one call.** `loadbalancer status show` returns the whole nested
  tree with `provisioning_status`/`operating_status` on every node; the in-memory
  graph is built from it, avoiding an N+1 fan-out of list calls.
- **Load-balancer overview.** Opening an LB immediately shows a responsive
  details/stats dashboard above its selectable related objects. Details include
  the owning project name and ID, which disambiguates LBs opened from the global
  scope, and show the primary VIP with its associated floating IP when one
  exists. Additional VIPs are selectable related objects; because all VIPs
  share one Neutron port, floating IPs are matched to them by fixed address. Full
  LB config and traffic counters load independently; Amphora-backed LBs also
  list each backing VM directly by ID and role. The results are cached with the
  status tree. Listener rows include normalized endpoints such as `TCP/443` or
  `HTTPS/8443 (TLS termination)`, always using the listener's actual protocol
  port, so duplicate listener names remain legible.
  Pool rows similarly include protocol, a readable balancing algorithm, and
  member and listener-attachment counts, for example
  `HTTP · round robin · 4 members · 2 listeners`; duplicate sibling names gain
  a short ID suffix. A zero-listener count makes unattached pools explicit.
  Non-selectable headings divide related objects
  into VIP, listener, pool, and Amphora groups; their visible counts update with
  the active text and status filters. The panel and individual group headings
  also roll up ERROR and DEGRADED counts from their currently visible rows.
- **Other detail is lazy.** Per-object `show` calls used for raw inspection and
  precise reference resolution are fetched only when needed.
- **Readable traffic statistics.** Byte totals use IEC units and show throughput,
  cumulative connections show a signed per-second rate, and request errors show
  the exact increase since the previous successful sample. Large counters use
  digit grouping. Counter resets become a new baseline instead of producing a
  negative rate or delta.
- **A graph, not a tree.** Nodes carry typed **containment** and **reference**
  edges, both traversable in either direction, so shared pools and boundary
  crossings (VIP → floating IP, member → Nova instance) are first-class and the
  backward "who points at me?" query works.
- **Caching & freshness.** An LRU of `status show` trees, each with a short TTL,
  bounds staleness; history entries are re-resolved against live/cached state on
  every revisit (a back-press can cost a round trip); `r` force-refreshes while
  retaining the last-known view and selected object until the new responses are
  ready, and prunes dead history entries. Automatic refresh is enabled by
  default: visible overview stats update every 5 seconds (adjustable with `+`
  and `-` through 1/2/5/10/30/60-second steps), while lists, details, and
  related objects update every 30 seconds. Details and related objects show
  their last-successful update age. Fresh automatic stats instead show a moving
  `Points` cadence indicator; after the interval and a short grace window they
  switch to an advancing age and a `stale` marker (manual mode always shows
  age). These display animations make no API calls, and failed refreshes retain
  old values. `a` pauses or resumes
  all automatic requests; overlays and active text filters pause them
  temporarily. Status filters remain applied while refresh continues normally.
  The header summarizes both cadences as `refresh: auto (stats/full)`, for
  example `refresh: auto (5s/30s)`.
- **Graceful degradation.** Admin-only (amphorae) and cross-service (floating IP,
  Nova instance) surfaces degrade with a clear reason when scope or RBAC is
  missing, rather than erroring out or rendering a dead node. OVN-backed LBs have
  no amphora branch at all.

See [docs/DECISIONS.md](docs/DECISIONS.md) for implementation decisions the spec
deferred (clipboard/OSC 52, reference-edge resolution, platform notes).

## Development

```sh
make test            # go test -race ./...
make lint            # vet + gofmt check
make check-licenses  # authoritative go-licenses gate (Apache-2.0-compatible only)
make notices         # regenerate embedded THIRD_PARTY_NOTICES
```

## License

Apache-2.0 (see [LICENSE](LICENSE)). Third-party dependencies are all permissive
(MIT / BSD / ISC / Apache-2.0); their notices are embedded in the binary and
printed by `olb --licenses`. CI enforces the license policy with
`google/go-licenses` over the full transitive tree.
