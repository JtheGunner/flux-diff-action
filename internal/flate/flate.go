// Package flate runs the flate CLI as a separate process and turns its output
// into a typed model. flate is AGPL-3.0 licensed and must never be imported as
// a library.
package flate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/JtheGunner/flux-diff-action/internal/guard"
	"github.com/JtheGunner/flux-diff-action/internal/model"
)

// Result is the complete outcome of one flate run. As returned by Run it is
// unmasked: callers must pass it through guard.MaskResult before it reaches
// any output sink (formatter, log, artifact).
type Result = model.Result

const (
	defaultBinary = "flate"
	// maxOutputBytes bounds how much of each stream is kept in memory.
	maxOutputBytes = 256 << 20
	// DefaultTimeout bounds each flate process when Options.Timeout is unset.
	DefaultTimeout = 10 * time.Minute
	// killGracePeriod is how long Wait waits for the output pipes to close
	// after flate was killed, so a child that inherited them cannot block it.
	killGracePeriod = 5 * time.Second
)

var (
	// ErrBinaryNotFound means the flate executable does not exist or is not
	// runnable.
	ErrBinaryNotFound = errors.New("flate binary not found")
	// ErrVersionTooOld means the installed flate is older than MinVersion.
	ErrVersionTooOld = errors.New("flate version is below the supported minimum")
	// ErrOutputTooLarge means flate printed more than the supported amount.
	ErrOutputTooLarge = errors.New("flate output exceeds the size limit")
	// ErrTimeout means a flate process ran longer than Options.Timeout and was
	// killed.
	ErrTimeout = errors.New("flate timed out")
)

// Options configures one flate run.
type Options struct {
	// Binary is the flate executable; "flate" (looked up on PATH) when empty.
	Binary string
	// Path is the directory holding the Flux manifests (flate --path).
	Path string
	// Base is the Git revision to compare with (flate --base).
	Base string
	// Args are extra flate flags appended after the generated ones.
	Args []string
	// Timeout bounds each flate process (version check, diff, build);
	// DefaultTimeout when zero.
	Timeout time.Duration
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultTimeout
	}

	return o.Timeout
}

// Run executes `flate diff -o json` and returns the parsed result.
//
// Exit code 1 means flate hit errors but usually still produced a partial
// result; it is returned with ExitCode 1 and no error so the caller can show
// the failures. Any other non-zero exit code, or exit code 1 without usable
// JSON, is an error.
//
// The returned Result is raw and unmasked. The caller must apply
// guard.MaskResult before writing it to any output sink.
//
// Error messages never embed flate's stdout or stderr content.
func Run(ctx context.Context, opts Options) (Result, error) {
	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary
	}

	if err := checkVersion(ctx, binary, opts.timeout()); err != nil {
		return Result{}, err
	}

	args, err := guard.EnforceSkipKinds(buildArgs(opts))
	if err != nil {
		return Result{}, fmt.Errorf("refusing to run flate: %w", err)
	}

	stdout, stderr, exitCode, err := execute(ctx, binary, args, true, opts.timeout())
	if err != nil {
		return Result{}, err
	}

	if exitCode != 0 && exitCode != 1 {
		return Result{}, fmt.Errorf("flate exited with unexpected code %d", exitCode)
	}

	result, err := Parse(stdout, stderr)
	if err != nil {
		return Result{}, fmt.Errorf("flate exited with code %d: %w", exitCode, err)
	}

	result.ExitCode = exitCode

	return result, nil
}

func buildArgs(opts Options) []string {
	args := []string{"diff", "-o", "json", "--no-progress"}
	if opts.Path != "" {
		args = append(args, "-p", opts.Path)
	}
	if opts.Base != "" {
		args = append(args, "--base", opts.Base)
	}

	return append(args, opts.Args...)
}

func checkVersion(ctx context.Context, binary string, timeout time.Duration) error {
	stdout, _, exitCode, err := execute(ctx, binary, []string{"--version"}, true, timeout)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("flate --version exited with code %d", exitCode)
	}

	found, err := parseVersion(string(stdout))
	if err != nil {
		return errors.New("could not determine the flate version from `flate --version`")
	}

	foundText := fmt.Sprintf("v%d.%d.%d", found[0], found[1], found[2])

	order, err := compareVersions(foundText, MinVersion)
	if err != nil {
		return err
	}
	if order < 0 {
		return fmt.Errorf("%w: found %s, need at least %s", ErrVersionTooOld, foundText, MinVersion)
	}

	return nil
}

// execute runs the binary and returns its streams and exit code. A non-zero
// exit code is not an error; failing to start or finish the process is.
// Without keepStdout, stdout is discarded unread and returned empty. A process
// still running after timeout is killed and reported as ErrTimeout.
func execute(ctx context.Context, binary string, args []string, keepStdout bool, timeout time.Duration) (stdout, stderr []byte, exitCode int, err error) {
	var outBuf, errBuf limitedBuffer
	outBuf.limit, errBuf.limit = maxOutputBytes, maxOutputBytes

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, binary, args...)
	cmd.WaitDelay = killGracePeriod
	cmd.Env = environmentWithoutFlateVariables(os.Environ())
	if keepStdout {
		cmd.Stdout = &outBuf
	}
	cmd.Stderr = &errBuf

	runErr := cmd.Run()

	if ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return nil, nil, 0, fmt.Errorf("%w after %s", ErrTimeout, timeout)
	}
	if ctx.Err() != nil {
		return nil, nil, 0, fmt.Errorf("flate run aborted: %w", ctx.Err())
	}
	if outBuf.exceeded || errBuf.exceeded {
		return nil, nil, 0, ErrOutputTooLarge
	}

	if runErr == nil {
		return outBuf.Bytes(), errBuf.Bytes(), 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitCode(), nil
	}

	if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) || errors.Is(runErr, fs.ErrPermission) {
		return nil, nil, 0, fmt.Errorf("%w: %q (install flate %s or set the binary path)", ErrBinaryNotFound, binary, PinnedVersion)
	}

	return nil, nil, 0, fmt.Errorf("start flate %q: %w", binary, runErr)
}

// limitedBuffer keeps at most limit bytes and records when more arrived.
type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		return len(p), nil
	}

	return b.Buffer.Write(p)
}

// environmentWithoutFlateVariables drops every FLATE_* variable. flate reads
// its flags from such variables (for example FLATE_SKIP_KINDS), which could
// otherwise weaken the --skip-kinds the guard forces. All other variables
// (PATH, HOME, DOCKER_CONFIG, ...) are kept so registry credentials still work.
func environmentWithoutFlateVariables(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if strings.HasPrefix(entry, "FLATE_") {
			continue
		}
		kept = append(kept, entry)
	}

	return kept
}
