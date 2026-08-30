// Package ingest runs `terraform plan` against a workspace and turns the
// plan JSON into terradune's view of the world: what already exists and
// what the plan will create, change, or destroy.
package ingest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// Status describes what the plan intends for a single resource.
type Status string

const (
	StatusExisting Status = "existing" // in state, no changes
	StatusCreate   Status = "create"
	StatusUpdate   Status = "update"
	StatusDestroy  Status = "destroy"
	StatusReplace  Status = "replace"
)

// Resource is one resource instance from the plan.
type Resource struct {
	Address      string // full address, e.g. module.vpc.aws_subnet.private[0]
	Type         string // e.g. aws_subnet
	Name         string
	ProviderName string
	Status       Status
}

// Inventory is everything terradune knows after one plan.
type Inventory struct {
	TerraformVersion string
	Resources        []Resource
	Plan             *tfjson.Plan // the raw plan, for graph building
}

// Load runs plan+show in dir and returns the parsed inventory.
func Load(ctx context.Context, dir string) (*Inventory, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	if _, err := os.Stat(filepath.Join(abs, ".terraform")); err != nil {
		return nil, fmt.Errorf("workspace %s is not initialized — run `terraform init` there first", abs)
	}

	execPath, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fmt.Errorf("terraform binary not found in PATH: %w", err)
	}

	tf, err := tfexec.NewTerraform(abs, execPath)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "terradune-plan-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	planFile := filepath.Join(tmp, "tfplan")

	if _, err := tf.Plan(ctx, tfexec.Out(planFile)); err != nil {
		return nil, fmt.Errorf("terraform plan failed: %w", err)
	}

	plan, err := tf.ShowPlanFile(ctx, planFile)
	if err != nil {
		return nil, fmt.Errorf("terraform show failed: %w", err)
	}

	return fromPlan(plan), nil
}

func fromPlan(plan *tfjson.Plan) *Inventory {
	inv := &Inventory{TerraformVersion: plan.TerraformVersion, Plan: plan}
	for _, rc := range plan.ResourceChanges {
		if rc.Mode != tfjson.ManagedResourceMode {
			continue // skip data sources for now
		}
		inv.Resources = append(inv.Resources, Resource{
			Address:      rc.Address,
			Type:         rc.Type,
			Name:         rc.Name,
			ProviderName: rc.ProviderName,
			Status:       statusOf(rc.Change.Actions),
		})
	}
	sort.Slice(inv.Resources, func(i, j int) bool {
		return inv.Resources[i].Address < inv.Resources[j].Address
	})
	return inv
}

func statusOf(actions tfjson.Actions) Status {
	switch {
	case actions.Replace():
		return StatusReplace
	case actions.Create():
		return StatusCreate
	case actions.Delete():
		return StatusDestroy
	case actions.Update():
		return StatusUpdate
	default:
		return StatusExisting
	}
}

// PrintSummary writes a human-readable inventory, grouped by status.
func (inv *Inventory) PrintSummary(w io.Writer) {
	groups := []struct {
		status Status
		title  string
	}{
		{StatusExisting, "Already exists (unchanged)"},
		{StatusUpdate, "Will change"},
		{StatusReplace, "Will be replaced"},
		{StatusCreate, "Will be created"},
		{StatusDestroy, "Will be destroyed"},
	}
	fmt.Fprintf(w, "Terraform %s — %d resources\n", inv.TerraformVersion, len(inv.Resources))
	for _, g := range groups {
		var members []Resource
		for _, r := range inv.Resources {
			if r.Status == g.status {
				members = append(members, r)
			}
		}
		if len(members) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s (%d):\n", g.title, len(members))
		for _, r := range members {
			fmt.Fprintf(w, "  %s\n", r.Address)
		}
	}
}
