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
olb --project other-proj   # scope to a different project up front
olb --print                # copy actions show the value instead of OSC 52
olb --licenses             # print embedded third-party notices
olb --version
```

### Authentication

Auth sources mirror `python-openstackclient`, so existing credentials work
unchanged: `OS_*` environment variables, `clouds.yaml` (via `--os-cloud` /
`OS_CLOUD`), and CLI flags (`--os-auth-url`, `--os-username`, …). Precedence is
**CLI flags > environment > clouds.yaml**.

### Project switching

Press `p` to switch projects without leaving the tool. Because a project-scoped
token's scope is immutable, switching **re-authenticates** with the chosen scope,
so it needs retained credentials. The selector's availability is detected up
front and shown disabled with a specific reason when it can't work (bare
`OS_TOKEN`, or an application credential bound to one project).

The switcher's first entry, **⟨ all accessible projects ⟩** (or start with
`--all-projects`), aggregates every load balancer you can see into one list
(each row tagged with its project), from two sources unioned:

- an **unfiltered list** from your current scope — for an **admin** this is
  Octavia's global list (the whole cluster, exactly what `openstack
  loadbalancer list` returns, including projects you hold no role in);
- a **per-project sweep** of the projects you hold a role on
  (`GET /v3/auth/projects`), which gives a non-admin cross-project visibility.

Drilling into a load balancer re-scopes to its owning project on demand where
needed. The default (single-project) view stays scoped to the selected project
so switching is meaningful; use all-projects mode for the cluster-wide view.

### Keybindings

| Group | Keys | Action |
|---|---|---|
| Move | `↑`/`↓`, `PgUp`/`PgDn`, `Home`/`End` | Move / page / jump selection |
| Navigate | `enter` | Open selected — drill into a child **or** follow a reference edge (the only "go in" key) |
| | `←` / `esc` / `⌫` | Back (history) |
| | `→` | Forward (history) |
| | `ctrl+home` | Return to the load balancer list |
| | `h` | History picker overlay |
| Inspect | `d` | Detail panel (lazy full config; LB adds traffic stats) |
| | `y` / `j` | Raw API object as YAML / JSON |
| | `i` / `n` / `o` | Copy id / name / displayed raw object |
| Search | `/` | Filter current list (substring) |
| | `s` | Cycle status filter — all / error / degraded |
| Global | `p` `r` `?` `q` | Project · refresh · help · quit (back out, then exit) |
| | `ctrl+c` | Force quit |

`enter` is the only descent key; the arrows are reserved for history. `esc`
clears an active filter first, otherwise it is *back*. Reference edges render as
`→` rows and back-references as `←` rows; `↦` in the breadcrumb marks a
reference jump.

## How it works

- **Structure in one call.** `loadbalancer status show` returns the whole nested
  tree with `provisioning_status`/`operating_status` on every node; the in-memory
  graph is built from it, avoiding an N+1 fan-out of list calls.
- **Detail is lazy.** Per-object `show` (algorithms, weights, thresholds, and the
  `default_pool_id` / `redirect_pool_id` that back the reference edges) loads on
  landing and is cached with its tree.
- **A graph, not a tree.** Nodes carry typed **containment** and **reference**
  edges, both traversable in either direction, so shared pools and boundary
  crossings (VIP → floating IP, member → Nova instance) are first-class and the
  backward "who points at me?" query works.
- **Caching & freshness.** An LRU of `status show` trees, each with a short TTL,
  bounds staleness; history entries are re-resolved against live/cached state on
  every revisit (a back-press can cost a round trip); `r` force-refreshes and
  prunes dead history entries.
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
