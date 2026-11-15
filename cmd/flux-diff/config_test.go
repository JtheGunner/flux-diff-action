package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func recordArgs(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("FAKE_FLATE_ARGS", path)

	return path
}

func readArgs(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("flate was not invoked: %v", err)
	}

	return strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", " ")
}

func TestCLI_Config_GeneratedArgsReachFlate(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)
	repo := t.TempDir()
	writeFile(t, repo, "dummy.yaml", "FAKE-DO-NOT-USE\n")
	writeFile(t, repo, ".flux-diff.yaml", "supply: [dummy.yaml]\napiVersions: [monitoring.coreos.com/v1]\nstripAttr: [example.com/hash]\nextraSkipKinds: [Namespace]\n")

	if _, err := execute(t, "--flate-binary", binary, "--path", repo); err != nil {
		t.Fatalf("run: %v", err)
	}

	got := readArgs(t, argsFile)
	for _, want := range []string{
		"--supply=" + filepath.Join(repo, "dummy.yaml"),
		"--api-versions=monitoring.coreos.com/v1",
		"--strip-attr=chart", "--strip-attr=example.com/hash",
		"--skip-kinds=Secret,CustomResourceDefinition,Namespace",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("flate args %q lack %q", got, want)
		}
	}
}

func TestCLI_Config_ExplicitPathAndMissingFile(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "custom.yaml", "kubeVersion: \"1.30.0\"\n")

	if _, err := execute(t, "--flate-binary", binary, "--config", cfgPath); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := readArgs(t, argsFile); !strings.Contains(got, "--kube-version=1.30.0") {
		t.Errorf("flate args %q lack the configured kube version", got)
	}

	_, err := execute(t, "--flate-binary", binary, "--config", filepath.Join(dir, "absent.yaml"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing explicit --config: error = %v", err)
	}

	if _, err := execute(t, "--flate-binary", binary, "--path", t.TempDir()); err != nil {
		t.Errorf("missing default config must not fail: %v", err)
	}

	bad := writeFile(t, dir, "bad.yaml", "bogus: [x]\n")
	if _, err := execute(t, "--flate-binary", binary, "--config", bad); err == nil || !strings.Contains(err.Error(), "unknown configuration key") {
		t.Errorf("bad config: error = %v", err)
	}
}

func TestCLI_Config_MaskPatternsMaskConfigMapValues(t *testing.T) {
	binary := fakeFlate(t, "configmap-secrets.json", "", "0")
	repo := t.TempDir()
	writeFile(t, repo, ".flux-diff.yaml", "maskPatterns: [client_secret, \"*password*\"]\n")

	for _, outputFormat := range []string{"markdown", "text", "json"} {
		t.Run(outputFormat, func(t *testing.T) {
			out, err := execute(t, "--flate-binary", binary, "--path", repo, "--format", outputFormat)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if strings.Contains(out, "FAKE-DO-NOT-USE") {
				t.Errorf("a ConfigMap value matching maskPatterns leaked into %s output", outputFormat)
			}
			if !strings.Contains(out, "debug") {
				t.Errorf("unmatched value must stay visible in %s output", outputFormat)
			}
		})
	}
}

func TestCLI_RegistryConfigFlagMayBeAbsolute(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)
	repo := t.TempDir()
	outside := writeFile(t, t.TempDir(), "creds.json", "FAKE-DO-NOT-USE")

	if _, err := execute(t, "--flate-binary", binary, "--path", repo, "--registry-config", outside); err != nil {
		t.Fatalf("run: %v", err)
	}

	got := readArgs(t, argsFile)
	if !strings.Contains(got, "--registry-config="+outside) {
		t.Errorf("flate args %q lack the trusted registry config", got)
	}
}

func TestCLI_Config_EscapingPathsAreRejectedBeforeFlateRuns(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)
	repo := t.TempDir()
	writeFile(t, repo, ".flux-diff.yaml", "supply: [../escape-marker.yaml]\n")

	_, err := execute(t, "--flate-binary", binary, "--path", repo)
	if err == nil || !strings.Contains(err.Error(), `key "supply" must not point outside`) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "escape-marker") {
		t.Errorf("error echoes the offending path: %v", err)
	}
	if _, statErr := os.Stat(argsFile); statErr == nil {
		t.Error("flate must not run for a rejected configuration")
	}
}

func TestCLI_Config_InvalidMaskPatternFailsBeforeFlateRuns(t *testing.T) {
	binary := fakeFlate(t, "changes.json", "", "0")
	argsFile := recordArgs(t)
	repo := t.TempDir()
	writeFile(t, repo, ".flux-diff.yaml", "maskPatterns: [\"[unclosed\"]\n")

	_, err := execute(t, "--flate-binary", binary, "--path", repo)
	if err == nil || !strings.Contains(err.Error(), "secret guard") {
		t.Fatalf("error = %v, want a secret guard setup error", err)
	}
	if _, statErr := os.Stat(argsFile); statErr == nil {
		t.Error("flate must not run when the mask patterns are invalid")
	}
}
