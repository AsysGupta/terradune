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
	ID     string // full instance address, e.g. module.vpc.aws_subnet.a[0]
	Type   string // e.g. aws_subnet
	Name   string
	Module string // containing module address, "" for root
	Status ingest.Status
}

// Edge means From depends on To.
type Edge struct {
	From string
	To   string
}

// Graph is what the diagram renders.
type Graph struct {
	Nodes []Node
	Edges []Edge
}

var indexRe = regexp.MustCompile(`\[[^\]]*\]`)

// stripIndexes turns module.x["a"].aws_foo.bar[0] into module.x.aws_foo.bar,
// the form used by config addresses.
func stripIndexes(addr string) string {
	return indexRe.ReplaceAllString(addr, "")
}

// Build derives the graph from a plan. Only managed resources become nodes;
// data sources and variables are followed as reference paths but not drawn.
func Build(plan *tfjson.Plan) *Graph {
	g := &Graph{}

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
		})
		cfgAddr := joinAddr(stripIndexes(rc.ModuleAddress), rc.Type+"."+rc.Name)
		instances[cfgAddr] = append(instances[cfgAddr], rc.Address)
	}

	if plan.Config != nil && plan.Config.RootModule != nil {
		r := &resolver{
			modules:   map[string]*tfjson.ConfigModule{},
			instances: instances,
			seen:      map[Edge]bool{},
			graph:     g,
		}
		r.indexModules(plan.Config.RootModule, "")
		r.walkModule(plan.Config.RootModule, "")
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
	modules   map[string]*tfjson.ConfigModule // config module address -> module
	instances map[string][]string             // resource config address -> instance addresses
	seen      map[Edge]bool
	graph     *Graph
}

func (r *resolver) indexModules(mod *tfjson.ConfigModule, moduleAddr string) {
	r.modules[moduleAddr] = mod
	for name, call := range mod.ModuleCalls {
		if call.Module != nil {
			r.indexModules(call.Module, joinAddr(moduleAddr, "module."+name))
		}
	}
}

func (r *resolver) walkModule(mod *tfjson.ConfigModule, moduleAddr string) {
	for _, res := range mod.Resources {
		if res.Mode != tfjson.ManagedResourceMode {
			continue
		}
		fromCfg := joinAddr(moduleAddr, res.Type+"."+res.Name)
		refs := map[string]bool{}
		for _, expr := range res.Expressions {
			collectRefs(expr, refs)
		}
		for _, dep := range res.DependsOn {
			refs[dep] = true
		}
		targets := map[string]bool{}
		for ref := range refs {
			r.resolveRef(ref, moduleAddr, targets, map[string]bool{})
		}
		for toCfg := range targets {
			for _, from := range r.instances[fromCfg] {
				for _, to := range r.instances[toCfg] {
					e := Edge{From: from, To: to}
					if from != to && !r.seen[e] {
						r.seen[e] = true
						r.graph.Edges = append(r.graph.Edges, e)
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

// resolveRef maps a config reference (as seen from moduleAddr) to resource
// config addresses, adding them to targets. Direct resource references
// resolve immediately; module output references are followed into the child
// module's output expression, recursively. Variables, locals, and data
// sources are not followed yet.
func (r *resolver) resolveRef(ref string, moduleAddr string, targets map[string]bool, visiting map[string]bool) {
	parts := strings.Split(ref, ".")
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case "var", "local", "data", "each", "count", "path", "terraform", "self":
		return
	case "module":
		if len(parts) < 3 {
			return
		}
		childAddr := joinAddr(moduleAddr, "module."+parts[1])
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
			r.resolveRef(outRef, childAddr, targets, visiting)
		}
		return
	}
	targets[joinAddr(moduleAddr, parts[0]+"."+parts[1])] = true
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
