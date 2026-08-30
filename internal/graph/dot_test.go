package graph

import (
	"sort"
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

func TestCorrespondsIgnoresPositionalIndexes(t *testing.T) {
	// Every copy of a counted resource carries [0], so it cannot tell them
	// apart; the for_each key can.
	a := `module.rt["web-az1"].aws_route_table.rt[0]`
	same := `module.rt["web-az1"].aws_route_table_association.a[0]`
	other := `module.rt["db-az2"].aws_route_table_association.a[0]`
	if !corresponds(a, same) {
		t.Error("instances sharing a for_each key should correspond")
	}
	if corresponds(a, other) {
		t.Error("instances of different for_each keys must not correspond")
	}
	if !corresponds("aws_eip.n[1]", "aws_nat_gateway.g[1]") {
		t.Error("unkeyed instances should correspond by position")
	}
	if corresponds("aws_eip.n[0]", "aws_nat_gateway.g[1]") {
		t.Error("different positions must not correspond")
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

func TestApplyDOTDepsPairsByKey(t *testing.T) {
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
		if namedKeys(e.From)["x"] != namedKeys(e.To)["x"] {
			t.Errorf("edge crosses for_each keys: %s -> %s", e.From, e.To)
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

func TestNamedKeysSorted(t *testing.T) {
	got := []string{}
	for k := range namedKeys(`module.rt["web-az1"].aws_route_table.rt[0]`) {
		got = append(got, k)
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "web-az1" {
		t.Errorf("namedKeys = %v, want [web-az1]", got)
	}
}
