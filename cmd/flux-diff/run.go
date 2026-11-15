package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/flux-diff-action/internal/config"
	"github.com/JtheGunner/flux-diff-action/internal/flate"
	"github.com/JtheGunner/flux-diff-action/internal/format"
	"github.com/JtheGunner/flux-diff-action/internal/gitrev"
	"github.com/JtheGunner/flux-diff-action/internal/guard"
	"github.com/JtheGunner/flux-diff-action/internal/model"
)

// Output formats accepted by --format.
const (
	formatMarkdown = "markdown"
	formatText     = "text"
	formatJSON     = "json"
)

// headWorkingTree is the only --head value that is supported: the checkout at
// --path.
const headWorkingTree = "HEAD"

type runOptions struct {
	base        string
	head        string
	path        string
	strict      bool
	format      string
	config      string
	flateBinary string
	// registryConfig comes from the trusted command line and may be absolute;
	// it is only passed to flate as a path.
	registryConfig string
	// configBaseRev reads the configuration from this git revision instead of
	// the working tree, so a PR cannot weaken its own checks.
	configBaseRev string
	// repoRoot is the trusted repository root for --config-base-rev; it
	// defaults to $GITHUB_WORKSPACE, then to the repository that holds --path.
	repoRoot string
	// configName is the configuration file name looked up with configBaseRev.
	configName string
	// maxSize caps the Markdown body in characters; the format default when 0.
	maxSize int
	// output receives the rendered report instead of stdout.
	output string
	// fullOutput receives the untruncated Markdown, only when maxSize cut it.
	fullOutput string
}

func (o runOptions) validate() error {
	switch o.format {
	case formatMarkdown, formatText, formatJSON:
	default:
		return fmt.Errorf("unsupported --format %q (want markdown, text or json)", o.format)
	}
	if o.head != "" && o.head != headWorkingTree {
		return errors.New("--head other than the working tree is not supported yet: check out the head revision and run with --path")
	}
	if o.configBaseRev != "" && o.config != "" {
		return errors.New("--config and --config-base-rev cannot be combined")
	}
	if o.fullOutput != "" && o.format != formatMarkdown {
		return errors.New("--full-output needs --format markdown")
	}

	return nil
}

func addRunFlags(cmd *cobra.Command, opts *runOptions) {
	flags := cmd.Flags()
	flags.StringVar(&opts.base, "base", "", "Git revision to compare against (passed to flate --base)")
	flags.StringVar(&opts.head, "head", "", "head revision; only the working tree at --path is supported yet")
	flags.StringVar(&opts.path, "path", ".", "directory holding the Flux manifests")
	flags.BoolVar(&opts.strict, "strict", false, "also exit 1 when units are only partially rendered")
	flags.StringVar(&opts.format, "format", formatMarkdown, "output format: markdown, text or json")
	flags.StringVar(&opts.config, "config", "", "configuration file (default: .flux-diff.yaml in --path, optional; a missing --config file is an error)")
	flags.StringVar(&opts.configBaseRev, "config-base-rev", "", "read the configuration from this git revision (run in --path) instead of the working tree; a missing file means defaults")
	flags.StringVar(&opts.repoRoot, "repo-root", "", "trusted repository root for --config-base-rev (default: $GITHUB_WORKSPACE, else the repository holding --path)")
	flags.StringVar(&opts.configName, "config-name", config.FileName, "configuration file name used with --config-base-rev, relative to --path")
	flags.IntVar(&opts.maxSize, "max-size", 0, "maximum Markdown length in characters (default 65000)")
	flags.StringVar(&opts.output, "output", "", "write the report to this file instead of stdout")
	flags.StringVar(&opts.fullOutput, "full-output", "", "with --format markdown, write the untruncated report to this file when --max-size cut the output")
	flags.StringVar(&opts.registryConfig, "registry-config", "", "registry credentials file passed to flate; the only way to supply registry credentials; may be an absolute path")
	flags.StringVar(&opts.flateBinary, "flate-binary", "flate", "flate executable to run")
}

func runDiff(ctx context.Context, opts runOptions, out io.Writer) error {
	if err := opts.validate(); err != nil {
		return err
	}

	cfg, err := loadConfig(opts)
	if err != nil {
		return err
	}

	if opts.registryConfig != "" {
		cfg.RegistryConfig = opts.registryConfig
	}

	// Build the guard first so an invalid maskPatterns glob fails before flate runs.
	masker, err := guard.New(guard.Config{ExtraKeyPatterns: cfg.MaskPatterns})
	if err != nil {
		return fmt.Errorf("set up secret guard: %w", err)
	}

	raw, err := flate.Run(ctx, flate.Options{
		Binary: opts.flateBinary, Path: opts.path, Base: opts.base, Args: cfg.FlateArgs(),
	})
	if err != nil {
		return fmt.Errorf("run flate: %w", err)
	}

	raw.BrokenInBothSides, err = findBrokenInBothSides(ctx, opts, cfg, raw)
	if err != nil {
		return err
	}

	// Mask before anything is formatted, written or put into an error.
	masked := masker.MaskResult(raw)
	known := masker.KnownValues(raw)

	if err := writeReport(masked, opts, out); err != nil {
		return err
	}

	if failure := exitError(masked, opts.strict); failure != nil {
		return errors.New(guard.MaskString(failure.Error(), known))
	}

	return nil
}

