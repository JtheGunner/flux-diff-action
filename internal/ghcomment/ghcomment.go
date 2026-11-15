// Package ghcomment keeps exactly one self-updating comment per pull request,
// using only net/http against the GitHub REST API.
//
// The token is only ever sent in the Authorization header. It never appears in
// errors, URLs or output, and response bodies are never echoed into errors.
package ghcomment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	pageSize = 100
	// maxPages caps pagination so a hostile or broken server cannot loop us.
	maxPages = 100
	// maxResponseBytes bounds one API response.
	maxResponseBytes = 32 << 20
	// DefaultMaxSize stays below GitHub's 65,536 character comment limit.
	DefaultMaxSize = 65000

	truncationNotice = "\n\n> [!NOTE]\n> This comment was truncated. The full diff is attached to the workflow run as an artifact.\n"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Outcome says where the body ended up.
type Outcome string

// Possible outcomes of Upsert.
const (
	Created Outcome = "created"
	Updated Outcome = "updated"
	// StepSummary means the comment could not be written (fork PR or missing
	// write permission) and the body went to the step summary file instead.
	StepSummary Outcome = "step-summary"
)

// Config describes the target and the credentials of one Upsert call.
type Config struct {
	// APIURL is the GitHub API base URL, for example https://api.github.com.
	APIURL string
	// Repository is "owner/name".
	Repository string
	// Token is a GitHub token with pull-requests: write.
	Token string
	// MaxSize is the maximum body length in characters; DefaultMaxSize when
	// zero or negative.
	MaxSize int
	// Fork skips the API entirely: fork PRs get a read-only token.
	Fork bool
	// StepSummaryPath is the file ($GITHUB_STEP_SUMMARY) that receives the
	// body when the comment cannot be written.
	StepSummaryPath string
	// HTTPClient defaults to a client with a 30 second timeout.
	HTTPClient *http.Client
}

// Result reports what Upsert did.
type Result struct {
	Outcome Outcome
	// Truncated is true when the body was cut to MaxSize; the caller should
	// upload the full diff as an artifact.
	Truncated bool
	// CommentID is set for Created and Updated.
	CommentID int64
}

// StatusError is an unexpected HTTP status from the API. It carries no
// response content.
type StatusError struct {
	Method string
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GitHub API %s request failed with status %d", e.Method, e.Status)
}

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// Upsert creates the comment on pull request pr, or updates the existing
// comment that contains marker. When writing is not possible it appends the
// body to cfg.StepSummaryPath and returns the StepSummary outcome.
func Upsert(ctx context.Context, cfg Config, pr int, marker, body string) (Result, error) {
	api, err := newAPI(cfg)
	if err != nil {
		return Result{}, err
	}
	if pr <= 0 {
		return Result{}, errors.New("pull request number must be positive")
	}
	if marker == "" {
		return Result{}, errors.New("marker must not be empty")
	}
	if !strings.Contains(body, marker) {
		return Result{}, errors.New("body does not contain the marker, so the comment could not be found again")
	}

	body, truncated := limit(body, cfg.MaxSize)

	if cfg.Fork {
		return summarise(cfg, body, truncated)
	}

	existing, err := api.findMarkerComment(ctx, pr, marker)
	if err != nil {
		return fallbackOrError(cfg, body, truncated, err)
	}

	if existing != nil {
		err = api.send(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", cfg.Repository, existing.ID), body, nil)
		if err != nil {
			return fallbackOrError(cfg, body, truncated, err)
		}

		return Result{Outcome: Updated, Truncated: truncated, CommentID: existing.ID}, nil
	}

	var created comment
	err = api.send(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", cfg.Repository, pr), body, &created)
	if err != nil {
		return fallbackOrError(cfg, body, truncated, err)
	}

	return Result{Outcome: Created, Truncated: truncated, CommentID: created.ID}, nil
}

// fallbackOrError turns a permission failure into the step summary fallback.
func fallbackOrError(cfg Config, body string, truncated bool, err error) (Result, error) {
	var status *StatusError
	if errors.As(err, &status) && (status.Status == http.StatusForbidden || status.Status == http.StatusNotFound) {
		return summarise(cfg, body, truncated)
	}

	return Result{}, err
}

func summarise(cfg Config, body string, truncated bool) (Result, error) {
	if cfg.StepSummaryPath == "" {
		return Result{}, errors.New("cannot write the comment and no step summary path was given")
	}

	file, err := os.OpenFile(cfg.StepSummaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("open step summary: %w", err)
	}
	if _, err := io.WriteString(file, body+"\n"); err != nil {
		_ = file.Close()

		return Result{}, fmt.Errorf("write step summary: %w", err)
	}
	if err := file.Close(); err != nil {
		return Result{}, fmt.Errorf("close step summary: %w", err)
	}

	return Result{Outcome: StepSummary, Truncated: truncated}, nil
}

// limit cuts body to at most maxSize characters, keeping the leading marker line
// and ending with a notice. It cuts at a line boundary where possible.
func limit(body string, maxSize int) (string, bool) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	runes := []rune(body)
	if len(runes) <= maxSize {
		return body, false
	}

	room := maxSize - len([]rune(truncationNotice))
	if room < 0 {
		room = 0
	}
	cut := string(runes[:room])
	if idx := strings.LastIndex(cut, "\n"); idx > 0 {
		cut = cut[:idx]
	}

	return cut + truncationNotice, true
}

type api struct {
	cfg    Config
	base   *url.URL
	client *http.Client
}

func newAPI(cfg Config) (*api, error) {
	if !repositoryPattern.MatchString(cfg.Repository) {
		return nil, errors.New("repository must look like owner/name")
	}
	if cfg.Token == "" && !cfg.Fork {
		return nil, errors.New("a GitHub token is required")
	}

	base, err := url.Parse(cfg.APIURL)
	if err != nil || base.Host == "" {
		return nil, errors.New("invalid GitHub API URL")
	}
	if base.Scheme != "https" && (base.Scheme != "http" || !isLoopback(base.Hostname())) {
		return nil, errors.New("the GitHub API URL must use https")
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &api{cfg: cfg, base: base, client: client}, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

func (a *api) findMarkerComment(ctx context.Context, pr int, marker string) (*comment, error) {
	for page := 1; page <= maxPages; page++ {
		path := fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=%d&page=%d", a.cfg.Repository, pr, pageSize, page)

		var comments []comment
		if err := a.do(ctx, http.MethodGet, path, nil, &comments); err != nil {
			return nil, err
		}
		for i := range comments {
			if strings.Contains(comments[i].Body, marker) {
				return &comments[i], nil
			}
		}
		if len(comments) < pageSize {
			return nil, nil
		}
	}

	return nil, errors.New("too many comments to search for the marker")
}

func (a *api) send(ctx context.Context, method, path, body string, out any) error {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("encode comment: %w", err)
	}

	return a.do(ctx, method, path, payload, out)
}

func (a *api) do(ctx context.Context, method, path string, payload []byte, out any) error {
	target := strings.TrimRight(a.base.String(), "/") + path

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return errors.New("build GitHub API request")
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "flux-diff")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		// The transport error quotes the URL; report only that the call failed.
		return fmt.Errorf("GitHub API %s request failed: %w", method, ctxOrGeneric(ctx))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Method: method, Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return errors.New("read GitHub API response")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("GitHub API returned an unexpected response")
	}

	return nil
}

func ctxOrGeneric(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return errors.New("network error")
}
