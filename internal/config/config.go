// Package config reads the optional .flux-diff.yaml of a repository and
// translates it into flate flags and secret-guard settings.
//
// File format (all keys optional, unknown keys are rejected):
//
//	supply: [path, ...]            # flate --supply (paths)
//	apiVersions: [group/v1, ...]   # flate --api-versions
//	stripAttr: [key, ...]          # flate --strip-attr, added to flate's defaults
//	stripField: [a.b.c, ...]       # flate --strip-field
//	extraSkipKinds: [Kind, ...]    # flate --skip-kinds, in addition to Secret
//	maskPatterns: [glob, ...]      # extra key patterns for the secret guard
//	kubeVersion: "1.30.0"          # flate --kube-version
//	allowMissingKinds: [Kind, ...] # flate --allow-missing-kinds
//
// Paths (supply) must be relative to the repo root, which is the directory
// that holds the configuration file. Absolute paths, "..", and symlinks that
// leave the root are rejected because the file is untrusted input. The file
// itself must not be a symlink.
//
// There is deliberately no registryConfig key: a Docker-style credentials file
// can name credential helpers (credsStore, credHelpers) that flate would run
// from PATH, so the file is only accepted through the trusted
// --registry-config CLI flag, which sets Config.RegistryConfig.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the default configuration file name, looked up in the repo root.
const FileName = ".flux-diff.yaml"

// ErrNotFound means a required configuration file does not exist.
var ErrNotFound = errors.New("configuration file not found")

// Config holds the recurring flate settings of a repository.
type Config struct {
	Supply            []string
	APIVersions       []string
	StripAttr         []string
	StripField        []string
	ExtraSkipKinds    []string
	MaskPatterns      []string
	RegistryConfig    string // set by the CLI only, never read from the file
	KubeVersion       string
	AllowMissingKinds []string
}

// Load reads the configuration at path. A missing file is not an error: the
// zero Config (flate's defaults) is returned, so a repo without a file works.
// Use LoadRequired when the user named the file explicitly.
func Load(path string) (Config, error) {
	cfg, err := LoadRequired(path)
	if errors.Is(err, ErrNotFound) {
		return Config{}, nil
	}

	return cfg, err
}

// LoadRequired is like Load but returns an error wrapping ErrNotFound when the
// file does not exist.
func LoadRequired(path string) (Config, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}

		return Config{}, fmt.Errorf("read configuration %s: %w", path, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return Config{}, fmt.Errorf("configuration %s must be a regular file, not a symlink", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration %s: %w", path, err)
	}

	root, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve repo root of %s: %w", path, err)
	}

	cfg, err := parse(data, root)
	if err != nil {
		return Config{}, fmt.Errorf("invalid configuration %s: %w", path, err)
	}

	return cfg, nil
}

// Parse validates configuration bytes that did not come from a file on disk,
// for example the version stored in a base revision. Relative paths resolve
// against root, which must be an existing directory.
func Parse(data []byte, root string) (Config, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration root: %w", err)
	}

	cfg, err := parse(data, absRoot)
	if err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// parse validates the YAML by hand instead of decoding into a struct: the
// decoder's type errors quote the offending value, and a configuration may
// hold credential file paths that must never reach an error message.
func parse(data []byte, root string) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, nil
		}

		return Config{}, fmt.Errorf("not valid YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("multiple YAML documents are not supported")
	}

	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		return Config{}, nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return Config{}, errors.New("the top level must be a mapping of keys")
	}

	var cfg Config
	seen := map[string]bool{}
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i], top.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			return Config{}, fmt.Errorf("line %d: keys must be plain strings", key.Line)
		}
		if seen[key.Value] {
			return Config{}, fmt.Errorf("line %d: duplicate configuration key", key.Line)
		}
		seen[key.Value] = true

		if err := assign(&cfg, root, key, value); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

func assign(cfg *Config, root string, key, value *yaml.Node) error {
	lists := map[string]*[]string{
		"supply":            &cfg.Supply,
		"apiVersions":       &cfg.APIVersions,
		"stripAttr":         &cfg.StripAttr,
		"stripField":        &cfg.StripField,
		"extraSkipKinds":    &cfg.ExtraSkipKinds,
		"maskPatterns":      &cfg.MaskPatterns,
		"allowMissingKinds": &cfg.AllowMissingKinds,
	}
	scalars := map[string]*string{
		"kubeVersion": &cfg.KubeVersion,
	}

	if target, ok := lists[key.Value]; ok {
		items, err := stringList(key, value)
		if err != nil {
			return err
		}
		if key.Value == "supply" {
			resolved, err := confineAll(root, key, value, items)
			if err != nil {
				return err
			}
			items = resolved
		}
		*target = items

		return nil
	}
	if target, ok := scalars[key.Value]; ok {
		text, err := stringScalar(key, value)
		if err != nil {
			return err
		}
		*target = text

		return nil
	}

	return fmt.Errorf("line %d: unknown configuration key", key.Line)
}

