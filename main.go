// terradune: Terraform, drawn. One command turns any Terraform codebase into
// a clear diagram of what it will create — and what already exists.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AsysGupta/terradune/internal/graph"
	"github.com/AsysGupta/terradune/internal/ingest"
	"github.com/AsysGupta/terradune/internal/server"
	"github.com/AsysGupta/terradune/internal/watch"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	printOnly := flag.Bool("print", false, "print the inventory and graph once, without serving")
	port := flag.Int("port", 8383, "port for the local server")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: terradune [flags] <path-to-terraform-dir>\n\n")
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

	if err := run(context.Background(), dir, *port, *printOnly); err != nil {
		fmt.Fprintln(os.Stderr, "terradune:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir string, port int, printOnly bool) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	inv, err := ingest.Load(ctx, abs)
	if err != nil {
		return err
	}
	g := graph.Build(inv.Plan)

	if printOnly {
		inv.PrintSummary(os.Stdout)
		g.Print(os.Stdout)
		return nil
	}

	srv := server.New()
	srv.SetGraph(inv.TerraformVersion, g)

	// One rebuild at a time; extra triggers during a rebuild coalesce into one.
	trigger := make(chan struct{}, 1)
	go func() {
		if err := watch.Watch(ctx, abs, func() {
			select {
			case trigger <- struct{}{}:
			default:
			}
		}); err != nil && ctx.Err() == nil {
			log.Printf("watcher stopped: %v", err)
		}
	}()
	go func() {
		for range trigger {
			log.Printf("change detected, re-planning %s", abs)
			srv.SetRebuilding()
			inv, err := ingest.Load(ctx, abs)
			if err != nil {
				log.Printf("rebuild failed: %v", err)
				srv.SetError(err.Error())
				continue
			}
			srv.SetGraph(inv.TerraformVersion, graph.Build(inv.Plan))
			log.Printf("rebuilt: %d resources", len(inv.Resources))
		}
	}()

	addr := fmt.Sprintf("localhost:%d", port)
	log.Printf("terradune serving http://%s (watching %s)", addr, abs)
	return http.ListenAndServe(addr, srv.Handler())
}
