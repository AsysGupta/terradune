package ingest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Workspace is one initialized Terraform working directory.
type Workspace struct {
	Name string // display name: path relative to the scan root
	Dir  string // absolute path
}

// Discover finds every initialized Terraform workspace at or below root.
// Initialization is the signal that separates a workspace root from a
// directory that merely holds module source: only the former has .terraform.
func Discover(root string) ([]Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	var found []Workspace
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // unreadable dirs are skipped, not fatal
		}
		switch d.Name() {
		case ".git", ".terraform":
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, ".terraform")); err != nil {
			return nil
		}
		name, rerr := filepath.Rel(abs, path)
		if rerr != nil || name == "." {
			name = filepath.Base(path)
		}
		found = append(found, Workspace{Name: name, Dir: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no initialized Terraform workspace found under %s — run `terraform init` there first", abs)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// Owner returns the workspace containing path — the one whose directory is
// the longest prefix, so an edit inside a module belongs to the workspace
// that uses it. Reports false when no workspace owns the path.
func Owner(workspaces []Workspace, path string) (Workspace, bool) {
	var best Workspace
	found := false
	for _, ws := range workspaces {
		if path == ws.Dir || strings.HasPrefix(path, ws.Dir+string(filepath.Separator)) {
			if !found || len(ws.Dir) > len(best.Dir) {
				best, found = ws, true
			}
		}
	}
	return best, found
}
