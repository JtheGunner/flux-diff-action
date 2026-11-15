package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeToken = "ghs_FAKE-DO-NOT-USE-token"

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", append([]string{
		"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func TestCLI_ConfigBaseRev_IgnoresWeakerHeadConfig(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)

	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, ".flux-diff.yaml", "extraSkipKinds: [Namespace]\nkubeVersion: \"1.30.0\"\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	base := gitIn(t, repo, "rev-parse", "HEAD")

	// The PR head drops the extra skip kind and the kube version.
	writeFile(t, repo, ".flux-diff.yaml", "kubeVersion: \"9.9.9\"\n")

	if _, err := execute(t, "--flate-binary", binary, "--path", repo, "--config-base-rev", base); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := readArgs(t, argsFile)
	for _, want := range []string{"--skip-kinds=Secret,CustomResourceDefinition,Namespace", "--kube-version=1.30.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("flate args %q lack %q from the base config", got, want)
		}
	}
	if strings.Contains(got, "9.9.9") {
		t.Errorf("flate args %q use the head config", got)
	}
}

func TestCLI_ConfigBaseRev_MissingFileMeansDefaults(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)

	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "other.txt", "x")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	writeFile(t, repo, ".flux-diff.yaml", "kubeVersion: \"9.9.9\"\n")

	if _, err := execute(t, "--flate-binary", binary, "--path", repo, "--config-base-rev", "HEAD"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := readArgs(t, argsFile); strings.Contains(got, "kube-version") {
		t.Errorf("flate args %q used a config that does not exist in the base", got)
	}
}

func TestCLI_ConfigBaseRev_RejectsSymlinkAndConflicts(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")

	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "real.yaml", "kubeVersion: \"1.30.0\"\n")
	if err := os.Symlink("real.yaml", filepath.Join(repo, ".flux-diff.yaml")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")

	if _, err := execute(t, "--flate-binary", binary, "--path", repo, "--config-base-rev", "HEAD"); err == nil {
		t.Error("a symlinked config in the base revision was accepted")
	}
	if _, err := execute(t, "--flate-binary", binary, "--path", repo, "--config-base-rev", "HEAD", "--config", "x.yaml"); err == nil {
		t.Error("--config and --config-base-rev were accepted together")
	}
}

func TestCLI_OutputFiles_FullReportOnlyWhenTruncated(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	dir := t.TempDir()
	out, full := filepath.Join(dir, "out.md"), filepath.Join(dir, "full.md")

	stdout, err := execute(t, "--flate-binary", binary, "--output", out, "--full-output", full)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing when --output is set", stdout)
	}
	if _, err := os.Stat(full); err == nil {
		t.Error("full output written although nothing was truncated")
	}
	untruncated, err := os.ReadFile(out)
	if err != nil || len(untruncated) == 0 {
		t.Fatalf("--output file: %v", err)
	}

	if _, err := execute(t, "--flate-binary", binary, "--output", out, "--full-output", full, "--max-size", "450"); err != nil {
		t.Fatalf("run: %v", err)
	}
	cut, _ := os.ReadFile(out)
	fullBody, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("full output missing after truncation: %v", err)
	}
	if len([]rune(string(cut))) > 450 || string(fullBody) != string(untruncated) {
		t.Errorf("cut=%d runes, full body matches untruncated=%v", len([]rune(string(cut))), string(fullBody) == string(untruncated))
	}
}

func TestCLI_Event_PrintsValidatedOutputs(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	writeFile(t, repo, "a", "a")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "a")
	sha := gitIn(t, repo, "rev-parse", "HEAD")

	event := writeFile(t, t.TempDir(), "event.json", `{"number":12,"pull_request":{
		"base":{"ref":"main","sha":"`+sha+`","repo":{"full_name":"o/r"}},
		"head":{"ref":"x","sha":"`+sha+`","repo":{"full_name":"fork/r"}}}}`)

	out, err := execute(t, "event", "--event-path", event, "--repo", repo)
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	want := "pr=12\nhead=" + sha + "\nbase=" + sha + "\nfork=true\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestCLI_Event_RejectsMalformedPayload(t *testing.T) {
	event := writeFile(t, t.TempDir(), "event.json", `{"number":1,"pull_request":{"base":{"ref":"CANARY\n::set-output"}}}`)

	out, err := execute(t, "event", "--event-path", event, "--repo", t.TempDir())
	if err == nil {
		t.Fatal("want an error")
	}
	if out != "" || strings.Contains(err.Error(), "CANARY") {
		t.Errorf("output %q or error %v echo the payload", out, err)
	}
}

func TestWriteOutputs_RejectsInjection(t *testing.T) {
	for _, value := range []string{"abc\nevil=1", "main", "", "12 ", "true\n"} {
		var out strings.Builder
		if err := writeOutputs(&out, [2]string{"k", value}); err == nil || out.Len() != 0 {
			t.Errorf("value %q was printed", value)
		}
	}
}

func TestCLI_Comment_CreatesCommentAndNeverPrintsToken(t *testing.T) {
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakeToken {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		if r.URL.Path == "/user" {
			w.WriteHeader(http.StatusForbidden)

			return
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")

			return
		}
		var in struct{ Body string }
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &in)
		posted = in.Body
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":42}`)
	}))
	t.Cleanup(server.Close)

	t.Setenv("GITHUB_TOKEN", fakeToken)
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	t.Setenv("GITHUB_API_URL", server.URL)
	body := writeFile(t, t.TempDir(), "body.md", "<!-- flux-diff -->\nhello\n")

	out, err := execute(t, "comment", "--pr", "3", "--body-file", body)
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if out != "outcome=created\ntruncated=false\n" || posted != "<!-- flux-diff -->\nhello\n" {
		t.Errorf("out = %q, posted = %q", out, posted)
	}
	if strings.Contains(out, fakeToken) {
		t.Error("output contains the token")
	}
}

func TestCLI_Comment_ForkWritesStepSummaryWithoutToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	t.Setenv("GITHUB_API_URL", "")
	dir := t.TempDir()
	body := writeFile(t, dir, "body.md", "<!-- flux-diff -->\nfork\n")
	summary := filepath.Join(dir, "summary.md")

	out, err := execute(t, "comment", "--pr", "3", "--body-file", body, "--fork", "--step-summary", summary)
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if out != "outcome=step-summary\ntruncated=false\n" {
		t.Errorf("out = %q", out)
	}
	if data, _ := os.ReadFile(summary); !strings.Contains(string(data), "fork") {
		t.Errorf("summary = %q", data)
	}
}

func TestCLI_Comment_MissingTokenErrorDoesNotLeak(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_REPOSITORY", "octo/repo")
	body := writeFile(t, t.TempDir(), "body.md", "<!-- flux-diff -->\n")

	if _, err := execute(t, "comment", "--pr", "3", "--body-file", body); err == nil {
		t.Fatal("want an error without a token")
	}
}

func TestCLI_FetchFlate_RefusesVersionWithoutChecksum(t *testing.T) {
	_, err := execute(t, "fetch-flate", "--version", "v0.0.1", "--dest", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no embedded checksum") {
		t.Fatalf("err = %v, want a refusal for an unchecksummed version", err)
	}
}
