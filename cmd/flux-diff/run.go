package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/flux-diff-action/internal/config"
	"github.com/JtheGunner/flux-diff-action/internal/flate"
	"github.com/JtheGunner/flux-diff-action/internal/format"
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
	flags.StringVar(&opts.registryConfig, "registry-config", "", "registry credentials file passed to flate; overrides registryConfig of the configuration and may be an absolute path")
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

	// Mask before anything is formatted, written or put into an error.
	masked := masker.MaskResult(raw)
	known := masker.KnownValues(raw)

	rendered, err := render(masked, opts.format)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(out, rendered); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	if failure := exitError(masked, opts.strict); failure != nil {
		return errors.New(guard.MaskString(failure.Error(), known))
	}

	return nil
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

	cfg, err := config.Load(filepath.Join(opts.path, config.FileName))
	if err != nil {
		return config.Config{}, fmt.Errorf("load %s: %w", config.FileName, err)
	}

	return cfg, nil
}

func render(masked model.Result, outputFormat string) (string, error) {
	switch outputFormat {
	case formatText:
		return format.Text(masked), nil
	case formatJSON:
		return format.JSON(masked)
	default:
		body, _ := format.Markdown(masked, format.MarkdownOptions{})

		return body, nil
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
