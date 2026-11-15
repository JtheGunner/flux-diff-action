package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/flux-diff-action/internal/flate"
	"github.com/JtheGunner/flux-diff-action/internal/format"
	"github.com/JtheGunner/flux-diff-action/internal/ghcomment"
	"github.com/JtheGunner/flux-diff-action/internal/ghevent"
)

const (
	defaultAPIURL = "https://api.github.com"
	// maxBodyFileBytes bounds the comment body that is read into memory.
	maxBodyFileBytes = 8 << 20
)

// outputValuePattern is the only shape of value written as key=value output:
// hex hashes, digits and booleans. Nothing else can carry a newline or a
// workflow command into $GITHUB_OUTPUT.
var outputValuePattern = regexp.MustCompile(`^(?:[0-9a-f]{40,64}|[0-9]{1,10}|true|false|created|updated|step-summary)$`)

// writeOutputs prints key=value lines after checking every value.
func writeOutputs(out io.Writer, pairs ...[2]string) error {
	for _, pair := range pairs {
		if !outputValuePattern.MatchString(pair[1]) {
			return errors.New("refusing to print an unexpected output value")
		}
	}
	for _, pair := range pairs {
		if _, err := fmt.Fprintf(out, "%s=%s\n", pair[0], pair[1]); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}

func newEventCommand() *cobra.Command {
	var eventPath, repoDir string

	cmd := &cobra.Command{
		Use:   "event",
		Short: "Print pr, head, base (merge base) and fork of a pull_request event as key=value lines",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if eventPath == "" {
				eventPath = os.Getenv("GITHUB_EVENT_PATH")
			}
			if eventPath == "" {
				return errors.New("no event payload: set GITHUB_EVENT_PATH or pass --event-path")
			}

			pr, err := ghevent.Load(eventPath)
			if err != nil {
				return err
			}
			mergeBase, err := ghevent.MergeBase(cmd.Context(), repoDir, pr.BaseSHA, pr.HeadSHA)
			if err != nil {
				return err
			}

			return writeOutputs(cmd.OutOrStdout(),
				[2]string{"pr", fmt.Sprint(pr.Number)},
				[2]string{"head", pr.HeadSHA},
				[2]string{"base", mergeBase},
				[2]string{"fork", fmt.Sprint(pr.Fork)},
			)
		},
	}
	cmd.Flags().StringVar(&eventPath, "event-path", "", "pull_request event JSON (default: $GITHUB_EVENT_PATH)")
	cmd.Flags().StringVar(&repoDir, "repo", ".", "git repository holding both commits")

	return cmd
}

type commentOptions struct {
	pr          int
	bodyFile    string
	marker      string
	maxSize     int
	stepSummary string
	fork        bool
}

func newCommentCommand() *cobra.Command {
	var opts commentOptions

	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Create or update the single flux-diff comment on a pull request",
		Long: "Reads GITHUB_TOKEN, GITHUB_REPOSITORY and GITHUB_API_URL from the environment. " +
			"Fork PRs and read-only tokens fall back to the step summary file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runComment(cmd.Context(), opts, cmd.OutOrStdout())
		},
	}
	flags := cmd.Flags()
	flags.IntVar(&opts.pr, "pr", 0, "pull request number")
	flags.StringVar(&opts.bodyFile, "body-file", "", "file holding the masked comment body")
	flags.StringVar(&opts.marker, "marker", format.DefaultMarker, "hidden marker that identifies the comment")
	flags.IntVar(&opts.maxSize, "max-size", ghcomment.DefaultMaxSize, "maximum comment length in characters")
	flags.StringVar(&opts.stepSummary, "step-summary", "", "file that receives the body when the comment cannot be written (the step summary)")
	flags.BoolVar(&opts.fork, "fork", false, "the PR comes from a fork: skip the API and write the step summary")

	return cmd
}

func runComment(ctx context.Context, opts commentOptions, out io.Writer) error {
	if opts.bodyFile == "" {
		return errors.New("--body-file is required")
	}
	body, err := readBounded(opts.bodyFile, maxBodyFileBytes)
	if err != nil {
		return err
	}

	apiURL := os.Getenv("GITHUB_API_URL")
	if apiURL == "" {
		apiURL = defaultAPIURL
	}

	result, err := ghcomment.Upsert(ctx, ghcomment.Config{
		APIURL:          apiURL,
		Repository:      os.Getenv("GITHUB_REPOSITORY"),
		Token:           os.Getenv("GITHUB_TOKEN"),
		MaxSize:         opts.maxSize,
		Fork:            opts.fork,
		StepSummaryPath: opts.stepSummary,
	}, opts.pr, opts.marker, body)
	if err != nil {
		return fmt.Errorf("post comment: %w", err)
	}

	return writeOutputs(out,
		[2]string{"outcome", string(result.Outcome)},
		[2]string{"truncated", fmt.Sprint(result.Truncated)},
	)
}

func readBounded(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open --body-file: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", fmt.Errorf("read --body-file: %w", err)
	}
	if int64(len(data)) > limit {
		return "", errors.New("--body-file is too large")
	}

	return string(data), nil
}

func newFetchFlateCommand() *cobra.Command {
	var (
		version, dest, downloadBase string
	)

	cmd := &cobra.Command{
		Use:   "fetch-flate",
		Short: "Download flate and verify it against the checksums built into flux-diff",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dest == "" {
				return errors.New("--dest is required")
			}
			path, err := flate.Fetch(cmd.Context(), flate.FetchOptions{
				Version: version, Dest: dest, DownloadBase: downloadBase,
			})
			if err != nil {
				return fmt.Errorf("fetch flate %s: %w", version, err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "installed", strings.TrimSpace(path))

			return err
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&version, "version", flate.PinnedVersion, "flate release to download; only versions with an embedded checksum are accepted")
	flags.StringVar(&dest, "dest", "", "directory that receives the flate executable")
	flags.StringVar(&downloadBase, "download-base", "", "override the release download URL prefix")
	_ = flags.MarkHidden("download-base")

	return cmd
}
