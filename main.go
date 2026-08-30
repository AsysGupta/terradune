// terradune: Terraform, drawn. One command turns any Terraform codebase into
// a clear diagram of what it will create — and what already exists.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/AsysGupta/terradune/internal/graph"
	"github.com/AsysGupta/terradune/internal/ingest"
	"github.com/AsysGupta/terradune/internal/server"
	"github.com/AsysGupta/terradune/internal/watch"
)

var version = "dev"

// repeatable collects a flag given more than once, like terraform's own
// -var-file and -var.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

// planConcurrency bounds parallel `terraform plan` runs: enough to keep a
// multi-workspace scan quick without thrashing a laptop.
const planConcurrency = 3

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	printOnly := flag.Bool("print", false, "print the inventory and graph once, without serving")
	port := flag.Int("port", 8383, "port for the local server")
	refresh := flag.Bool("refresh", false, "refresh state before planning (slower, needs live credentials)")
	var varFiles, vars repeatable
	flag.Var(&varFiles, "var-file", "variable file to pass to terraform (repeatable)")
	flag.Var(&vars, "var", "variable as name=value (repeatable)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: terradune [flags] <path>\n\n")
		fmt.Fprintf(flag.CommandLine.Output(),
			"<path> may be a Terraform workspace or a directory containing several;\n"+
				"every initialized workspace beneath it is planned and drawn.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("terradune", version)
		return
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}

	opts := ingest.Options{VarFiles: varFiles, Vars: vars, Refresh: *refresh}
	if err := run(context.Background(), dir, *port, *printOnly, opts); err != nil {
		fmt.Fprintln(os.Stderr, "terradune:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir string, port int, printOnly bool, opts ingest.Options) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	workspaces, err := ingest.Discover(root)
	if err != nil {
		return err
	}

	if printOnly {
		for _, ws := range workspaces {
			fmt.Printf("\n=== %s ===\n", ws.Name)
			inv, err := ingest.Load(ctx, ws.Dir, opts)
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			inv.PrintSummary(os.Stdout)
			graph.BuildWithDOT(inv.Plan, inv.DOT).Print(os.Stdout)
		}
		return nil
	}

	srv := server.New(root)
	plan := func(ws ingest.Workspace) {
		srv.SetRebuilding(ws.Name, ws.Dir)
		inv, err := ingest.Load(ctx, ws.Dir, opts)
		if err != nil {
			log.Printf("%s: plan failed: %v", ws.Name, err)
			srv.SetError(ws.Name, ws.Dir, err.Error())
			return
		}
		srv.SetGraph(ws.Name, ws.Dir, inv.TerraformVersion,
			graph.BuildWithDOT(inv.Plan, inv.DOT), graph.BuildDetails(inv.Plan))
		log.Printf("%s: %d resources", ws.Name, len(inv.Resources))
	}

	log.Printf("planning %d workspace(s) under %s", len(workspaces), root)
	go planAll(workspaces, plan)

	// Rebuilds are serialized per workspace; a change arriving mid-plan
	// queues exactly one follow-up rather than piling up.
	rebuild := make(chan ingest.Workspace, len(workspaces))
	go func() {
		queued := map[string]bool{}
		var mu sync.Mutex
		for ws := range rebuild {
			mu.Lock()
			if queued[ws.Name] {
				mu.Unlock()
				continue
			}
			queued[ws.Name] = true
			mu.Unlock()

			plan(ws)

			mu.Lock()
			delete(queued, ws.Name)
			mu.Unlock()
		}
	}()

	go func() {
		err := watch.Watch(ctx, root, func(paths []string) {
			hit := map[string]ingest.Workspace{}
			for _, p := range paths {
				if ws, ok := ingest.Owner(workspaces, p); ok {
					hit[ws.Name] = ws
				}
			}
			if len(hit) == 0 { // a shared file outside every workspace
				for _, ws := range workspaces {
					hit[ws.Name] = ws
				}
			}
			for _, ws := range hit {
				log.Printf("%s: change detected", ws.Name)
				rebuild <- ws
			}
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("watcher stopped: %v", err)
		}
	}()

	addr := fmt.Sprintf("localhost:%d", port)
	// Bind before announcing: otherwise a second terradune prints a serving
	// banner it cannot honour, and the browser keeps talking to the first one.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("port %d is already in use — another terradune may be running; "+
				"stop it or choose another port with -port", port)
		}
		return err
	}
	log.Printf("terradune serving http://%s", addr)
	return http.Serve(ln, srv.Handler())
}

func planAll(workspaces []ingest.Workspace, plan func(ingest.Workspace)) {
	sem := make(chan struct{}, planConcurrency)
	var wg sync.WaitGroup
	for _, ws := range workspaces {
		wg.Add(1)
		go func(ws ingest.Workspace) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			plan(ws)
		}(ws)
	}
	wg.Wait()
}
