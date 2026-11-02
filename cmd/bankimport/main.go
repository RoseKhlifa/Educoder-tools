// bankimport imports public source references; it never contacts an upstream
// account or turns repository claims into verified answer evidence.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"educoder-cli/internal/questionbank"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("bankimport", flag.ContinueOnError)
	bank := fs.String("bank", "", "local SQLite bank path (required unless --dry-run)")
	dir := fs.String("source-dir", "", "local pinned source checkout")
	repository := fs.String("repository", "https://github.com/ppxxforever/fuck-educoder", "public source repository")
	commit := fs.String("commit", "", "full source commit SHA")
	dry := fs.Bool("dry-run", false, "inspect without modifying a bank")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *dir == "" || *commit == "" || (!*dry && *bank == "") {
		return fmt.Errorf("--source-dir, --commit and --bank are required; --dry-run permits omitting --bank")
	}
	bankPath := *bank
	if *dry {
		bankPath = ":memory:"
	}
	s, err := questionbank.Open(bankPath)
	if err != nil {
		return err
	}
	defer s.Close()
	report, err := s.ImportExternalDirectory(context.Background(), questionbank.ExternalImportOptions{SourceDir: *dir, Repository: *repository, Commit: *commit, DryRun: *dry})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
