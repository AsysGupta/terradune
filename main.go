// terradune: Terraform, drawn. One command turns any Terraform codebase into
// a clear diagram of what it will create — and what already exists.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/AsysGupta/terradune/internal/graph"
	"github.com/AsysGupta/terradune/internal/ingest"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
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

	if err := run(context.Background(), dir); err != nil {
		fmt.Fprintln(os.Stderr, "terradune:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir string) error {
	inv, err := ingest.Load(ctx, dir)
	if err != nil {
		return err
	}
	inv.PrintSummary(os.Stdout)
	graph.Build(inv.Plan).Print(os.Stdout)
	return nil
}
