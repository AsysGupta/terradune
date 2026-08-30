// Package watch triggers rebuilds when Terraform source files change.
package watch

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// onChange with the changed paths after edits settle for a debounce interval.
// Blocks until ctx is cancelled.
func Watch(ctx context.Context, root string, onChange func(paths []string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	addTree := func(dir string) {
		// A tree that cannot be walked simply is not watched; the diagram
		// still builds, it just will not refresh by itself.
		if werr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDir(d.Name()) {
					return filepath.SkipDir
				}
				if aerr := w.Add(path); aerr != nil {
					log.Printf("terradune: not watching %s: %v", path, aerr)
				}
			}
			return nil
		}); werr != nil {
			log.Printf("terradune: scanning %s: %v", dir, werr)
		}
	}
	addTree(root)

	var (
		timer   *time.Timer
		mu      sync.Mutex
		pending = map[string]bool{}
	)
	fire := func(path string) {
		mu.Lock()
		pending[path] = true
		mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(debounce, func() {
			mu.Lock()
			paths := make([]string, 0, len(pending))
			for p := range pending {
				paths = append(paths, p)
			}
			pending = map[string]bool{}
			mu.Unlock()
			onChange(paths)
		})
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
				fire(ev.Name)
			}
		case <-w.Errors:
			// Non-fatal; keep watching.
		}
	}
}