// findBrokenInBothSides builds the head once more: flate's diff stays silent
// for a unit that fails identically in base and head, a build does not. The
// head failures the diff did not report are those units.
func findBrokenInBothSides(ctx context.Context, opts runOptions, cfg config.Config, diff model.Result) ([]model.BrokenUnit, error) {
	built, err := flate.Build(ctx, flate.Options{
		Binary: opts.flateBinary, Path: opts.path, Args: cfg.BuildArgs(),
	})
	if err != nil {
		return nil, fmt.Errorf("run flate build: %w", err)
	}

	return flate.BrokenInBothSides(diff.Failures, built), nil
}

// loadConfig reads the explicit --config file (which must exist) or, without
// one, the optional .flux-diff.yaml in --path. Relative paths inside the file
// resolve against the directory of the file.
func loadConfig(opts runOptions) (config.Config, error) {
	if opts.config != "" {
		cfg, err := config.LoadRequired(opts.config)
		if err != nil {
			return config.Config{}, fmt.Errorf("load --config: %w", err)
		}

		return cfg, nil
	}

	if opts.configBaseRev != "" {
		return loadBaseConfig(opts)
	}

	cfg, err := config.Load(filepath.Join(opts.path, config.FileName))
	if err != nil {
		return config.Config{}, fmt.Errorf("load %s: %w", config.FileName, err)
	}

	return cfg, nil
}

// loadBaseConfig reads the configuration as stored in opts.configBaseRev. A PR
// controls the file in its head, so the base version is the trusted one. A
// missing file means defaults; symlinks are rejected.
func loadBaseConfig(opts runOptions) (config.Config, error) {
	ctx := context.Background()
	root := opts.repoRoot
	if root == "" {
		root = os.Getenv("GITHUB_WORKSPACE")
	}
	if root == "" {
		resolved, err := gitrev.ResolveRoot(ctx, opts.path)
		if err != nil {
			return config.Config{}, fmt.Errorf("load %s from --config-base-rev: %w", opts.configName, err)
		}
		root = resolved
	}

	data, err := gitrev.ReadFile(ctx, root, opts.path, opts.configBaseRev, opts.configName)
	if errors.Is(err, gitrev.ErrNotFound) {
		return config.Config{}, nil
	}
	if err != nil {
		return config.Config{}, fmt.Errorf("load %s from --config-base-rev: %w", opts.configName, err)
	}

	cfg, err := config.Parse(data, opts.path)
	if err != nil {
		return config.Config{}, fmt.Errorf("load %s from --config-base-rev: %w", opts.configName, err)
	}

	return cfg, nil
}

// writeReport renders the masked result to opts.output or out. It also writes
// the untruncated Markdown to opts.fullOutput when the report was cut.
func writeReport(masked model.Result, opts runOptions, out io.Writer) error {
	rendered, truncated, err := render(masked, opts.format, opts.maxSize)
	if err != nil {
		return err
	}

	if opts.output == "" {
		if _, err := io.WriteString(out, rendered); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	} else if err := os.WriteFile(opts.output, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("write --output file: %w", err)
	}

	if truncated && opts.fullOutput != "" {
		full, _ := format.Markdown(masked, format.MarkdownOptions{MaxChars: math.MaxInt32})
		if err := os.WriteFile(opts.fullOutput, []byte(full), 0o600); err != nil {
			return fmt.Errorf("write --full-output file: %w", err)
		}
	}

	return nil
}

func render(masked model.Result, outputFormat string, maxSize int) (rendered string, truncated bool, err error) {
	switch outputFormat {
	case formatText:
		return format.Text(masked), false, nil
	case formatJSON:
		rendered, err = format.JSON(masked)

		return rendered, false, err
	default:
		rendered, truncated = format.Markdown(masked, format.MarkdownOptions{MaxChars: maxSize})

		return rendered, truncated, nil
	}
}

// exitError maps a masked result to the process outcome: nil for a completed
// run, an error (exit code 1) when a unit failed, and with strict also when a
// unit is only partial.
func exitError(masked model.Result, strict bool) error {
	if len(masked.Failures) > 0 || masked.ExitCode != 0 {
		return errors.New(format.FailureSummary(masked))
	}
	if strict && format.IsPartial(masked) {
		return errors.New(format.PartialSummary(masked))
	}

	return nil
}
