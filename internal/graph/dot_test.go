package graph

import (
	"strings"
	"testing"
)

// A slice of the shape terraform emits: string for_each keys arrive with
// their quotes backslash-escaped, wiring runs through locals and module
// outputs, and each module has both an (expand) and a (close) node.
const sampleDOT = `
digraph {
	compound = "true"
	subgraph "root" {
		"[root] aws_vpc.main (expand)" -> "[root] provider[\"registry.terraform.io/hashicorp/aws\"]"
		"[root] aws_vpc.main[0]" -> "[root] aws_vpc.main (expand)"
		"[root] module.rt.aws_route_table.rt (expand)" -> "[root] module.rt.var.vpc_id (expand)"
		"[root] module.rt.var.vpc_id (expand)" -> "[root] module.rt (expand)"
		"[root] module.rt (expand)" -> "[root] local.wiring (expand)"
		"[root] local.wiring (expand)" -> "[root] module.net.output.subnets (expand)"
		"[root] module.net.output.subnets (expand)" -> "[root] module.net.aws_subnet.s (expand)"
		"[root] module.net.aws_subnet.s (expand)" -> "[root] aws_vpc.main[0]"
		"[root] module.rt (close)" -> "[root] module.rt.aws_route_table.rt (expand)"
		"[root] module.other.aws_thing.t (expand)" -> "[root] module.rt (close)"
	}
}
`

func TestDependenciesFromDOTFollowsLocalsAndOutputs(t *testing.T) {
	resources := map[string]bool{
		"aws_vpc.main":                 true,
		"module.rt.aws_route_table.rt": true,
		"module.net.aws_subnet.s":      true,
		"module.other.aws_thing.t":     true,
	}
	deps := DependenciesFromDOT([]byte(sampleDOT), func(a string) bool { return resources[a] })

	// The route table reaches the subnet only through var -> module expand ->
	// local -> module output, every hop of which must be walked.
	if !deps["module.rt.aws_route_table.rt"]["module.net.aws_subnet.s"] {
		t.Errorf("route table did not reach subnet through locals; got %v",
			deps["module.rt.aws_route_table.rt"])
	}
	// Walking stops at the first resource, so the VPC behind the subnet is
	// the subnet's dependency rather than the route table's.
	if deps["module.rt.aws_route_table.rt"]["aws_vpc.main"] {
		t.Error("route table gained a transitive edge to the VPC")
	}
	if !deps["module.net.aws_subnet.s"]["aws_vpc.main"] {
		t.Error("subnet did not reach the VPC")
	}
	// A module's (close) node depends on everything the module made; walking
	// it would invent a dependency on an unrelated module's resources.
	if deps["module.other.aws_thing.t"]["module.rt.aws_route_table.rt"] {
		t.Error("walked a module (close) node and invented a dependency")
	}
}

