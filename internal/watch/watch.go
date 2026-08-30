// Package watch triggers rebuilds when Terraform source files change.
package watch

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounce = 400 * time.Millisecond

func isRelevant(path string) bool {
	switch filepath.Ext(path) {
	case ".tf", ".tfvars":
		return true
	}
	return strings.HasSuffix(path, ".tf.json")
}

func skipDir(name string) bool {
	return name == ".terraform" || name == ".git"
}

// Watch observes *.tf/*.tfvars files under root (recursively) and calls
// onChange after edits settle for a debounce interval. Blocks until ctx is
// cancelled.
func Watch(ctx context.Context, root string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	addTree := func(dir string) {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDir(d.Name()) {
					return filepath.SkipDir
				}
				w.Add(path)
			}
			return nil
		})
	}
	addTree(root)

	var timer *time.Timer
	fire := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, onChange)
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			// A new directory may bring .tf files with it.
			if ev.Op.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() && !skipDir(filepath.Base(ev.Name)) {
					addTree(ev.Name)
				}
			}
			if isRelevant(ev.Name) {
				fire()
			}
		case <-w.Errors:
			// Non-fatal; keep watching.
		}
	}
}
