package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JtheGunner/flux-diff-action/internal/flate"
	"github.com/JtheGunner/flux-diff-action/internal/guard"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()

	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestLoad_ValidatesSchemaAndRejectsUnknownKeys(t *testing.T) {
	cases := map[string]struct {
		yaml    string
		wantErr string
	}{
		"unknown key":    {"skipKinds: [Secret]\n", `unknown key "skipKinds"`},
		"list is string": {"supply: FAKE-DO-NOT-USE.yaml\n", `key "supply" must be a list`},
		"number item":    {"stripAttr: [1]\n", `key "stripAttr" must hold strings`},
		"unquoted kube":  {"kubeVersion: 1.30\n", `key "kubeVersion" must hold strings`},
		"empty entry":    {"maskPatterns: ['']\n", "empty entries"},
		"duplicate key":  {"kubeVersion: '1'\nkubeVersion: '2'\n", `duplicate key "kubeVersion"`},
		"top level list": {"- a\n", "top level must be a mapping"},
		"two documents":  {"kubeVersion: '1'\n---\nkubeVersion: '2'\n", "multiple YAML documents"},
		"broken yaml":    {"supply: [a\n", "not valid YAML"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, t.TempDir(), tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "FAKE-DO-NOT-USE") {
				t.Errorf("error echoes a configuration value: %v", err)
			}
		})
	}

	valid := `
supply: [secrets/dummy.yaml]
apiVersions: [monitoring.coreos.com/v1]
stripAttr: [example.com/hash]
stripField: [spec.replicas]
extraSkipKinds: [Namespace]
maskPatterns: [client_secret, "*password*"]
registryConfig: config.json
kubeVersion: "1.30.0"
allowMissingKinds: [ExternalSecret]
`
	validRoot := t.TempDir()
	touch(t, validRoot, "secrets/dummy.yaml")
	touch(t, validRoot, "config.json")
	cfg, err := Load(writeConfig(t, validRoot, valid))
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if cfg.KubeVersion != "1.30.0" || !reflect.DeepEqual(cfg.MaskPatterns, []string{"client_secret", "*password*"}) ||
		!reflect.DeepEqual(cfg.AllowMissingKinds, []string{"ExternalSecret"}) || cfg.RegistryConfig != filepath.Join(validRoot, "config.json") {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	missing := filepath.Join(t.TempDir(), FileName)

	cfg, err := Load(missing)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Errorf("want defaults, got %+v", cfg)
	}
	if args := cfg.FlateArgs(); len(args) != 0 {
		t.Errorf("defaults must add no flags, got %v", args)
	}

	if _, err := LoadRequired(missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("LoadRequired error = %v, want ErrNotFound", err)
	}
}

func TestLoad_EmptyFile_ReturnsDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, t.TempDir(), "# nothing yet\n"))
	if err != nil || !reflect.DeepEqual(cfg, Config{}) {
		t.Errorf("got %+v, %v", cfg, err)
	}
}

func TestLoad_RelativePathsResolvedAgainstRepoRoot(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "secrets/a.yaml")
	touch(t, root, "b.yaml")
	touch(t, root, ".docker/config.json")
	path := writeConfig(t, root, "supply: [secrets/a.yaml, ./sub/../b.yaml]\nregistryConfig: .docker/config.json\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantSupply := []string{filepath.Join(root, "secrets/a.yaml"), filepath.Join(root, "b.yaml")}
	if !reflect.DeepEqual(cfg.Supply, wantSupply) {
		t.Errorf("Supply = %v, want %v", cfg.Supply, wantSupply)
	}
	if want := filepath.Join(root, ".docker/config.json"); cfg.RegistryConfig != want {
		t.Errorf("RegistryConfig = %q, want %q", cfg.RegistryConfig, want)
	}
}

func TestFlateArgs_MapsSupplyAPIVersionsAndStrip(t *testing.T) {
	cfg := Config{
		Supply:            []string{"/r/a.yaml", "/r/b.yaml"},
		APIVersions:       []string{"monitoring.coreos.com/v1", "x/v1"},
		StripAttr:         []string{"example.com/hash", "chart"},
		StripField:        []string{"spec.replicas"},
		RegistryConfig:    "/r/config.json",
		KubeVersion:       "1.30.0",
		AllowMissingKinds: []string{"A", "B"},
	}

	want := []string{
		"--supply=/r/a.yaml", "--supply=/r/b.yaml",
		"--api-versions=monitoring.coreos.com/v1,x/v1",
		"--strip-attr=helm.sh/chart", "--strip-attr=checksum/", "--strip-attr=app.kubernetes.io/version",
		"--strip-attr=chart", "--strip-attr=example.com/hash",
		"--strip-field=spec.replicas",
		"--registry-config=/r/config.json",
		"--kube-version=1.30.0",
		"--allow-missing-kinds=A,B",
	}
	if got := cfg.FlateArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("FlateArgs =\n%v\nwant\n%v", got, want)
	}
}

