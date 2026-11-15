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
//	registryConfig: path           # flate --registry-config (credentials file)
//	kubeVersion: "1.30.0"          # flate --kube-version
//	allowMissingKinds: [Kind, ...] # flate --allow-missing-kinds
//
// Relative paths (supply, registryConfig) are resolved against the repo
// root, which is the directory that holds the configuration file.
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
	RegistryConfig    string
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
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}

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
			return Config{}, fmt.Errorf("line %d: duplicate key %q", key.Line, key.Value)
		}
		seen[key.Value] = true

		if err := assign(&cfg, key, value); err != nil {
			return Config{}, err
		}
	}

	cfg.Supply = resolveAll(root, cfg.Supply)
	cfg.RegistryConfig = resolve(root, cfg.RegistryConfig)

	return cfg, nil
}

func assign(cfg *Config, key, value *yaml.Node) error {
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
		"registryConfig": &cfg.RegistryConfig,
		"kubeVersion":    &cfg.KubeVersion,
	}

	if target, ok := lists[key.Value]; ok {
		items, err := stringList(key, value)
		if err != nil {
			return err
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

	return fmt.Errorf("line %d: unknown key %q", key.Line, key.Value)
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

func resolve(root, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(root, path)
}

func resolveAll(root string, paths []string) []string {
	if paths == nil {
		return nil
	}
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = resolve(root, path)
	}

	return out
}

// flateDefaultStripAttr mirrors flate's built-in --strip-attr defaults. flate
// replaces its defaults as soon as the flag is given once, so they are listed
// explicitly to let the configuration add to them.
var flateDefaultStripAttr = []string{"helm.sh/chart", "checksum/", "app.kubernetes.io/version", "chart"}

// FlateArgs maps the configuration to flate flags. Flags use the --flag=value
// form so a value can never be read as a separate flag. MaskPatterns have no
// flate flag; the caller passes them to the secret guard.
//
// The result never lets flate render Secrets: whenever extra kinds are given,
// Secret leads the --skip-kinds list, and the guard that runs before flate
// adds the remaining required kinds.
func (c Config) FlateArgs() []string {
	var args []string

	for _, path := range c.Supply {
		args = append(args, "--supply="+path)
	}
	if len(c.APIVersions) > 0 {
		args = append(args, "--api-versions="+strings.Join(c.APIVersions, ","))
	}
	if len(c.StripAttr) > 0 {
		for _, attr := range mergeUnique(flateDefaultStripAttr, c.StripAttr) {
			args = append(args, "--strip-attr="+attr)
		}
	}
	for _, field := range c.StripField {
		args = append(args, "--strip-field="+field)
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
