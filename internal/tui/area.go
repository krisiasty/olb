package tui

// areaKind is a top-level functional area — a group of related views. Areas are
// the navigation dimension above workspaces: within an area, number keys 1-n
// switch views; uppercase accelerator keys (or the 0 switcher overlay) switch
// areas. The project selector stays global across every area.
type areaKind int

const (
	areaLB       areaKind = iota // load balancers and their related objects
	areaIdentity                 // Keystone identity & access (users, roles, …)
	areaCatalog                  // Keystone service catalog (services, endpoints, regions)
)

// areaDesc declares one area: its uppercase accelerator key, display label, and
// the ordered views reachable by number keys 1-n within it. Only implemented
// areas appear in the areas table below, so both the accelerator keys and the
// switcher overlay stay in sync with what actually exists — nothing else needs
// updating when an area is added.
type areaDesc struct {
	kind  areaKind
	key   rune       // uppercase accelerator, e.g. 'L', 'A'
	label string     // breadcrumb / switcher area label
	views []listKind // number-key order; index+1 is the digit within the area
}

// areas is the registered navigation surface. Cross-area references (e.g. an LB
// member → its Nova instance) are NOT modeled here; they open in place within
// the current workspace and never change the active area. This table only drives
// deliberate area/view switching.
var areas = []areaDesc{
	{kind: areaCatalog, key: 'S', label: "service catalog", views: []listKind{kindRegion, kindService, kindEndpoint}},
	{kind: areaIdentity, key: 'A', label: "identity & access", views: []listKind{kindDomain, kindProject, kindGroup, kindUser, kindRole}},
	{kind: areaLB, key: 'L', label: "load balancers", views: []listKind{kindLB, kindVIP, kindListener, kindPool, kindAmphora}},
}

// areaKeyStrings returns the uppercase accelerator keys as key.Binding strings,
// keeping the TopLevel binding and the areas table the single source of truth.
func areaKeyStrings() []string {
	out := make([]string, 0, len(areas))
	for _, a := range areas {
		out = append(out, string(a.key))
	}
	return out
}

func areaByKind(k areaKind) areaDesc {
	for _, a := range areas {
		if a.kind == k {
			return a
		}
	}
	return areas[0]
}

// areaByKey resolves an uppercase accelerator to its area.
func areaByKey(r rune) (areaDesc, bool) {
	for _, a := range areas {
		if a.key == r {
			return a, true
		}
	}
	return areaDesc{}, false
}

// areaOf reports which area a view belongs to (defaults to areaLB for the
// historical load-balancer views).
func areaOf(k listKind) areaKind {
	for _, a := range areas {
		for _, v := range a.views {
			if v == k {
				return a.kind
			}
		}
	}
	return areaLB
}

// viewsInArea returns the ordered views of an area; index+1 is the number key.
func viewsInArea(k areaKind) []listKind { return areaByKind(k).views }

// viewNumber is the 1-based number key of a view within its area (0 if absent),
// used to label the breadcrumb area chip, e.g. "L-3".
func viewNumber(k listKind) int {
	for i, v := range viewsInArea(areaOf(k)) {
		if v == k {
			return i + 1
		}
	}
	return 0
}

// allViews flattens every registered view in area/number order. Used to seed and
// reset the per-view workspaces.
func allViews() []listKind {
	var out []listKind
	for _, a := range areas {
		out = append(out, a.views...)
	}
	return out
}

// switcherRow is one selectable entry in the 0 area/view switcher overlay.
type switcherRow struct {
	area  areaKind
	view  listKind
	label string // "<area> › <view>", e.g. "load balancers › listeners"
}

// switcherRows builds the full switcher list from the registered areas.
func switcherRows() []switcherRow {
	var rows []switcherRow
	for _, a := range areas {
		for _, v := range a.views {
			rows = append(rows, switcherRow{area: a.kind, view: v, label: a.label + " › " + v.rootLabel()})
		}
	}
	return rows
}