func TestFlateArgs_NeverRemovesSecretFromSkipKinds(t *testing.T) {
	for _, extra := range [][]string{{"Namespace"}, {"ConfigMap", "CustomResourceDefinition"}, {"Deployment"}} {
		args := Config{ExtraSkipKinds: extra}.FlateArgs()

		if !strings.HasPrefix(args[0], "--skip-kinds=Secret,") {
			t.Errorf("extra %v: Secret does not lead the list: %q", extra, args[0])
		}
		enforced, err := guard.EnforceSkipKinds(args)
		if err != nil {
			t.Fatalf("extra %v: guard rejects the generated args %v: %v", extra, args, err)
		}
		if !strings.HasPrefix(enforced[0], "--skip-kinds=Secret,CustomResourceDefinition") {
			t.Errorf("extra %v: enforced skip-kinds = %q", extra, enforced[0])
		}
	}
}

func touch(t *testing.T, root, rel string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("FAKE-DO-NOT-USE"), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func TestLoad_PathsMustStayInsideRepoRoot(t *testing.T) {
	outside := t.TempDir()
	touch(t, outside, "creds.json")
	creds := filepath.Join(outside, "creds.json")

	cases := map[string]struct {
		setup   func(t *testing.T, root string)
		yaml    string
		wantErr string
		secret  string
	}{
		"supply traversal":   {nil, "supply: [../escape-marker.yaml]\n", `key "supply" must not point outside`, "escape-marker"},
		"supply nested dots": {nil, "supply: [a/../../escape-marker.yaml]\n", `key "supply" must not point outside`, "escape-marker"},
		"supply absolute":    {nil, "supply: [" + creds + "]\n", `key "supply" must use paths relative`, "creds.json"},
		"registry traversal": {nil, "registryConfig: ../escape-marker.json\n", `key "registryConfig" must not point outside`, "escape-marker"},
		"registry absolute":  {nil, "registryConfig: /etc/escape-marker.json\n", `key "registryConfig" must use paths relative`, "escape-marker"},
		"supply missing":     {nil, "supply: [missing-marker.yaml]\n", `key "supply" refers to a file that does not exist`, "missing-marker"},
		"supply symlink": {func(t *testing.T, root string) {
			symlink(t, creds, filepath.Join(root, "link-marker.yaml"))
		}, "supply: [link-marker.yaml]\n", `key "supply" must not point outside`, "link-marker"},
		"registry dir symlink": {func(t *testing.T, root string) {
			symlink(t, outside, filepath.Join(root, "linkdir-marker"))
		}, "registryConfig: linkdir-marker/creds.json\n", `key "registryConfig" must not point outside`, "linkdir-marker"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, root)
			}

			_, err := Load(writeConfig(t, root, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			// The error may name the configuration file itself, never the entry.
			if strings.Contains(strings.ReplaceAll(err.Error(), filepath.Join(root, FileName), ""), tc.secret) {
				t.Errorf("error echoes the offending path: %v", err)
			}
		})
	}
}

func TestLoad_SymlinkInsideRootIsAllowed(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "real/a.yaml")
	symlink(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))

	cfg, err := Load(writeConfig(t, root, "supply: [alias/a.yaml]\n"))
	if err != nil || len(cfg.Supply) != 1 {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}

// Guards the hand-copied flate defaults: bumping the pinned flate version must
// be accompanied by re-checking flateDefaultStripAttr.
func TestFlateDefaults_CheckedAgainstPinnedVersion(t *testing.T) {
	if FlateDefaultsCheckedAgainst != flate.PinnedVersion {
		t.Errorf("flate.PinnedVersion is %s but the strip-attr defaults were checked against %s: re-run flate diff --help, compare with flateDefaultStripAttr, then update FlateDefaultsCheckedAgainst",
			flate.PinnedVersion, FlateDefaultsCheckedAgainst)
	}
}
