// Package graph builds terradune's dependency graph from a Terraform plan:
// resource nodes with their planned status, dependency edges derived from
// configuration references, and module containment.
package graph

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/AsysGupta/terradune/internal/ingest"
)

// Node is one resource instance in the diagram.
type Node struct {
	ID     string        `json:"id"`   // full instance address, e.g. module.vpc.aws_subnet.a[0]
	Type   string        `json:"type"` // e.g. aws_subnet
	Name   string        `json:"name"`
	Module string        `json:"module"` // containing module address, "" for root
	Status ingest.Status `json:"status"`
	// Meta carries a few attribute values the UI lays out by (az, cidr,
	// name tag, and ids for matching resources that lack config edges).
	Meta map[string]string `json:"meta,omitempty"`
}

// Edge means From depends on To.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is what the diagram renders.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

var (
	indexRe         = regexp.MustCompile(`\[[^\]]*\]`)
	trailingIndexRe = regexp.MustCompile(`\[([^\]]+)\]$`)
)

// instanceKey returns the instance's own index — "0" for aws_eip.nat[0],
// `"a"` for aws_foo.b["a"] — or "" for a single-instance resource.
func instanceKey(addr string) string {
	m := trailingIndexRe.FindStringSubmatch(addr)
	if m == nil {
		return ""
	}
	return m[1]
}

// stripIndexes turns module.x["a"].aws_foo.bar[0] into module.x.aws_foo.bar,
// the form used by config addresses.
func stripIndexes(addr string) string {
	return indexRe.ReplaceAllString(addr, "")
}

// Build derives the graph from a plan. Only managed resources become nodes;
// data sources and variables are followed as reference paths but not drawn.
func Build(plan *tfjson.Plan) *Graph {
	return BuildWithDOT(plan, nil)
}

// BuildWithDOT additionally consults Terraform's own dependency graph, which
// resolves the wiring that runs through locals — invisible in plan JSON.
func BuildWithDOT(plan *tfjson.Plan, dot []byte) *Graph {
	g := &Graph{}

	values := map[string]map[string]string{}
	if plan.PriorState != nil && plan.PriorState.Values != nil {
		collectValues(plan.PriorState.Values.RootModule, values)
	}
	if plan.PlannedValues != nil {
		collectValues(plan.PlannedValues.RootModule, values)
	}

	// Config address -> all live instance addresses of that resource.
	instances := map[string][]string{}
	for _, rc := range plan.ResourceChanges {
		if rc.Mode != tfjson.ManagedResourceMode {
			continue
		}
		g.Nodes = append(g.Nodes, Node{
			ID:     rc.Address,
			Type:   rc.Type,
			Name:   rc.Name,
			Module: rc.ModuleAddress,
			Status: statusOf(rc.Change.Actions),
			Meta:   values[rc.Address],
		})
		cfgAddr := joinAddr(stripIndexes(rc.ModuleAddress), rc.Type+"."+rc.Name)
		instances[cfgAddr] = append(instances[cfgAddr], rc.Address)
	}

	if plan.Config != nil && plan.Config.RootModule != nil {
		r := &resolver{
			modules:   map[string]*tfjson.ConfigModule{},
			parents:   map[string]parentRef{},
			data:      map[string]*tfjson.ConfigResource{},
			instances: instances,
			seen:      map[Edge]bool{},
			cfgPairs:  map[string]bool{},
			graph:     g,
		}
		r.indexModules(plan.Config.RootModule, "")
		r.walkModule(plan.Config.RootModule, "")
		if len(dot) > 0 {
			isResource := func(addr string) bool { _, ok := instances[addr]; return ok }
			r.applyDOTDeps(DependenciesFromDOT(dot, isResource))
		}
	}

	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return g
}

// metaKeys maps attribute names to the shorter keys sent to the UI.
var metaKeys = map[string]string{
	"id":                 "id",
	"availability_zone":  "az",
	"cidr_block":         "cidr",
	"vpc_id":             "vpc_id",
	"subnet_id":          "subnet_id",
	"instance_type":      "spec",
	"load_balancer_type": "spec",
}

func collectValues(mod *tfjson.StateModule, out map[string]map[string]string) {
	if mod == nil {
		return
	}
	for _, res := range mod.Resources {
		m := map[string]string{}
		for attr, key := range metaKeys {
			if v, ok := res.AttributeValues[attr].(string); ok && v != "" {
				m[key] = v
			}
		}
		if tags, ok := res.AttributeValues["tags"].(map[string]interface{}); ok {
			if name, ok := tags["Name"].(string); ok && name != "" {
				m["name"] = name
			}
		}
		if len(m) > 0 {
			out[res.Address] = m
		}
	}
	for _, child := range mod.ChildModules {
		collectValues(child, out)
	}
}