func stringList(key, value *yaml.Node) ([]string, error) {
	if isNull(value) {
		return nil, nil
	}
	if value.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("line %d: key %q must be a list of strings", key.Line, key.Value)
	}

	items := make([]string, 0, len(value.Content))
	for _, item := range value.Content {
		text, err := stringScalar(key, item)
		if err != nil {
			return nil, err
		}
		if text == "" {
			return nil, fmt.Errorf("line %d: key %q must not contain empty entries", item.Line, key.Value)
		}
		items = append(items, text)
	}

	return items, nil
}

func stringScalar(key, value *yaml.Node) (string, error) {
	if isNull(value) {
		return "", nil
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return "", fmt.Errorf("line %d: key %q must hold strings (quote version numbers)", value.Line, key.Value)
	}

	return value.Value, nil
}

func isNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

// confineAll resolves every entry of a path list with confine.
func confineAll(root string, key, list *yaml.Node, paths []string) ([]string, error) {
	if paths == nil {
		return nil, nil
	}
	out := make([]string, len(paths))
	for i, path := range paths {
		resolved, err := confine(root, key, list.Content[i], path)
		if err != nil {
			return nil, err
		}
		out[i] = resolved
	}

	return out, nil
}

// confine resolves a repo-relative path against root and rejects everything
// that could reach outside the repository: absolute paths, "..", and symlinks
// that leave the root. The file comes from the PR head and is untrusted, so
// the checks run on the cleaned path and on the symlink-resolved path, and
// errors name only the key and line, never the path.
func confine(root string, key, node *yaml.Node, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("line %d: key %q must use paths relative to the repository root", node.Line, key.Value)
	}
	joined := filepath.Join(root, path)
	if !within(root, joined) {
		return "", fmt.Errorf("line %d: key %q must not point outside the repository root", node.Line, key.Value)
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("line %d: key %q: cannot resolve the repository root", node.Line, key.Value)
	}
	realPath, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("line %d: key %q refers to a file that does not exist or cannot be resolved", node.Line, key.Value)
	}
	if !within(realRoot, realPath) {
		return "", fmt.Errorf("line %d: key %q must not point outside the repository root", node.Line, key.Value)
	}

	return joined, nil
}

// within reports whether path lies inside root (both cleaned, absolute).
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// flateDefaultStripAttr mirrors flate's built-in --strip-attr defaults. flate
// replaces its defaults as soon as the flag is given once, so they are listed
// explicitly to let the configuration add to them.
//
// Verified against "flate diff --help" of the flate version in
// FlateDefaultsCheckedAgainst. Re-check this list whenever flate.PinnedVersion
// changes, then update that constant.
const FlateDefaultsCheckedAgainst = "v0.7.3"

var flateDefaultStripAttr = []string{"helm.sh/chart", "checksum/", "app.kubernetes.io/version", "chart"}

// FlateArgs maps the configuration to flate flags. Flags use the --flag=value
// form so a value can never be read as a separate flag. MaskPatterns have no
// flate flag; the caller passes them to the secret guard.
//
// The result never lets flate render Secrets: whenever extra kinds are given,
// Secret leads the --skip-kinds list, and the guard that runs before flate
// adds the remaining required kinds.
func (c Config) FlateArgs() []string {
	return c.flateArgs(true)
}

// BuildArgs is FlateArgs limited to the flags `flate build` accepts: it has no
// --strip-attr and no --strip-field.
func (c Config) BuildArgs() []string {
	return c.flateArgs(false)
}

func (c Config) flateArgs(withStrip bool) []string {
	var args []string

	for _, path := range c.Supply {
		args = append(args, "--supply="+path)
	}
	if len(c.APIVersions) > 0 {
		args = append(args, "--api-versions="+strings.Join(c.APIVersions, ","))
	}
	if withStrip && len(c.StripAttr) > 0 {
		for _, attr := range mergeUnique(flateDefaultStripAttr, c.StripAttr) {
			args = append(args, "--strip-attr="+attr)
		}
	}
	if withStrip {
		for _, field := range c.StripField {
			args = append(args, "--strip-field="+field)
		}
	}
	if len(c.ExtraSkipKinds) > 0 {
		args = append(args, "--skip-kinds="+strings.Join(mergeUnique([]string{"Secret"}, c.ExtraSkipKinds), ","))
	}
	if c.RegistryConfig != "" {
		args = append(args, "--registry-config="+c.RegistryConfig)
	}
	if c.KubeVersion != "" {
		args = append(args, "--kube-version="+c.KubeVersion)
	}
	if len(c.AllowMissingKinds) > 0 {
		args = append(args, "--allow-missing-kinds="+strings.Join(c.AllowMissingKinds, ","))
	}

	return args
}

func mergeUnique(base, extra []string) []string {
	merged := append([]string(nil), base...)
	for _, item := range extra {
		found := false
		for _, existing := range merged {
			if existing == item {
				found = true

				break
			}
		}
		if !found {
			merged = append(merged, item)
		}
	}

	return merged
}
