package graph

import (
	"regexp"
	"strings"
)

// Terraform's own dependency graph (`terraform graph -plan`) knows things the
// plan's configuration block cannot show: locals are absent from plan JSON
// entirely, so any wiring that passes through a local is invisible to
// reference resolution. Parsing that graph recovers those dependencies.

// Node labels are quoted, and any quote inside one — every string key of a
// for_each, such as module.rt[\"web-az1\"] — arrives backslash-escaped, so
// the pattern has to span escapes rather than stop at the first quote.
var dotEdgeRe = regexp.MustCompile(`"\[root\] ((?:[^"\\]|\\.)*)" -> "\[root\] ((?:[^"\\]|\\.)*)"`)

var dotUnescape = strings.NewReplacer(`\"`, `"`, `\\`, `\`)

// isModuleContainer reports whether an address names a module itself rather
// than something inside it.
func isModuleContainer(addr string) bool {
	rest := addr
	for strings.HasPrefix(rest, "module.") {
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) < 3 {
			return true // ran out of segments: only a module path
		}
		rest = parts[2]
	}
	return false
}

// normalizeDOTNode turns a graph node label into a configuration address:
// "aws_subnet.a (expand)" and `aws_subnet.a["x"]` both become "aws_subnet.a".
//
// Module container nodes keep their suffix, because the two halves mean
// opposite things: a module's "(expand)" node carries the arguments passed
// into it, while its "(close)" node depends on everything the module
// created. Merging them would route every resource in a module to every
// other one.
func normalizeDOTNode(name string) string {
	suffix := ""
	if i := strings.Index(name, " ("); i >= 0 {
		suffix = name[i:]
		name = name[:i]
	}
	base := stripIndexes(strings.TrimSpace(name))
	if isModuleContainer(base) {
		return base + suffix
	}
	return base
}

// dotNodeKind classifies a normalized node. Only pass and resource nodes take
// part in the walk: provider, output and module container nodes carry
// ordering rather than data flow, and walking them would invent
// relationships between resources that merely share a module.
type dotNodeKind int

const (
	dotSkip dotNodeKind = iota
	dotResource
	dotPass // var, local, data — traversed through, never drawn
)

func classifyDOTNode(name string, isResource func(string) bool) dotNodeKind {
	switch {
	case name == "" || name == "root":
		return dotSkip
	case strings.HasPrefix(name, "provider"),
		strings.HasPrefix(name, "meta."),
		strings.HasPrefix(name, "provisioner"):
		return dotSkip
	}
	// A module's own node: walk the arguments going in, ignore the close.
	if strings.HasSuffix(name, " (expand)") && isModuleContainer(strings.TrimSuffix(name, " (expand)")) {
		return dotPass
	}
	if strings.Contains(name, " (") {
		return dotSkip // any other module container node, notably (close)
	}
	// Strip leading module segments to see what the node actually names.
	rest := name
	for strings.HasPrefix(rest, "module.") {
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) < 3 {
			return dotSkip
		}
		rest = parts[2]
	}
	switch {
	case isResource(name):
		return dotResource
	case strings.HasPrefix(rest, "var."), strings.HasPrefix(rest, "local."),
		strings.HasPrefix(rest, "data."), strings.HasPrefix(rest, "output."):
		// Module outputs carry cross-module wiring: a child's var points at
		// the producing module's output. Walking is one-directional and
		// starts at resources, so this never turns an output into a target.
		return dotPass
	}
	return dotSkip
}

// DependenciesFromDOT walks Terraform's graph and returns, for each managed
// resource configuration address, the resource addresses it depends on —
// following paths that run through variables, locals and data sources.
func DependenciesFromDOT(dot []byte, isResource func(string) bool) map[string]map[string]bool {
	adj := map[string][]string{}
	kind := map[string]dotNodeKind{}

	note := func(name string) dotNodeKind {
		if k, ok := kind[name]; ok {
			return k
		}
		k := classifyDOTNode(name, isResource)
		kind[name] = k
		return k
	}

	for _, m := range dotEdgeRe.FindAllSubmatch(dot, -1) {
		from := normalizeDOTNode(dotUnescape.Replace(string(m[1])))
		to := normalizeDOTNode(dotUnescape.Replace(string(m[2])))
		if from == to {
			continue // an instance pointing at its own config node
		}
		if note(from) == dotSkip || note(to) == dotSkip {
			continue
		}
		adj[from] = append(adj[from], to)
	}

	deps := map[string]map[string]bool{}
	for node, k := range kind {
		if k != dotResource {
			continue
		}
		reached := map[string]bool{}
		seen := map[string]bool{node: true}
		queue := append([]string{}, adj[node]...)
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			if seen[next] {
				continue
			}
			seen[next] = true
			if kind[next] == dotResource {
				reached[next] = true
				continue // stop at the first resource on each path
			}
			queue = append(queue, adj[next]...)
		}
		if len(reached) > 0 {
			deps[node] = reached
		}
	}
	return deps
}

var (
	allKeysRe      = regexp.MustCompile(`\[([^\]]+)\]`)
	digitsRe       = regexp.MustCompile(`^\d+$`)
	modulePrefixRe = regexp.MustCompile(`^((?:module\.[^.\[]+(?:\[[^\]]*\])?\.)*)`)
)

// modulePath returns the module instance an address lives in, keys included:
// module.rt["web"].aws_route_table.rt[0] -> module.rt["web"]. The root module
// is the empty string.
func modulePath(addr string) string {
	return strings.TrimSuffix(modulePrefixRe.FindString(addr), ".")
}

// namedKeys returns an address's for_each keys, ignoring positional indexes.
// module.rt["web-az1"].aws_route_table.rt[0] yields {"web-az1"}: the string
// key identifies which copy this is, while [0] is shared by every copy and so
// tells instances apart from nothing.
func namedKeys(addr string) map[string]bool {
	out := map[string]bool{}
	for _, m := range allKeysRe.FindAllStringSubmatch(addr, -1) {
		k := strings.Trim(m[1], `"`)
		if !digitsRe.MatchString(k) {
			out[k] = true
		}
	}
	return out
}

// applyDOTDeps adds instance edges for configuration pairs the reference
// resolver could not connect.
//
// Terraform's graph does not expand modules, so a dependency between two
// expanded resources is known only at configuration level. An edge is drawn
// only where the instance is not in doubt: the copy inside the same module
// instance, or a resource that has just one. Everything else is left out
// rather than inferred, because a drawn edge reads as a fact.
func (r *resolver) applyDOTDeps(deps map[string]map[string]bool) {
	for fromCfg, tos := range deps {
		fromInstances := r.instances[fromCfg]
		if len(fromInstances) == 0 {
			continue
		}
		for toCfg := range tos {
			if r.cfgPairs[fromCfg+"|"+toCfg] {
				continue // the resolver already connected these precisely
			}
			toInstances := r.instances[toCfg]
			if len(toInstances) == 0 {
				continue
			}
			byModule := map[string][]string{}
			for _, to := range toInstances {
				byModule[modulePath(to)] = append(byModule[modulePath(to)], to)
			}
			for _, from := range fromInstances {
				// A reference inside a module instance can only mean that
				// instance's own copy, so one candidate there is certain.
				if own := byModule[modulePath(from)]; len(own) == 1 {
					r.addEdge(from, own[0])
					continue
				}
				// Otherwise only a lone instance overall is certain; with
				// several, the plan does not say which, and a guess would
				// read as fact.
				if len(toInstances) == 1 {
					r.addEdge(from, toInstances[0])
				}
			}
		}
	}
}
