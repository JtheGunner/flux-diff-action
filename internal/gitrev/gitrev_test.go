package gitrev

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{
		"-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")

	return dir
}

func TestReadFile_ReturnsContentOfRevision(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "sub/.flux-diff.yaml", "kubeVersion: \"1.30.0\"\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	write(t, dir, "sub/.flux-diff.yaml", "kubeVersion: \"9.9.9\"\n")

	got, err := ReadFile(context.Background(), dir, filepath.Join(dir, "sub"), "HEAD", ".flux-diff.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "kubeVersion: \"1.30.0\"\n" {
		t.Errorf("content = %q, want the committed version", got)
	}
}

func TestReadFile_MissingFileIsNotFound(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "other", "x")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")

	_, err := ReadFile(context.Background(), dir, dir, "HEAD", ".flux-diff.yaml")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestReadFile_SymlinkIsRejected(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "real.yaml", "x: 1\n")
	if err := os.Symlink("real.yaml", filepath.Join(dir, ".flux-diff.yaml")); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")

	_, err := ReadFile(context.Background(), dir, dir, "HEAD", ".flux-diff.yaml")
	if !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("err = %v, want ErrNotRegularFile", err)
	}
}

func TestReadFile_RejectsUnsafeArguments(t *testing.T) {
	dir := newRepo(t)
	for _, tc := range []struct{ rev, name string }{
		{"--output=x", "a"}, {"", "a"}, {"HEAD", "../a"}, {"HEAD", "/etc/passwd"}, {"HEAD", "-a"}, {"HEAD", ""},
	} {
		if _, err := ReadFile(context.Background(), dir, dir, tc.rev, tc.name); err == nil {
			t.Errorf("ReadFile(%q, %q) succeeded, want an error", tc.rev, tc.name)
		}
	}
}

func TestReadFile_UnknownRevisionFails(t *testing.T) {
	dir := newRepo(t)
	if _, err := ReadFile(context.Background(), dir, dir, "deadbeef", ".flux-diff.yaml"); err == nil {
		t.Fatal("want an error for an unknown revision")
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadFile_OversizeBlobFailsPromptly(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, ".flux-diff.yaml", strings.Repeat("# FAKE-DO-NOT-USE padding\n", 200000))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")

	// The outer context is long on purpose: only the kill after the limit may
	// end the call quickly, not the context deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	start := time.Now()
	_, err := ReadFile(ctx, dir, dir, "HEAD", ".flux-diff.yaml")
	if err == nil {
		t.Fatal("want an error for a blob over the limit")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("took %v, git was probably left blocked on a full pipe", elapsed)
	}
}

// plantNestedRepos fills sub with a bare-repository look-alike and a nested
// .git whose history holds a weaker configuration.
func plantNestedRepos(t *testing.T, sub string) {
	t.Helper()

	write(t, sub, "HEAD", "ref: refs/heads/main\n")
	for _, name := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(sub, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	nested := filepath.Join(sub, "nested")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, nested, "init", "-q", "-b", "main")
	write(t, nested, ".flux-diff.yaml", "kubeVersion: \"9.9.9\"\n")
	runGit(t, nested, "add", ".")
	runGit(t, nested, "commit", "-q", "-m", "evil")
}

func TestReadFile_UsesTrustedRootNotNestedRepositories(t *testing.T) {
	outer := newRepo(t)
	write(t, outer, "apps/.flux-diff.yaml", "kubeVersion: \"1.30.0\"\n")
	runGit(t, outer, "add", ".")
	runGit(t, outer, "commit", "-q", "-m", "base")
	plantNestedRepos(t, filepath.Join(outer, "apps"))

	for name, dir := range map[string]string{
		"path with a bare look-alike": filepath.Join(outer, "apps"),
		"path with a nested .git":     filepath.Join(outer, "apps", "nested"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ReadFile(context.Background(), outer, filepath.Join(outer, "apps"), "HEAD", ".flux-diff.yaml")
			if err != nil || string(got) != "kubeVersion: \"1.30.0\"\n" {
				t.Fatalf("explicit root: %q, %v", got, err)
			}

			// Without a trusted root, discovery must fail closed or find the outer
			// repository, never the look-alike.
			if name == "path with a bare look-alike" {
				root, err := ResolveRoot(context.Background(), dir)
				resolved, _ := filepath.EvalSymlinks(outer)
				if err == nil && root != resolved {
					t.Errorf("root = %s, want the outer repository or an error", root)
				}
			}
		})
	}
}

func TestReadFile_RejectsDirOutsideRoot(t *testing.T) {
	root, other := newRepo(t), t.TempDir()
	if _, err := ReadFile(context.Background(), root, other, "HEAD", ".flux-diff.yaml"); err == nil {
		t.Fatal("want an error for a directory outside the root")
	}
}

func TestReadFile_RejectsPathReachedThroughCommittedSymlink(t *testing.T) {
	root := newRepo(t)
	write(t, root, "real/.flux-diff.yaml", "kubeVersion: \"1.30.0\"\n")
	if err := os.Symlink("real", filepath.Join(root, "clusters")); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "base")

	_, err := ReadFile(context.Background(), root, filepath.Join(root, "clusters"), "HEAD", ".flux-diff.yaml")
	if err == nil {
		t.Fatal("a path reached through a symlink must be rejected")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; the config of the symlink target must not even be looked up", err)
	}

	// The same file stays readable through its real path.
	if _, err := ReadFile(context.Background(), root, filepath.Join(root, "real"), "HEAD", ".flux-diff.yaml"); err != nil {
		t.Errorf("real path: %v", err)
	}
}

func TestReadFile_AcceptsRootSpelledThroughAnAlias(t *testing.T) {
	root := newRepo(t)
	write(t, root, "apps/.flux-diff.yaml", "x: 1\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "base")
	real, _ := filepath.EvalSymlinks(root)

	// root is the resolved top level (as ResolveRoot returns it), dir the alias.
	if _, err := ReadFile(context.Background(), real, filepath.Join(root, "apps"), "HEAD", ".flux-diff.yaml"); err != nil {
		t.Fatalf("aliased dir: %v", err)
	}
}
