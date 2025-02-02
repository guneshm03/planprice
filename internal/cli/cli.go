// Package cli implements the planprice command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/guneshm03/planprice/internal/mapper"
	"github.com/guneshm03/planprice/internal/pricedata"
	"github.com/guneshm03/planprice/internal/pricing"
	"github.com/guneshm03/planprice/internal/usage"
)

// Version is set at build time with -ldflags "-X .../cli.Version=...".
var Version = "dev"

// Exit codes.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitUsage     = 2
	ExitThreshold = 3
)

// errUsage marks command-line mistakes (exit code 2).
var errUsage = errors.New("usage error")

// errThreshold marks a diff that exceeded a --fail-above-* threshold.
var errThreshold = errors.New("cost increase exceeds threshold")

const topUsage = `planprice estimates AWS costs from Terraform plan JSON.

Usage:
  planprice breakdown      --plan plan.json [--usage usage.yml] [--format table|markdown|json]
  planprice diff           --base base.json --head head.json [--usage usage.yml] [--format ...]
  planprice diff           --plan plan.json            (prior state vs. planned)
  planprice usage-template --plan plan.json
  planprice resources      [--format table|markdown]
  planprice snapshot       info|verify|lookup|diff
  planprice version

Produce plan JSON with: terraform plan -out tf.plan && terraform show -json tf.plan > plan.json
Run "planprice <command> -h" for command flags.
`

// Run executes the CLI and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, topUsage)
		return ExitUsage
	}
	var err error
	switch args[0] {
	case "breakdown":
		err = runBreakdown(ctx, args[1:], stdout, stderr)
	case "diff":
		err = runDiff(ctx, args[1:], stdout, stderr)
	case "usage-template":
		err = runUsageTemplate(args[1:], stdout, stderr)
	case "resources":
		err = runResources(args[1:], stdout, stderr)
	case "snapshot":
		err = runSnapshot(args[1:], stdout, stderr)
	case "version", "--version":
		_, err = fmt.Fprintf(stdout, "planprice %s\n", Version)
	case "help", "-h", "--help":
		_, err = io.WriteString(stdout, topUsage)
	default:
		_, _ = fmt.Fprintf(stderr, "planprice: unknown command %q\n\n%s", args[0], topUsage)
		return ExitUsage
	}
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, flag.ErrHelp):
		return ExitOK
	case errors.Is(err, errThreshold):
		_, _ = fmt.Fprintf(stderr, "planprice: %v\n", err)
		return ExitThreshold
	case errors.Is(err, errUsage):
		_, _ = fmt.Fprintf(stderr, "planprice: %v\n", err)
		return ExitUsage
	default:
		_, _ = fmt.Fprintf(stderr, "planprice: %v\n", err)
		return ExitError
	}
}

// common holds flags shared by the estimating commands.
type common struct {
	usagePath     string
	snapshotPath  string
	defaultRegion string
	format        string
	out           string
	verbose       bool
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.usagePath, "usage", "", "usage file (YAML) with usage-based values")
	fs.StringVar(&c.snapshotPath, "snapshot", "", "price snapshot JSON (default: the embedded snapshot)")
	fs.StringVar(&c.defaultRegion, "region", "us-east-1", "region for resources whose region the plan does not determine")
	fs.StringVar(&c.format, "format", "table", "output format: table, markdown or json")
	fs.StringVar(&c.out, "out", "", "write output to this file instead of stdout")
	fs.BoolVar(&c.verbose, "verbose", false, "log debug details to stderr")
}

func (c *common) logger(stderr io.Writer) *slog.Logger {
	level := slog.LevelWarn
	if c.verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("planprice "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	return nil
}

func loadSnapshot(path string) (*pricing.Snapshot, error) {
	if path == "" || path == "embedded" {
		return pricedata.Default()
	}
	return pricing.LoadFile(path)
}

// loadUsage reads and validates the usage file (nil when path is empty).
func loadUsage(path string, reg *mapper.Registry, log *slog.Logger) (*usage.File, error) {
	if path == "" {
		return nil, nil
	}
	f, err := usage.Load(path)
	if err != nil {
		return nil, err
	}
	warnings, err := f.Validate(reg.UsageSchema)
	for _, w := range warnings {
		log.Warn(w)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// output opens the --out file or returns stdout.
func output(path string, stdout io.Writer) (io.Writer, func() error, error) {
	if path == "" {
		return stdout, func() error { return nil }, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

func requireFlag(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: --%s is required", errUsage, name)
	}
	return nil
}
