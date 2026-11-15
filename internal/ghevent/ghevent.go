// Package ghevent reads the pull_request event payload that GitHub Actions
// writes to GITHUB_EVENT_PATH and derives the revisions to compare.
//
// The payload is attacker-influenced (branch names, repository names), so every
// field is validated and errors never echo payload content.
package ghevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/JtheGunner/flux-diff-action/internal/gitrev"
)

// maxPayloadBytes bounds how much of the event file is read.
const maxPayloadBytes = 16 << 20

var (
	errMalformed = errors.New("malformed pull_request event payload")

	shaPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// PullRequest is the validated subset of a pull_request event.
type PullRequest struct {
	Number int
	// BaseRef is the target branch name.
	BaseRef string
	BaseSHA string
	// HeadSHA is the PR head commit, not the synthetic merge commit.
	HeadSHA string
	// HeadRepo is the full name (owner/name) of the head repository; empty
	// when the head repository was deleted.
	HeadRepo string
	// Fork is true when the head lives in another repository than the base,
	// including a deleted head repository.
	Fork bool
}

type payload struct {
	Number      int `json:"number"`
	PullRequest *struct {
		Base ref `json:"base"`
		Head ref `json:"head"`
	} `json:"pull_request"`
}

type ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo *struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

// Load reads and parses the event file at path.
func Load(path string) (PullRequest, error) {
	file, err := os.Open(path)
	if err != nil {
		return PullRequest{}, fmt.Errorf("open event payload: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxPayloadBytes+1))
	if err != nil {
		return PullRequest{}, fmt.Errorf("read event payload: %w", err)
	}
	if len(data) > maxPayloadBytes {
		return PullRequest{}, errors.New("event payload is too large")
	}

	return Parse(data)
}

// Parse validates a pull_request event payload.
func Parse(data []byte) (PullRequest, error) {
	var event payload
	// The decoder error can quote payload bytes, so it is dropped.
	if err := json.Unmarshal(data, &event); err != nil || event.PullRequest == nil {
		return PullRequest{}, errMalformed
	}

	base, head := event.PullRequest.Base, event.PullRequest.Head
	if event.Number <= 0 || !validBranch(base.Ref) || !shaPattern.MatchString(base.SHA) || !shaPattern.MatchString(head.SHA) {
		return PullRequest{}, errMalformed
	}
	if base.Repo == nil || !repoPattern.MatchString(base.Repo.FullName) {
		return PullRequest{}, errMalformed
	}

	pr := PullRequest{
		Number: event.Number, BaseRef: base.Ref, BaseSHA: base.SHA, HeadSHA: head.SHA, Fork: true,
	}
	if head.Repo != nil {
		if !repoPattern.MatchString(head.Repo.FullName) {
			return PullRequest{}, errMalformed
		}
		pr.HeadRepo = head.Repo.FullName
		pr.Fork = head.Repo.FullName != base.Repo.FullName
	}

	return pr, nil
}

func validBranch(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}

	return true
}

// MergeBase returns the best common ancestor of the base and head commits,
// computed in the repository at dir. Both commits must exist locally, so the
// checkout needs the full history (fetch-depth: 0).
func MergeBase(ctx context.Context, dir, baseSHA, headSHA string) (string, error) {
	if !shaPattern.MatchString(baseSHA) || !shaPattern.MatchString(headSHA) {
		return "", errors.New("merge-base needs full commit hashes")
	}

	out, err := gitrev.MergeBase(ctx, dir, baseSHA, headSHA)
	if err != nil {
		return "", errors.New("git merge-base failed: both commits must be present (check out with fetch-depth: 0)")
	}

	sha := out
	if !shaPattern.MatchString(sha) {
		return "", errors.New("git merge-base returned an unexpected value")
	}

	return sha, nil
}
