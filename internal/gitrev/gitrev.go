// Package gitrev reads files from git revisions without touching the working
// tree. Every git call uses a fixed argument list and never goes through a
// shell.
package gitrev

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
)

// maxBlobBytes bounds how much of a blob is read.
const maxBlobBytes = 1 << 20

var (
	// ErrNotFound means the revision holds no entry at the requested path.
	ErrNotFound = errors.New("file not found in revision")
	// ErrNotRegularFile means the entry is a symlink, submodule or directory.
	ErrNotRegularFile = errors.New("entry in revision is not a regular file")
)

// ReadFile returns the content of the regular file name (relative to dir, no
// subdirectories above dir) as stored in rev. dir is the working directory of
// git, so name resolves relative to dir inside the repository. Symlinks and
// other non-blob entries are rejected: they could point anywhere.
func ReadFile(ctx context.Context, dir, rev, name string) ([]byte, error) {
	if err := validateRev(rev); err != nil {
		return nil, err
	}
	if err := validateName(name); err != nil {
		return nil, err
	}

	listing, err := git(ctx, dir, maxBlobBytes, "ls-tree", "-z", rev, "--", name)
	if err != nil {
		return nil, fmt.Errorf("list %s in the base revision: %w", name, err)
	}
	if len(listing) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	mode, objectType, object, err := parseTreeEntry(listing)
	if err != nil {
		return nil, err
	}
	if objectType != "blob" || (mode != "100644" && mode != "100755") {
		return nil, fmt.Errorf("%w: %s", ErrNotRegularFile, name)
	}

	content, err := git(ctx, dir, maxBlobBytes, "cat-file", "blob", object)
	if err != nil {
		return nil, fmt.Errorf("read %s from the base revision: %w", name, err)
	}

	return content, nil
}

func validateRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, "\x00\n") {
		return errors.New("invalid git revision")
	}

	return nil
}

func validateName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || path.IsAbs(name) || strings.ContainsAny(name, "\x00\n") {
		return errors.New("invalid file name")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return errors.New("invalid file name")
		}
	}

	return nil
}

// parseTreeEntry parses one "git ls-tree -z" record:
// "<mode> SP <type> SP <object> TAB <path> NUL".
func parseTreeEntry(listing []byte) (mode, objectType, object string, err error) {
	record, _, _ := strings.Cut(string(listing), "\x00")
	meta, _, found := strings.Cut(record, "\t")
	fields := strings.Fields(meta)
	if !found || len(fields) != 3 {
		return "", "", "", errors.New("unexpected git ls-tree output")
	}

	return fields[0], fields[1], fields[2], nil
}

func git(ctx context.Context, dir string, limit int64, args ...string) ([]byte, error) {
	// The derived context lets us kill git once the limit is exceeded: git would
	// otherwise block writing into the full pipe and Wait would never return.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start git %s: %w", args[0], err)
	}

	data, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	exceeded := int64(len(data)) > limit
	if exceeded || readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("read git %s output: %w", args[0], readErr)
	}
	if exceeded {
		return nil, fmt.Errorf("git %s output exceeds %d bytes", args[0], limit)
	}
	if waitErr != nil {
		// git's own message (bad revision, not a repository) never holds file
		// content, but stays out of the error to keep errors short.
		return nil, fmt.Errorf("git %s failed: %w", args[0], waitErr)
	}

	return data, nil
}
