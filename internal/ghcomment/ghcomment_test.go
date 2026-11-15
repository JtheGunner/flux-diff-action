package ghcomment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	fakeToken = "ghs_FAKE-DO-NOT-USE-token"
	marker    = "<!-- flux-diff -->"
)

// fakeGitHub is a minimal issue-comment API.
type fakeGitHub struct {
	mu       sync.Mutex
	comments []comment
	nextID   int64
	// writeStatus, when non-zero, answers POST and PATCH with that status.
	writeStatus int
	requests    []string
	authHeaders []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.authHeaders = append(f.authHeaders, r.Header.Get("Authorization"))

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/comments"):
		f.list(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
		f.write(w, r, 0)
	case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/issues/comments/"):
		var id int64
		_, _ = fmt.Sscanf(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "%d", &id)
		f.write(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeGitHub) list(w http.ResponseWriter, r *http.Request) {
	var page int
	_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
	start := (page - 1) * pageSize
	if start > len(f.comments) {
		start = len(f.comments)
	}
	end := min(start+pageSize, len(f.comments))
	_ = json.NewEncoder(w).Encode(f.comments[start:end])
}

func (f *fakeGitHub) write(w http.ResponseWriter, r *http.Request, id int64) {
	if f.writeStatus != 0 {
		w.WriteHeader(f.writeStatus)
		_, _ = io.WriteString(w, `{"message":"Resource not accessible by integration"}`)

		return
	}
	var in struct{ Body string }
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &in)

	if id == 0 {
		f.nextID++
		id = 1000 + f.nextID
		f.comments = append(f.comments, comment{ID: id, Body: in.Body})
		w.WriteHeader(http.StatusCreated)
	} else {
		for i := range f.comments {
			if f.comments[i].ID == id {
				f.comments[i].Body = in.Body
			}
		}
	}
	_ = json.NewEncoder(w).Encode(comment{ID: id, Body: in.Body})
}

func newConfig(t *testing.T, fake *fakeGitHub) Config {
	t.Helper()

	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	return Config{
		APIURL: server.URL, Repository: "octo/repo", Token: fakeToken,
		StepSummaryPath: filepath.Join(t.TempDir(), "summary.md"),
	}
}

func TestUpsert_CreatesWhenNoMarkerComment(t *testing.T) {
	fake := &fakeGitHub{comments: []comment{{ID: 1, Body: "unrelated"}}}
	cfg := newConfig(t, fake)

	result, err := Upsert(context.Background(), cfg, 5, marker, marker+"\nhello")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if result.Outcome != Created || result.Truncated {
		t.Errorf("result = %+v, want created and not truncated", result)
	}
	if len(fake.comments) != 2 || fake.comments[1].Body != marker+"\nhello" {
		t.Errorf("comments = %+v", fake.comments)
	}
	for _, header := range fake.authHeaders {
		if header != "Bearer "+fakeToken {
			t.Errorf("Authorization = %q", header)
		}
	}
}

func TestUpsert_UpdatesExistingMarkerComment(t *testing.T) {
	// 100 unrelated comments force the marker comment onto page two.
	fake := &fakeGitHub{}
	for i := 1; i <= pageSize; i++ {
		fake.comments = append(fake.comments, comment{ID: int64(i), Body: "noise"})
	}
	fake.comments = append(fake.comments, comment{ID: 500, Body: marker + "\nold"})
	cfg := newConfig(t, fake)

	result, err := Upsert(context.Background(), cfg, 5, marker, marker+"\nnew")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if result.Outcome != Updated || result.CommentID != 500 {
		t.Errorf("result = %+v, want updated comment 500", result)
	}
	if len(fake.comments) != pageSize+1 || fake.comments[pageSize].Body != marker+"\nnew" {
		t.Errorf("comment was not updated in place")
	}
	if !strings.Contains(strings.Join(fake.requests, ","), "PATCH /repos/octo/repo/issues/comments/500") {
		t.Errorf("requests = %v", fake.requests)
	}
}

func TestUpsert_ForkPR_NoWritePermission_FallsBackToStepSummary(t *testing.T) {
	t.Run("fork flag skips the API", func(t *testing.T) {
		fake := &fakeGitHub{}
		cfg := newConfig(t, fake)
		cfg.Fork = true

		result, err := Upsert(context.Background(), cfg, 5, marker, marker+"\nfork body")
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if result.Outcome != StepSummary || len(fake.requests) != 0 {
			t.Errorf("result = %+v, requests = %v", result, fake.requests)
		}
		assertSummary(t, cfg.StepSummaryPath, "fork body")
	})

	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprintf("write answered %d", status), func(t *testing.T) {
			fake := &fakeGitHub{writeStatus: status}
			cfg := newConfig(t, fake)

			result, err := Upsert(context.Background(), cfg, 5, marker, marker+"\nread-only body")
			if err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			if result.Outcome != StepSummary {
				t.Errorf("outcome = %s, want step-summary", result.Outcome)
			}
			assertSummary(t, cfg.StepSummaryPath, "read-only body")
		})
	}

	t.Run("fallback without a summary path is an error", func(t *testing.T) {
		cfg := newConfig(t, &fakeGitHub{})
		cfg.Fork = true
		cfg.StepSummaryPath = ""

		if _, err := Upsert(context.Background(), cfg, 5, marker, marker); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestUpsert_BodyOverLimit_UsesTruncatedBodyAndUploadsFullArtifact(t *testing.T) {
	fake := &fakeGitHub{}
	cfg := newConfig(t, fake)
	cfg.MaxSize = 500

	var full strings.Builder
	full.WriteString(marker + "\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&full, "line %d of a very large diff\n", i)
	}

	result, err := Upsert(context.Background(), cfg, 5, marker, full.String())
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !result.Truncated {
		t.Fatal("Truncated = false, the caller must be told to upload the full artifact")
	}
	posted := fake.comments[0].Body
	if len([]rune(posted)) > cfg.MaxSize {
		t.Errorf("posted %d characters, limit %d", len([]rune(posted)), cfg.MaxSize)
	}
	if !strings.HasPrefix(posted, marker) || !strings.Contains(posted, "truncated") {
		t.Errorf("posted body lost the marker or the notice: %q", posted)
	}
}

func TestUpsert_RejectsBodyWithoutMarker(t *testing.T) {
	cfg := newConfig(t, &fakeGitHub{})
	if _, err := Upsert(context.Background(), cfg, 5, marker, "no marker here"); err == nil {
		t.Fatal("want an error")
	}
}

func TestUpsert_TokenNeverAppearsInErrors(t *testing.T) {
	failures := map[string]func(t *testing.T) (Config, string){
		"server error": func(t *testing.T) (Config, string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, "echo "+fakeToken)
			}))
			t.Cleanup(server.Close)

			return Config{APIURL: server.URL, Repository: "octo/repo", Token: fakeToken}, ""
		},
		"unreachable": func(_ *testing.T) (Config, string) {
			return Config{APIURL: "http://127.0.0.1:1", Repository: "octo/repo", Token: fakeToken}, ""
		},
		"garbage response": func(t *testing.T) (Config, string) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "not json "+fakeToken)
			}))
			t.Cleanup(server.Close)

			return Config{APIURL: server.URL, Repository: "octo/repo", Token: fakeToken}, ""
		},
		"plain http to a remote host": func(_ *testing.T) (Config, string) {
			return Config{APIURL: "http://api.example.invalid", Repository: "octo/repo", Token: fakeToken}, ""
		},
		"bad repository": func(_ *testing.T) (Config, string) {
			return Config{APIURL: "https://api.example.invalid", Repository: "../" + fakeToken, Token: fakeToken}, ""
		},
	}

	for name, setup := range failures {
		t.Run(name, func(t *testing.T) {
			cfg, _ := setup(t)

			_, err := Upsert(context.Background(), cfg, 5, marker, marker)
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), fakeToken) || strings.Contains(err.Error(), "ghs_") {
				t.Errorf("error leaks the token: %v", err)
			}
		})
	}
}

func assertSummary(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("step summary not written: %v", err)
	}
	if !strings.Contains(string(data), want) {
		t.Errorf("summary = %q, want it to contain %q", data, want)
	}
	if strings.Contains(string(data), fakeToken) {
		t.Error("summary contains the token")
	}
}
