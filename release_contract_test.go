package fluxdiffaction_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// goreleaserFile is the part of .goreleaser.yaml that the action's
// "Install flux-diff" step depends on.
type goreleaserFile struct {
	Builds []struct {
		Env     []string `yaml:"env"`
		Flags   []string `yaml:"flags"`
		Ldflags []string `yaml:"ldflags"`
		Goos    []string `yaml:"goos"`
		Goarch  []string `yaml:"goarch"`
		Binary  string   `yaml:"binary"`
	} `yaml:"builds"`
	Archives []struct {
		Formats      []string `yaml:"formats"`
		NameTemplate string   `yaml:"name_template"`
	} `yaml:"archives"`
	Checksum struct {
		NameTemplate string `yaml:"name_template"`
		Algorithm    string `yaml:"algorithm"`
	} `yaml:"checksum"`
	Release struct {
		Prerelease string `yaml:"prerelease"`
	} `yaml:"release"`
}

func loadGoreleaser(t *testing.T) goreleaserFile {
	t.Helper()

	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg goreleaserFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}

	return cfg
}

func TestGoreleaser_ArchiveNamesMatchTheActionContract(t *testing.T) {
	cfg := loadGoreleaser(t)

	if len(cfg.Archives) != 1 {
		t.Fatalf("want exactly one archive definition, got %d", len(cfg.Archives))
	}
	archive := cfg.Archives[0]
	// goreleaser appends ".tar.gz" to the template; the action downloads
	// flux-diff_<version without v>_<os>_<arch>.tar.gz.
	if archive.NameTemplate != "flux-diff_{{ .Version }}_{{ .Os }}_{{ .Arch }}" {
		t.Errorf("archive name_template = %q", archive.NameTemplate)
	}
	if len(archive.Formats) != 1 || archive.Formats[0] != "tar.gz" {
		t.Errorf("archive formats = %v, want [tar.gz]", archive.Formats)
	}
	if cfg.Checksum.NameTemplate != "checksums.txt" || cfg.Checksum.Algorithm != "sha256" {
		t.Errorf("checksum = %+v, want checksums.txt with sha256", cfg.Checksum)
	}

	_, action := loadAction(t)
	for _, needle := range []string{`asset="flux-diff_${INPUT_VERSION#v}_${os}_${arch}.tar.gz"`, "checksums.txt"} {
		if !strings.Contains(action, needle) {
			t.Errorf("action.yml no longer contains %q; update the release contract together", needle)
		}
	}
}

func TestGoreleaser_BuildsTheSupportedTargetsReproducibly(t *testing.T) {
	cfg := loadGoreleaser(t)

	if len(cfg.Builds) != 1 {
		t.Fatalf("want exactly one build, got %d", len(cfg.Builds))
	}
	build := cfg.Builds[0]
	if build.Binary != "flux-diff" {
		t.Errorf("binary = %q, the action extracts flux-diff by name", build.Binary)
	}
	if strings.Join(build.Env, " ") != "CGO_ENABLED=0" {
		t.Errorf("env = %v, want CGO_ENABLED=0", build.Env)
	}
	if strings.Join(build.Flags, " ") != "-trimpath" {
		t.Errorf("flags = %v, want -trimpath", build.Flags)
	}
	if strings.Join(build.Ldflags, " ") != "-s -w -X main.version={{.Version}}" {
		t.Errorf("ldflags = %v", build.Ldflags)
	}
	if strings.Join(build.Goos, ",") != "linux,darwin" || strings.Join(build.Goarch, ",") != "amd64,arm64" {
		t.Errorf("targets = %v x %v", build.Goos, build.Goarch)
	}
	if cfg.Release.Prerelease != "auto" {
		t.Errorf("release.prerelease = %q, want auto so -rc tags are pre-releases", cfg.Release.Prerelease)
	}
}
