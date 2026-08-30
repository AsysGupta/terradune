package graph

import (
	"encoding/json"
	"os"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/AsysGupta/terradune/internal/ingest"
)

func loadFixture(t *testing.T, path string) *tfjson.Plan {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan tfjson.Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	return &plan
}

func TestBuildModularPlan(t *testing.T) {
	g := Build(loadFixture(t, "testdata/modular_plan.json"))

	wantNodes := map[string]struct {
		module string
		status ingest.Status
	}{
		"local_file.roster":               {"", ingest.StatusCreate},
		"module.pets.local_file.note":     {"module.pets", ingest.StatusCreate},
		"module.pets.random_pet.these[0]": {"module.pets", ingest.StatusCreate},
		"module.pets.random_pet.these[1]": {"module.pets", ingest.StatusCreate},
	}
	if len(g.Nodes) != len(wantNodes) {
		t.Fatalf("got %d nodes, want %d: %+v", len(g.Nodes), len(wantNodes), g.Nodes)
	}
	for _, n := range g.Nodes {
		want, ok := wantNodes[n.ID]
		if !ok {
			t.Errorf("unexpected node %s", n.ID)
			continue
		}
		if n.Module != want.module || n.Status != want.status {
			t.Errorf("node %s: got (module=%q, status=%s), want (%q, %s)",
				n.ID, n.Module, n.Status, want.module, want.status)
		}
	}

	wantEdges := map[Edge]bool{
		// direct in-module references
		{From: "module.pets.local_file.note", To: "module.pets.random_pet.these[0]"}: true,
		{From: "module.pets.local_file.note", To: "module.pets.random_pet.these[1]"}: true,
		// resolved through the module output "names"
		{From: "local_file.roster", To: "module.pets.random_pet.these[0]"}: true,
		{From: "local_file.roster", To: "module.pets.random_pet.these[1]"}: true,
	}
	if len(g.Edges) != len(wantEdges) {
		t.Fatalf("got %d edges, want %d: %+v", len(g.Edges), len(wantEdges), g.Edges)
	}
	for _, e := range g.Edges {
		if !wantEdges[e] {
			t.Errorf("unexpected edge %s -> %s", e.From, e.To)
		}
	}
}

func TestStripIndexes(t *testing.T) {
	cases := map[string]string{
		`module.x["a"].aws_foo.bar[0]`: "module.x.aws_foo.bar",
		`aws_foo.bar`:                  "aws_foo.bar",
		`module.x[1].module.y.a.b[2]`:  "module.x.module.y.a.b",
	}
	for in, want := range cases {
		if got := stripIndexes(in); got != want {
			t.Errorf("stripIndexes(%q) = %q, want %q", in, got, want)
		}
	}
}