func TestNormalizeDOTNode(t *testing.T) {
	cases := map[string]string{
		`aws_subnet.a (expand)`:      "aws_subnet.a",
		`aws_subnet.a["web"]`:        "aws_subnet.a",
		`module.rt["x"].aws_route.r`: "module.rt.aws_route.r",
		`module.rt (expand)`:         "module.rt (expand)", // containers keep their half
		`module.rt (close)`:          "module.rt (close)",
	}
	for in, want := range cases {
		if got := normalizeDOTNode(in); got != want {
			t.Errorf("normalizeDOTNode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModulePath(t *testing.T) {
	cases := map[string]string{
		`module.rt["web"].aws_route_table.rt[0]`: `module.rt["web"]`,
		`module.a.module.b["k"].aws_x.y`:         `module.a.module.b["k"]`,
		`aws_vpc.main`:                           "",
		`aws_eip.n[1]`:                           "",
	}
	for in, want := range cases {
		if got := modulePath(in); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyDOTDepsLeavesAmbiguousSpansAlone(t *testing.T) {
	// One load balancer whose subnets are chosen inside a local, so the only
	// thing the plan records is a dependency on the subnet resource itself.
	subnets := []string{
		`module.vpc.aws_subnet.s["a"]`, `module.vpc.aws_subnet.s["b"]`,
		`module.vpc.aws_subnet.s["c"]`,
	}
	r := &resolver{
		instances: map[string][]string{
			"module.gwlb.aws_lb.lb":    {"module.gwlb.aws_lb.lb[0]"},
			"module.vpc.aws_subnet.s":  subnets,
			"aws_internet_gateway.igw": {"aws_internet_gateway.igw[0]"},
		},
		seen:     map[Edge]bool{},
		cfgPairs: map[string]bool{},
		graph:    &Graph{},
	}
	r.applyDOTDeps(map[string]map[string]bool{
		"module.gwlb.aws_lb.lb": {
			"module.vpc.aws_subnet.s":  true, // ambiguous: which of the three?
			"aws_internet_gateway.igw": true, // unambiguous: there is only one
		},
	})

	for _, e := range r.graph.Edges {
		if strings.Contains(e.To, "aws_subnet") {
			t.Errorf("guessed a subnet for a load balancer: %s -> %s", e.From, e.To)
		}
	}
	if len(r.graph.Edges) != 1 || r.graph.Edges[0].To != "aws_internet_gateway.igw[0]" {
		t.Errorf("expected only the single-target edge, got %+v", r.graph.Edges)
	}
}

func TestApplyDOTDepsPairsWithinAModuleInstance(t *testing.T) {
	r := &resolver{
		instances: map[string][]string{
			`module.rt.aws_route_table_association.a`: {
				`module.rt["x"].aws_route_table_association.a[0]`,
				`module.rt["y"].aws_route_table_association.a[0]`,
			},
			`module.rt.aws_route_table.rt`: {
				`module.rt["x"].aws_route_table.rt[0]`,
				`module.rt["y"].aws_route_table.rt[0]`,
			},
		},
		seen:     map[Edge]bool{},
		cfgPairs: map[string]bool{},
		graph:    &Graph{},
	}
	r.applyDOTDeps(map[string]map[string]bool{
		`module.rt.aws_route_table_association.a`: {`module.rt.aws_route_table.rt`: true},
	})
	if len(r.graph.Edges) != 2 {
		t.Fatalf("want 2 paired edges, got %d: %+v", len(r.graph.Edges), r.graph.Edges)
	}
	for _, e := range r.graph.Edges {
		if modulePath(e.From) != modulePath(e.To) {
			t.Errorf("edge crosses module instances: %s -> %s", e.From, e.To)
		}
	}
}

func TestIsModuleContainer(t *testing.T) {
	for addr, want := range map[string]bool{
		"module.rt":             true,
		"module.a.module.b":     true,
		"module.rt.aws_route.r": false,
		"aws_vpc.main":          false,
		"module.rt.var.vpc_id":  false,
	} {
		if got := isModuleContainer(addr); got != want {
			t.Errorf("isModuleContainer(%q) = %v, want %v", addr, got, want)
		}
	}
}

// Two counted resources in the same module carry no evidence of which copy
// pairs with which, so nothing is drawn rather than pairing them by position.
func TestApplyDOTDepsDoesNotPairByPosition(t *testing.T) {
	r := &resolver{
		instances: map[string][]string{
			"aws_nat_gateway.g": {"aws_nat_gateway.g[0]", "aws_nat_gateway.g[1]"},
			"aws_eip.n":         {"aws_eip.n[0]", "aws_eip.n[1]"},
		},
		seen:     map[Edge]bool{},
		cfgPairs: map[string]bool{},
		graph:    &Graph{},
	}
	r.applyDOTDeps(map[string]map[string]bool{
		"aws_nat_gateway.g": {"aws_eip.n": true},
	})
	if len(r.graph.Edges) != 0 {
		t.Errorf("paired counted instances by position: %+v", r.graph.Edges)
	}
}
