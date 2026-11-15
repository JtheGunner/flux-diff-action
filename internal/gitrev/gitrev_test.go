package gitrev

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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

	got, err := ReadFile(context.Background(), filepath.Join(dir, "sub"), "HEAD", ".flux-diff.yaml")
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

	_, err := ReadFile(context.Background(), dir, "HEAD", ".flux-diff.yaml")
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

	_, err := ReadFile(context.Background(), dir, "HEAD", ".flux-diff.yaml")
	if !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("err = %v, want ErrNotRegularFile", err)
	}
}

func TestReadFile_RejectsUnsafeArguments(t *testing.T) {
	dir := newRepo(t)
	for _, tc := range []struct{ rev, name string }{
		{"--output=x", "a"}, {"", "a"}, {"HEAD", "../a"}, {"HEAD", "/etc/passwd"}, {"HEAD", "-a"}, {"HEAD", ""},
	} {
		if _, err := ReadFile(context.Background(), dir, tc.rev, tc.name); err == nil {
			t.Errorf("ReadFile(%q, %q) succeeded, want an error", tc.rev, tc.name)
		}
	}
}

func TestReadFile_UnknownRevisionFails(t *testing.T) {
	dir := newRepo(t)
	if _, err := ReadFile(context.Background(), dir, "deadbeef", ".flux-diff.yaml"); err == nil {
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
