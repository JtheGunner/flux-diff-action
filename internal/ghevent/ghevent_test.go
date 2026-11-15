package ghevent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func eventJSON(number int, baseRef, baseSHA, headSHA, baseRepo, headRepo string) string {
	head := "null"
	if headRepo != "" {
		head = fmt.Sprintf(`{"full_name":%q}`, headRepo)
	}

	return fmt.Sprintf(`{"number":%d,"pull_request":{
		"base":{"ref":%q,"sha":%q,"repo":{"full_name":%q}},
		"head":{"ref":"feature","sha":%q,"repo":%s}}}`, number, baseRef, baseSHA, baseRepo, headSHA, head)
}

func git(t *testing.T, dir string, args ...string) string {
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

func commitFile(t *testing.T, dir, name string) string {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", name)

	return git(t, dir, "rev-parse", "HEAD")
}

func TestAction_DerivesBaseAndHeadFromEventPayload(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	fork := commitFile(t, dir, "root")
	baseTip := commitFile(t, dir, "main-1")
	git(t, dir, "checkout", "-q", "-b", "feature", fork)
	headTip := commitFile(t, dir, "feature-1")
	// The synthetic merge commit GitHub checks out by default must not be used.
	git(t, dir, "checkout", "-q", "-b", "merge", baseTip)
	git(t, dir, "merge", "-q", "--no-ff", "-m", "merge", "feature")

	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(eventJSON(7, "main", baseTip, headTip, "o/r", "o/r")), 0o600); err != nil {
		t.Fatal(err)
	}

	pr, err := Load(event)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pr.Number != 7 || pr.HeadSHA != headTip || pr.BaseSHA != baseTip || pr.BaseRef != "main" || pr.Fork {
		t.Fatalf("pr = %+v", pr)
	}

	mergeBase, err := MergeBase(context.Background(), dir, pr.BaseSHA, pr.HeadSHA)
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if mergeBase != fork {
		t.Errorf("merge base = %s, want %s", mergeBase, fork)
	}
}

func TestParse_ForkDetection(t *testing.T) {
	tests := map[string]struct {
		headRepo string
		want     bool
	}{
		"same repository":  {"o/r", false},
		"other repository": {"someone/r", true},
		"deleted fork":     {"", true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pr, err := Parse([]byte(eventJSON(1, "main", shaA, shaB, "o/r", tc.headRepo)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if pr.Fork != tc.want {
				t.Errorf("Fork = %v, want %v", pr.Fork, tc.want)
			}
		})
	}
}

func TestParse_RejectsMalformedPayloadsWithoutEchoingContent(t *testing.T) {
	const canary = "CANARY-FAKE-DO-NOT-USE"
	tests := map[string]string{
		"not json":        canary,
		"no pull_request": `{"number":1}`,
		"zero number":     eventJSON(0, "main", shaA, shaB, "o/r", "o/r"),
		"short sha":       eventJSON(1, "main", "abc", shaB, "o/r", "o/r"),
		"flag-like sha":   eventJSON(1, "main", "--output="+canary, shaB, "o/r", "o/r"),
		"bad repo":        eventJSON(1, "main", shaA, shaB, "o/r", "a b/"+canary),
		"control in ref":  eventJSON(1, "ma\nin"+canary, shaA, shaB, "o/r", "o/r"),
		"wrong type":      `{"number":"` + canary + `","pull_request":{}}`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(data))
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), canary) {
				t.Errorf("error echoes payload content: %v", err)
			}
		})
	}
}

func TestMergeBase_RejectsNonHashArguments(t *testing.T) {
	if _, err := MergeBase(context.Background(), t.TempDir(), "--help", shaB); err == nil {
		t.Fatal("want an error for a non-hash argument")
	}
}
