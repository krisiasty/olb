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
startup token for a token scoped to that project. With `--global-admin`, it
keeps the startup token and applies a server-side project filter instead. Use
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
matching Octavia, Neutron, Nova, and Barbican clients.

Pass `--global-admin` to explicitly treat the startup credentials as globally
privileged. This mode validates administrative Keystone project enumeration and
a bounded cross-project Octavia read, populates the selector from
`GET /v3/projects`, and retains the original clients when a project is selected.
The selection becomes an Octavia `project_id` filter rather than an
authentication-scope change.

In `--global-admin` mode, the switcher's first entry is
**⟨ all projects ⟩**. Regular mode omits that row and shows a footer hint to
restart with `--global-admin` for the global view. `--all-projects` selects this
view at startup and therefore requires `--global-admin`. Changing the selected
project clears all five workspace histories and returns to the load-balancer
list because previous objects may not exist in the new view.

### Keybindings

| Group | Keys | Action |
|---|---|---|
| Move | `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` | Move / page / jump selection |
| Navigate | `enter` | Open selected — drill into a child **or** follow a reference edge (the only "go in" key) |
| | `←` / `esc` / `⌫` | Back (history) |
| | `→` | Forward (history) |
| | `ctrl+home` | Jump to the active view's pinned root history entry |
| | `h` | History picker overlay |
| Inspect | `y` / `j` | Raw API object as YAML / JSON |
| | `i` / `n` / `o` | Copy id / name / displayed raw object |
| Search | `/` | Filter current list (substring) |
| | `s` | Cycle status filter — all / error / degraded |
| Global | `p` `r` `a` `+`/`-` `t` `?` `q` | Project · refresh · auto-refresh toggle/interval · API telemetry · help · quit (back out, then exit) |
| | `ctrl+c` | Force quit |

`enter` is the only descent key; the arrows are reserved for history. `esc`
clears an active filter first, otherwise it is *back*. Reference edges render as
`→` rows and back-references as `←` rows; `↦` in the breadcrumb marks a
reference jump. Keys `1`–`5` switch persistent load-balancer, VIP, listener,
pool, and Amphora workspaces without adding history entries. Each workspace
retains its own back/forward history, selection, scroll position, and filters;
cross-resource links remain in the workspace where navigation began. The `?`
overlay includes a status-color legend matching dots, status fields, and issue
counts throughout the TUI.

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
- **Local API telemetry.** Every OpenStack HTTP request is timed and classified
  as successful, timed out, or failed; calls taking at least one second also
  count as slow. Press `t` for totals and per-endpoint min/average/median/p95/
  p99/max latency. The snapshot display defaults to five-second auto-refresh,
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
