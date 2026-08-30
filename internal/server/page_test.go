package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/AsysGupta/terradune/internal/graph"
)

// jscPaths are where JavaScriptCore's shell lives on macOS. The page is
// mostly logic — layout derivation, markup building — so running it headlessly
// catches real errors (a runaway recursion, a broken template) that would
// otherwise only show up in a browser.
var jscPaths = []string{
	"/System/Library/Frameworks/JavaScriptCore.framework/Versions/Current/Helpers/jsc",
	"/System/Library/Frameworks/JavaScriptCore.framework/Versions/A/Helpers/jsc",
}

func findJSC() string {
	for _, p := range jscPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("jsc"); err == nil {
		return p
	}
	return ""
}

var scriptRe = regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)

// inlineScript returns the page's own script, skipping the tags that only
// pull in a vendored library.
func inlineScript(page []byte) []byte {
	var last []byte
	for _, m := range scriptRe.FindAllSubmatch(page, -1) {
		if strings.Contains(string(m[1]), "src=") {
			continue
		}
		last = m[2]
	}
	return last
}

// stateFromFixtures builds the same payload the browser receives, from the
// plan fixtures the graph tests already use.
func stateFromFixtures(t *testing.T) []byte {
	t.Helper()
	state := State{Root: "/examples"}
	for _, f := range []struct{ name, path string }{
		{"ec2", "ec2_plan.json"},
		{"modular", "modular_plan.json"},
		{"vpc", "vpc_plan.json"},
	} {
		raw, err := os.ReadFile(filepath.Join("..", "graph", "testdata", f.path))
		if err != nil {
			t.Fatalf("reading fixture %s: %v", f.path, err)
		}
		var plan tfjson.Plan
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatalf("parsing fixture %s: %v", f.path, err)
		}
		g := graph.Build(&plan)
		state.Workspaces = append(state.Workspaces, Workspace{
			Name: f.name, Dir: "/examples/" + f.name,
			TerraformVersion: plan.TerraformVersion,
			Nodes:            g.Nodes, Edges: g.Edges,
		})
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestPageRendersHeadlessly(t *testing.T) {
	jsc := findJSC()
	if jsc == "" {
		t.Skip("no JavaScriptCore shell available")
	}

	page, err := static.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	script := inlineScript(page)
	if len(script) == 0 {
		t.Fatal("no inline script found in index.html")
	}

	stub, err := os.ReadFile(filepath.Join("testdata", "dom-stub.js"))
	if err != nil {
		t.Fatal(err)
	}
	checks, err := os.ReadFile(filepath.Join("testdata", "checks.js"))
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.Write(stub)
	b.WriteString("\nvar STATE = ")
	b.Write(stateFromFixtures(t))
	b.WriteString(";\n")
	b.Write(script)
	b.WriteString("\n")
	b.Write(checks)

	bundle := filepath.Join(t.TempDir(), "page.js")
	if err := os.WriteFile(bundle, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(jsc, bundle).CombinedOutput()
	t.Logf("headless page checks:\n%s", out)
	if err != nil {
		t.Fatalf("jsc failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL CHECKS PASSED") {
		t.Fatal("headless page checks did not pass")
	}
}