func statusOf(actions tfjson.Actions) ingest.Status {
	switch {
	case actions.Replace():
		return ingest.StatusReplace
	case actions.Create():
		return ingest.StatusCreate
	case actions.Delete():
		return ingest.StatusDestroy
	case actions.Update():
		return ingest.StatusUpdate
	default:
		return ingest.StatusExisting
	}
}

func joinAddr(module, rest string) string {
	if module == "" {
		return rest
	}
	return module + "." + rest
}

// resolver walks the configuration turning references into edges, following
// module outputs through to the resources behind them.
type resolver struct {
	modules   map[string]*tfjson.ConfigModule   // config module address -> module
	parents   map[string]parentRef              // module address -> where it was called from
	data      map[string]*tfjson.ConfigResource // data source config address -> config
	instances map[string][]string               // resource config address -> instance addresses
	seen      map[Edge]bool
	cfgPairs  map[string]bool // config pairs already connected precisely
	graph     *Graph
}

func (r *resolver) addEdge(from, to string) {
	e := Edge{From: from, To: to}
	if from == to || r.seen[e] {
		return
	}
	r.seen[e] = true
	r.graph.Edges = append(r.graph.Edges, e)
}

// parentRef is where a module was called from, so a var reference inside it
// can be traced back to the argument the caller passed.
type parentRef struct {
	addr string // calling module's address
	call string // name of the module call
}

func (r *resolver) indexModules(mod *tfjson.ConfigModule, moduleAddr string) {
	r.modules[moduleAddr] = mod
	for _, res := range mod.Resources {
		if res.Mode == tfjson.DataResourceMode {
			r.data[joinAddr(moduleAddr, "data."+res.Type+"."+res.Name)] = res
		}
	}
	for name, call := range mod.ModuleCalls {
		if call.Module != nil {
			childAddr := joinAddr(moduleAddr, "module."+name)
			r.parents[childAddr] = parentRef{addr: moduleAddr, call: name}
			r.indexModules(call.Module, childAddr)
		}
	}
}

func (r *resolver) walkModule(mod *tfjson.ConfigModule, moduleAddr string) {
	for _, res := range mod.Resources {
		if res.Mode != tfjson.ManagedResourceMode {
			continue
		}
		fromCfg := joinAddr(moduleAddr, res.Type+"."+res.Name)

		// Each expression is resolved separately: when count.index/each.key
		// appears next to a resource reference in the same expression, the
		// edge pairs instances index-to-index instead of fanning out to all.
		type refGroup struct {
			refs   map[string]bool
			paired bool
		}
		var groups []refGroup
		for _, expr := range res.Expressions {
			refs := map[string]bool{}
			collectRefs(expr, refs)
			if len(refs) == 0 {
				continue
			}
			groups = append(groups, refGroup{
				refs:   refs,
				paired: refs["count.index"] || refs["each.key"] || refs["each.value"],
			})
		}
		if len(res.DependsOn) > 0 {
			refs := map[string]bool{}
			for _, dep := range res.DependsOn {
				refs[dep] = true
			}
			groups = append(groups, refGroup{refs: refs}) // depends_on is resource-level: full fan-out
		}

		for _, grp := range groups {
			t := &targets{cfg: map[string]bool{}, pinned: map[string]map[string]bool{}}
			for ref := range grp.refs {
				r.resolveRef(ref, moduleAddr, t, map[string]bool{})
			}
			for toCfg := range t.cfg {
				pins := t.pinned[toCfg]
				for _, from := range r.instances[fromCfg] {
					for _, to := range r.instances[toCfg] {
						switch {
						case len(pins) > 0:
							// A literal index (aws_subnet.web[0]) names one
							// instance; it wins over any index pairing.
							if !pins[instanceKey(to)] {
								continue
							}
						case grp.paired:
							fk, tk := instanceKey(from), instanceKey(to)
							if fk != "" && tk != "" && fk != tk {
								continue
							}
						}
						r.addEdge(from, to)
						r.cfgPairs[fromCfg+"|"+toCfg] = true
					}
				}
			}
		}
	}
	for name, call := range mod.ModuleCalls {
		if call.Module != nil {
			r.walkModule(call.Module, joinAddr(moduleAddr, "module."+name))
		}
	}
}

