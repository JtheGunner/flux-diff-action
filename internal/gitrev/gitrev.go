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
	"os"
	"os/exec"
	"path"
	"path/filepath"
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

// ResolveRoot returns the top-level directory of the repository that contains
// dir. Bare repositories are refused, so a directory that merely looks like one
// is never treated as a repository. Callers that know a trusted root (the
// runner's workspace) should pass it to ReadFile instead.
func ResolveRoot(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "", maxBlobBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find the repository root: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// ReadFile returns the content of the regular file name (relative to dir) as
// stored in rev. root is the trusted repository root and dir a directory below
// it. git always runs in root and never discovers a repository from dir, which
// may hold a pull request's files. Symlinks and other non-blob entries are
// rejected: they could point anywhere.
func ReadFile(ctx context.Context, root, dir, rev, name string) ([]byte, error) {
	if err := validateRev(rev); err != nil {
		return nil, err
	}
	if err := validateName(name); err != nil {
		return nil, err
	}
	relative, err := repoRelative(root, dir, name)
	if err != nil {
		return nil, err
	}
	name = relative

	listing, err := git(ctx, root, root, maxBlobBytes, "ls-tree", "-z", rev, "--", name)
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

	content, err := git(ctx, root, root, maxBlobBytes, "cat-file", "blob", object)
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

// git runs git in dir. ceilingRoot, when set, is the trusted repository root:
// discovery is not allowed to climb above its parent.
func git(ctx context.Context, dir, ceilingRoot string, limit int64, args ...string) ([]byte, error) {
	// The derived context lets us kill git once the limit is exceeded: git would
	// otherwise block writing into the full pipe and Wait would never return.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Never let a bare-repository look-alike in the working tree become the
	// repository.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "safe.bareRepository=explicit"}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv(ceilingRoot)

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

// gitEnv drops variables that would redirect git to another repository and, with
// a trusted root, forbids discovery above it.
func gitEnv(root string) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES":
			continue
		}
		env = append(env, entry)
	}
	if root != "" {
		env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	}

	return env
}

// repoRelative converts name, relative to dir, into a path relative to root.
//
// The path is computed lexically. Only the trusted root is symlink-resolved:
// a pull request can commit a symlink below the root, and following it would
// let the PR choose which path of the base revision is read. A symlink anywhere
// between root and dir therefore fails closed.
func repoRelative(root, dir, name string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("cannot resolve the repository root")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", errors.New("cannot resolve the repository root")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", errors.New("cannot resolve the manifest directory")
	}

	// Walk up from dir until the trusted root is reached. The root may be
	// spelled through an alias (/var vs /private/var), so an ancestor counts as
	// the root when it resolves to it. Every component below is checked, and
	// none may be a symlink.
	var parts []string
	for current := absDir; ; {
		info, statErr := os.Lstat(current)
		isLink := statErr == nil && info.Mode()&os.ModeSymlink != 0

		if !isLink || current == absRoot {
			if resolved, evalErr := filepath.EvalSymlinks(current); evalErr == nil && resolved == realRoot {
				break
			}
		}
		if isLink {
			return "", errors.New("the manifest directory must not be reached through a symlink")
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("the manifest directory is not inside the repository root")
		}
		parts = append([]string{filepath.Base(current)}, parts...)
		current = parent
	}

	return path.Join(append(parts, name)...), nil
}
