<div align="center">

# 🔍 flux-diff

**A readable, secret-safe Flux manifest diff, posted as one pull request comment.**

<code>🌿 base revision</code> &nbsp;→&nbsp; <code>🔄 flux-diff + flate</code> &nbsp;→&nbsp; <code>💬 PR comment</code>

[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27-3776ab?style=flat-square&logo=go&logoColor=white)](go.mod)
[![GitHub Action](https://img.shields.io/badge/GitHub-Action-0ea5e9?style=flat-square&logo=githubactions&logoColor=white)](action.yml)
[![Secrets](https://img.shields.io/badge/secrets-masked-8b5cf6?style=flat-square)](#-masking-guarantees)
[![Status](https://img.shields.io/badge/status-pre--release-f59e0b?style=flat-square)](CHANGELOG.md)

</div>

---

## 📖 What it is

`flux-diff` shows what a pull request changes in a Flux/Kustomize repository,
as the rendered manifests rather than the YAML source. It is a small Go CLI plus
a composite GitHub Action. The Action posts the diff as **one comment per pull
request** and edits it on every push.

Rendering is done by the [flate](https://github.com/home-operations/flate) CLI,
which flux-diff runs as a separate process. flux-diff adds what a review needs
around it:

- 💬 a self-updating comment (found by a hidden marker, edited in place)
- ✂️ truncation to the comment size limit, with the full report as an artifact
- 🍴 a step-summary fallback for fork pull requests
- 🛡️ a secret guard that masks values before anything is written
- ⚙️ a repository configuration file, `.flux-diff.yaml`

```text
 pull request            flux-diff                      flate (separate process)
┌────────────┐   ┌──────────────────────────┐   ┌───────────────────────────┐
│ base + head│──▶│ config from base revision │──▶│ render both, Secrets      │
└────────────┘   │ mask, format, truncate    │◀──│ skipped (--skip-kinds)    │
                 └─────────────┬────────────┘   └───────────────────────────┘
                               ▼
                    one PR comment (or step summary)
```

### 🧭 How it differs

| | Tool | Difference |
|---|---|---|
| 🔧 | **flate** | The renderer and differ that flux-diff drives. Used on its own it prints a diff; it does not post or update PR comments, handle forks, or guard secrets for you. |
| 🏢 | **konflate** | A standing review service. flux-diff is a lightweight Action with no service to run: it renders in your CI job and exits. |

---

## 📦 Installation

The GitHub Action downloads and verifies flux-diff itself, so nothing needs
installing for CI use.

For local use, download the archive for your platform from the
[releases page](https://github.com/JtheGunner/flux-diff-action/releases).
Archives are named `flux-diff_<version>_<os>_<arch>.tar.gz` for
`linux/amd64`, `linux/arm64` and `darwin/arm64`, and the release carries a
`checksums.txt`. Verify the archive before using it (see
[docs/release.md](docs/release.md)).

Or build from source with Go:

```sh
go build ./cmd/flux-diff
```

You also need the `flate` executable. `flux-diff fetch-flate` downloads a
checksum-verified copy.

> [!NOTE]
> No release has been published yet. Examples use the placeholder `<version>`.

---

## 🖥️ Local usage

```sh
flux-diff fetch-flate --dest ./bin                       # checksum-verified flate download
flux-diff --flate-binary ./bin/flate --base main --path . # diff, Markdown on stdout
flux-diff --format json --base main                       # text, markdown or json
flux-diff event                                           # pr, head, base, fork of a pull_request event
flux-diff comment --pr 7 --body-file body.md              # needs GITHUB_TOKEN, GITHUB_REPOSITORY
```

Main flags of the diff command:

| | Flag | Meaning |
|---|---|---|
| 🌿 | `--base` | revision to compare against |
| 📁 | `--path` | directory of the Flux manifests (default `.`) |
| 🧾 | `--format` | `markdown` (default), `text` or `json` |
| ⚙️ | `--config`, `--config-base-rev`, `--config-name` | configuration file, or read it from a git revision |
| 🔑 | `--registry-config` | registry credentials file passed to flate |
| ✂️ | `--max-size`, `--output`, `--full-output` | truncation and output files |
| 🚦 | `--strict` | also exit 1 when units are only partially rendered |
| 🔧 | `--flate-binary` | flate executable to run (default `flate` from `PATH`) |

Run `flux-diff --help` for the complete list.

---

## 🚀 GitHub Action

A complete workflow is in [examples/workflow.yaml](examples/workflow.yaml):

```yaml
on: pull_request   # pull_request_target is not supported

permissions:
  contents: read
  pull-requests: write

jobs:
  diff:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4          # pin to a full commit SHA in practice
        with:
          ref: ${{ github.event.pull_request.head.sha }}   # the PR head, not the merge commit
          fetch-depth: 0                                    # the merge base needs the history
          persist-credentials: false

      - uses: JtheGunner/flux-diff-action@<full commit SHA>
        with:
          version: v<version>      # flux-diff release tag; required, "latest" is rejected
          path: .                  # directory of the Flux manifests
```

| | Input | Default | Meaning |
|---|---|---|---|
| 🏷️ | `version` | required | flux-diff release to run; its checksum is verified before use |
| 🧰 | `flate-version` | `v0.7.3` | flate release; only versions with a checksum built into flux-diff |
| 📁 | `path` | `.` | directory of the Flux manifests |
| ⚙️ | `config` | `.flux-diff.yaml` | configuration file name inside `path` |
| 🔀 | `base` | merge base | revision to compare against |
| 🔑 | `registry-config` | none | registry credentials file passed to flate |
| ✂️ | `max-comment-size` | `65000` | longer diffs are truncated and uploaded as the `flux-diff-full` artifact |
| 🚦 | `fail-on-error` | `false` | fail the step when units failed; otherwise only a warning |

The step output `outcome` is `created`, `updated` or `step-summary`.

- **One comment per PR.** It is found by a hidden marker and edited on every push.
- **Fork PRs** have a read-only token. They get the diff in the job's step
  summary instead of a comment.
- **`GITHUB_TOKEN`** is the workflow's default token. It reaches only the step
  that writes the comment, and only needs `pull-requests: write`.
- `pull_request_target` is **not supported**: it would run with a write token
  next to untrusted code.

> [!TIP]
> Pin the action to a full commit SHA, not to a tag or branch.

### 🔐 Trust model of the downloads

- **flate** is checked against SHA-256 digests that are embedded in the
  flux-diff binary. They ship with the action's own code, so they are covered
  by whatever you pin.
- **flux-diff** is downloaded from the release of this repository and checked
  against the `checksums.txt` of the same release. That detects corruption and
  mixed-up assets, but not a compromised release: whoever can replace the
  archive can replace the checksum file too.
- The stronger controls are to pin the action to a **full commit SHA**, to use
  a release `version` you have reviewed, and to verify the build provenance
  attestation of the release (`gh attestation verify`, see
  [docs/release.md](docs/release.md)).

See [docs/e2e-acceptance.md](docs/e2e-acceptance.md) for a manual acceptance run.

---

## ⚙️ Configuration: `.flux-diff.yaml`

All keys are optional and unknown keys are rejected. Each maps to a flate flag
or to the secret guard.

```yaml
supply: [clusters/prod/vars]      # flate --supply (paths relative to the repo root)
apiVersions: [monitoring.coreos.com/v1]
stripAttr: [checksum/config]      # added to flate's default stripped attributes
stripField: [metadata.annotations.example]
extraSkipKinds: [Namespace]       # skipped in addition to Secret
maskPatterns: ["*token*", "*apikey*"]  # extra key globs the guard masks
kubeVersion: "1.30.0"
allowMissingKinds: [Certificate]
```

- `supply` paths must be relative; absolute paths, `..` and symlinks that leave
  the repository are rejected, because the file is untrusted input.
- There is **no** key for registry credentials. A credentials file can name
  helper programs that would run from `PATH`, so it is accepted only through
  `--registry-config` or the Action's `registry-config` input.
- The Action reads the file from the **base revision**, never from the pull
  request head. A PR cannot weaken its own checks by editing the file.

---

## 🛡️ Masking guarantees

- flate always runs with `--skip-kinds Secret` (and `CustomResourceDefinition`).
  A configuration that would render Secrets is rejected, so no Secret object
  reaches the diff.
- Values are masked in the result model before formatting, logging, error
  construction or artifact writing. Masked values appear as `<masked>`, or as
  `<masked, changed>` when the value differs between the revisions.
- `maskPatterns` adds extra key globs for the secret guard; values under
  matching keys are masked too. Matching is case-insensitive.
- flux-diff never decrypts SOPS. Encrypted inputs stay unresolved and the unit
  is reported as partial.

Honest limits:

- **Partial units.** A unit that cannot be fully rendered is reported as
  `partial` or `failed` with a reason. It is never dropped or shown as "no
  changes". Use `--strict` or `fail-on-error` to turn them into failures.
- **SOPS is not diffable.** Only ciphertext is available, so changes inside
  encrypted values cannot be shown.
- **Free-form messages** (warnings, failure reasons, stderr lines) contain
  nothing beyond what flate prints, which names keys and objects but not
  values. They are only as safe as flate's text. A `kind: List` that wraps
  Secrets is not masked structurally; skipping `Secret` in flate is what keeps
  those out.

---

## 🧱 Limits

- Only the `pull_request` event is supported.
- Runners: Linux and macOS, `amd64` or `arm64`.
- Only the working tree at `--path` is compared against `--base`; other head
  revisions are not supported yet.
- flate versions are limited to those with a checksum built into flux-diff.
- A unit that is broken in the same way in the base and the head revision (for
  example a `spec.path` that never existed) is not reported: flate's diff only
  compares the two sides. A unit that a pull request breaks, adds or deletes is
  reported as `failed` with a reason.

---

## 🤝 Contributing

Report bugs and ideas as GitHub issues. For a change, open a pull request
against `main`. Run the checks locally before you do:

```sh
gofmt -l .
go vet ./...
golangci-lint run
go test ./...
```

Golden files update with `go test ./... -update`; review the diff by hand
before committing. Fixtures use fake secrets only, marked `FAKE-DO-NOT-USE`.
Release steps are in [docs/release.md](docs/release.md).

---

## 📝 Changelog

See [CHANGELOG.md](CHANGELOG.md).

---

## 📄 License

flux-diff is released under the [MIT License](LICENSE).

[flate](https://github.com/home-operations/flate) is licensed under
**AGPL-3.0**. flux-diff only runs it as a **separate process**. It does not link
flate, and it does not bundle it: the Action downloads flate at run time and
verifies it against a pinned checksum. Your use of flate is governed by its own
license.

---

<div align="center"><sub>Built for readable GitOps reviews.</sub></div>