// targets collects what an expression refers to: resource config addresses,
// plus — when a reference carries a literal index like aws_subnet.web[0] —
// the specific instance keys it names.
type targets struct {
	cfg    map[string]bool
	pinned map[string]map[string]bool // config address -> instance keys
}

// resolveRef maps a config reference (as seen from moduleAddr) into t.
// Direct resource references resolve immediately; module output references
// are followed into the child module's output expression, recursively.
// Variables, locals, and data sources are not followed yet.
func (r *resolver) resolveRef(ref string, moduleAddr string, t *targets, visiting map[string]bool) {
	parts := strings.Split(ref, ".")
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case "local", "each", "count", "path", "terraform", "self":
		// Locals are absent from plan JSON entirely, so chains through them
		// cannot be followed; the rest carry no resource dependency.
		return
	case "var":
		// A variable inside a module is whatever its caller passed, so follow
		// the reference up into the module call's argument.
		p, ok := r.parents[moduleAddr]
		if !ok {
			return // a root variable: supplied by the user, not by a resource
		}
		key := "var|" + moduleAddr + "|" + parts[1]
		if visiting[key] {
			return
		}
		visiting[key] = true
		parentMod, ok := r.modules[p.addr]
		if !ok {
			return
		}
		call, ok := parentMod.ModuleCalls[p.call]
		if !ok || call.Expressions == nil {
			return
		}
		arg, ok := call.Expressions[parts[1]]
		if !ok {
			return
		}
		argRefs := map[string]bool{}
		collectRefs(arg, argRefs)
		for argRef := range argRefs {
			r.resolveRef(argRef, p.addr, t, visiting)
		}
		return
	case "data":
		// Data sources are not drawn, but resources reached through them are
		// genuinely related, so resolve onward through the query's own inputs.
		if len(parts) < 3 {
			return
		}
		cfg := joinAddr(moduleAddr, "data."+parts[1]+"."+stripIndexes(parts[2]))
		if visiting[cfg] {
			return
		}
		visiting[cfg] = true
		ds, ok := r.data[cfg]
		if !ok {
			return
		}
		dsRefs := map[string]bool{}
		for _, expr := range ds.Expressions {
			collectRefs(expr, dsRefs)
		}
		for dsRef := range dsRefs {
			r.resolveRef(dsRef, moduleAddr, t, visiting)
		}
		return
	case "module":
		if len(parts) < 3 {
			return
		}
		childAddr := joinAddr(moduleAddr, "module."+stripIndexes(parts[1]))
		outputName := parts[2]
		key := childAddr + "|" + outputName
		if visiting[key] {
			return
		}
		visiting[key] = true
		child, ok := r.modules[childAddr]
		if !ok || child.Outputs == nil {
			return
		}
		out, ok := child.Outputs[outputName]
		if !ok || out.Expression == nil {
			return
		}
		outRefs := map[string]bool{}
		collectRefs(out.Expression, outRefs)
		for outRef := range outRefs {
			r.resolveRef(outRef, childAddr, t, visiting)
		}
		return
	}
	cfg := joinAddr(moduleAddr, parts[0]+"."+stripIndexes(parts[1]))
	t.cfg[cfg] = true
	if key := instanceKey(parts[1]); key != "" {
		if t.pinned[cfg] == nil {
			t.pinned[cfg] = map[string]bool{}
		}
		t.pinned[cfg][key] = true
	}
}

func collectRefs(expr *tfjson.Expression, refs map[string]bool) {
	if expr == nil {
		return
	}
	for _, r := range expr.References {
		refs[r] = true
	}
	for _, block := range expr.NestedBlocks {
		for _, nested := range block {
			collectRefs(nested, refs)
		}
	}
}

// Print writes a human-readable view: nodes grouped by module, then edges.
func (g *Graph) Print(w io.Writer) {
	byModule := map[string][]Node{}
	var modules []string
	for _, n := range g.Nodes {
		if _, ok := byModule[n.Module]; !ok {
			modules = append(modules, n.Module)
		}
		byModule[n.Module] = append(byModule[n.Module], n)
	}
	sort.Strings(modules)
	for _, m := range modules {
		title := m
		if title == "" {
			title = "root"
		}
		fmt.Fprintf(w, "\n%s:\n", title)
		for _, n := range byModule[m] {
			fmt.Fprintf(w, "  %-9s %s\n", n.Status, n.ID)
		}
	}
	if len(g.Edges) > 0 {
		fmt.Fprintf(w, "\nDependencies (%d):\n", len(g.Edges))
		for _, e := range g.Edges {
			fmt.Fprintf(w, "  %s -> %s\n", e.From, e.To)
		}
	}
}
