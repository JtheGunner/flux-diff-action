package flate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type tarEntry struct {
	name string
	body string
	mode int64
}

func buildArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// fetchFixture serves archive and returns options whose checksum table lists
// wantDigestOf (the archive the table should accept).
func fetchFixture(t *testing.T, served, accepted []byte) FetchOptions {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v9.9.9/flate_9.9.9_linux_amd64.tar.gz" {
			http.NotFound(w, r)

			return
		}
		_, _ = w.Write(served)
	}))
	t.Cleanup(server.Close)

	sum := sha256.Sum256(accepted)
	table := fmt.Sprintf("v9.9.9 linux amd64 %s flate_9.9.9_linux_amd64.tar.gz\n", hex.EncodeToString(sum[:]))

	return FetchOptions{
		Version: "v9.9.9", Dest: filepath.Join(t.TempDir(), "bin"), DownloadBase: server.URL,
		GOOS: "linux", GOARCH: "amd64", Checksums: table,
	}
}

func TestFetch_GoodArchiveIsExtractedExecutable(t *testing.T) {
	archive := buildArchive(t,
		tarEntry{"README.md", "docs", 0o644},
		tarEntry{"flate_9.9.9/flate", "FAKE-BINARY", 0o600},
	)
	opts := fetchFixture(t, archive, archive)

	path, err := Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if path != filepath.Join(opts.Dest, "flate") {
		t.Errorf("path = %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(path); string(data) != "FAKE-BINARY" {
		t.Errorf("content = %q", data)
	}

	entries, _ := os.ReadDir(opts.Dest)
	if len(entries) != 1 {
		t.Errorf("destination holds %d entries, want only the executable", len(entries))
	}
}

func TestFetch_TamperedArchiveIsRejectedBeforeExtraction(t *testing.T) {
	good := buildArchive(t, tarEntry{"flate", "FAKE-BINARY", 0o755})
	tampered := buildArchive(t, tarEntry{"flate", "EVIL-BINARY", 0o755})
	opts := fetchFixture(t, tampered, good)

	_, err := Fetch(context.Background(), opts)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	if entries, _ := os.ReadDir(opts.Dest); len(entries) != 0 {
		t.Errorf("destination holds %d entries after a rejected download", len(entries))
	}
}

func TestFetch_UnknownVersionIsRejectedWithoutDownload(t *testing.T) {
	archive := buildArchive(t, tarEntry{"flate", "x", 0o755})
	opts := fetchFixture(t, archive, archive)
	opts.Version = "v1.0.0"
	opts.DownloadBase = "http://127.0.0.1:1"

	if _, err := Fetch(context.Background(), opts); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("err = %v, want ErrUnknownVersion", err)
	}

	opts.Version = "latest"
	if _, err := Fetch(context.Background(), opts); err == nil {
		t.Fatal("want an error for a non-semver version")
	}
}

func TestFetch_TraversalEntryIsRejected(t *testing.T) {
	for _, name := range []string{"../flate", "a/../../flate", "/abs/flate"} {
		t.Run(name, func(t *testing.T) {
			archive := buildArchive(t, tarEntry{name, "x", 0o755}, tarEntry{"flate", "ok", 0o755})
			opts := fetchFixture(t, archive, archive)

			if _, err := Fetch(context.Background(), opts); err == nil {
				t.Fatal("want an error")
			}
			if _, err := os.Stat(filepath.Join(opts.Dest, "flate")); err == nil {
				t.Error("an executable was installed from a hostile archive")
			}
		})
	}
}

func TestFetch_ArchiveWithoutExecutableIsRejected(t *testing.T) {
	archive := buildArchive(t, tarEntry{"README.md", "x", 0o644})
	if _, err := Fetch(context.Background(), fetchFixture(t, archive, archive)); err == nil {
		t.Fatal("want an error")
	}
}

func TestFetch_ServerErrorIsReported(t *testing.T) {
	archive := buildArchive(t, tarEntry{"flate", "x", 0o755})
	opts := fetchFixture(t, archive, archive)
	opts.GOARCH = "amd64"
	opts.DownloadBase += "/missing"

	if _, err := Fetch(context.Background(), opts); err == nil {
		t.Fatal("want an error for a 404")
	}
}

func TestEmbeddedChecksums_CoverPinnedVersionOnAllPlatforms(t *testing.T) {
	assets, err := EmbeddedAssets()
	if err != nil {
		t.Fatalf("embedded checksums are malformed: %v", err)
	}

	found := map[string]bool{}
	for _, asset := range assets {
		if asset.Version == PinnedVersion {
			found[asset.OS+"/"+asset.Arch] = true
		}
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		if !found[platform] {
			t.Errorf("no embedded checksum for %s %s", PinnedVersion, platform)
		}
	}
}
