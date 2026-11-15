package flate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// DefaultDownloadBase is the URL prefix of flate release downloads; the asset
// lives at <base>/<version>/<asset>.
const DefaultDownloadBase = "https://github.com/home-operations/flate/releases/download"

const (
	binaryName = "flate"
	// maxArchiveBytes and maxBinaryBytes bound the download and the extracted
	// executable.
	maxArchiveBytes = 512 << 20
	maxBinaryBytes  = 1 << 30
)

//go:embed checksums.txt
var embeddedChecksums string

var (
	// ErrUnknownVersion means no checksum is embedded for the version, so it
	// is refused.
	ErrUnknownVersion = errors.New("no embedded checksum for this flate version and platform")
	// ErrChecksumMismatch means the download does not match the embedded digest.
	ErrChecksumMismatch = errors.New("downloaded archive does not match the embedded checksum")

	versionArgPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	assetPattern      = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// Asset is one checksummed release archive.
type Asset struct {
	Version string
	OS      string
	Arch    string
	SHA256  string
	Name    string
}

// EmbeddedAssets returns the assets whose checksums are built into the binary.
func EmbeddedAssets() ([]Asset, error) {
	return parseChecksums(embeddedChecksums)
}

func parseChecksums(text string) ([]Asset, error) {
	var assets []Asset
	for number, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 5 || !versionArgPattern.MatchString(fields[0]) ||
			!digestPattern.MatchString(fields[3]) || !assetPattern.MatchString(fields[4]) {
			return nil, fmt.Errorf("checksums line %d is malformed", number+1)
		}
		assets = append(assets, Asset{
			Version: fields[0], OS: fields[1], Arch: fields[2], SHA256: fields[3], Name: fields[4],
		})
	}

	return assets, nil
}

// FetchOptions configures Fetch.
type FetchOptions struct {
	// Version is the flate release, for example v0.7.3.
	Version string
	// Dest is the directory that receives the flate executable.
	Dest string
	// DownloadBase overrides DefaultDownloadBase (tests).
	DownloadBase string
	// GOOS and GOARCH default to the running platform.
	GOOS, GOARCH string
	// Checksums overrides the embedded table (tests).
	Checksums string
	// HTTPClient defaults to a client with a 5 minute timeout.
	HTTPClient *http.Client
}

// Fetch downloads the flate release archive for the platform, verifies its
// SHA-256 against the embedded checksum before opening it, and extracts only
// the flate executable into Dest with mode 0755. It returns the path of the
// executable.
func Fetch(ctx context.Context, opts FetchOptions) (string, error) {
	asset, err := selectAsset(opts)
	if err != nil {
		return "", err
	}
	if opts.Dest == "" {
		return "", errors.New("destination directory is required")
	}
	if err := os.MkdirAll(opts.Dest, 0o750); err != nil {
		return "", fmt.Errorf("create destination: %w", err)
	}

	archive, err := download(ctx, opts, asset)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(archive) }()

	return extract(archive, opts.Dest)
}

func selectAsset(opts FetchOptions) (Asset, error) {
	if !versionArgPattern.MatchString(opts.Version) {
		return Asset{}, errors.New("version must look like v1.2.3")
	}

	table := opts.Checksums
	if table == "" {
		table = embeddedChecksums
	}
	assets, err := parseChecksums(table)
	if err != nil {
		return Asset{}, err
	}

	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	for _, asset := range assets {
		if asset.Version == opts.Version && asset.OS == goos && asset.Arch == goarch {
			return asset, nil
		}
	}

	return Asset{}, fmt.Errorf("%w: %s %s/%s", ErrUnknownVersion, opts.Version, goos, goarch)
}

// download streams the archive into a file inside Dest while hashing it and
// removes the file again when the digest differs.
func download(ctx context.Context, opts FetchOptions, asset Asset) (string, error) {
	base := opts.DownloadBase
	if base == "" {
		base = DefaultDownloadBase
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+asset.Version+"/"+asset.Name, nil)
	if err != nil {
		return "", errors.New("build download request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("download flate archive failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download flate archive failed with status %d", resp.StatusCode)
	}

	file, err := os.CreateTemp(opts.Dest, ".flate-download-*")
	if err != nil {
		return "", fmt.Errorf("create download file: %w", err)
	}
	name := file.Name()

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, maxArchiveBytes+1))
	closeErr := file.Close()

	switch {
	case copyErr != nil:
		_ = os.Remove(name)

		return "", errors.New("read flate archive failed")
	case closeErr != nil:
		_ = os.Remove(name)

		return "", fmt.Errorf("write download file: %w", closeErr)
	case written > maxArchiveBytes:
		_ = os.Remove(name)

		return "", errors.New("flate archive exceeds the size limit")
	case hex.EncodeToString(hash.Sum(nil)) != asset.SHA256:
		_ = os.Remove(name)

		return "", ErrChecksumMismatch
	}

	return name, nil
}

// extract writes the flate executable of a verified tar.gz archive to dest.
// Every entry name is checked, even those that are not extracted, so an
// archive with a traversal entry is rejected as a whole.
func extract(archive, dest string) (string, error) {
	file, err := os.Open(archive)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = file.Close() }()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", errors.New("flate archive is not gzip")
	}
	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	var target string
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", errors.New("flate archive is corrupt")
		}
		if !safeEntryName(header.Name) {
			return "", errors.New("flate archive contains an unsafe path")
		}
		if target != "" || header.Typeflag != tar.TypeReg || path.Base(header.Name) != binaryName {
			continue
		}

		target, err = writeBinary(reader, dest)
		if err != nil {
			return "", err
		}
	}

	if target == "" {
		return "", errors.New("flate archive does not contain the flate executable")
	}

	return target, nil
}

func safeEntryName(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") || path.IsAbs(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}

	return true
}

func writeBinary(src io.Reader, dest string) (string, error) {
	tmp, err := os.CreateTemp(dest, ".flate-extract-*")
	if err != nil {
		return "", fmt.Errorf("create executable: %w", err)
	}

	written, copyErr := io.Copy(tmp, io.LimitReader(src, maxBinaryBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > maxBinaryBytes {
		_ = os.Remove(tmp.Name())

		return "", errors.New("extract flate executable failed")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec // the executable must be runnable
		_ = os.Remove(tmp.Name())

		return "", fmt.Errorf("make flate executable: %w", err)
	}

	final := filepath.Join(dest, binaryName)
	if err := os.Rename(tmp.Name(), final); err != nil {
		_ = os.Remove(tmp.Name())

		return "", fmt.Errorf("install flate executable: %w", err)
	}

	return final, nil
}
