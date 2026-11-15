<div align="center">

# 🔍 flux-diff

**A readable, secret-safe Flux manifest diff, posted as a pull request comment.**

[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27-3776ab?style=flat-square&logo=go&logoColor=white)](go.mod)
[![Status](https://img.shields.io/badge/status-early%20development-f59e0b?style=flat-square)](#-github-action)

</div>

---

## 📖 What it is

`flux-diff` is a small Go CLI and GitHub Action built around the
[flate](https://github.com/home-operations/flate) CLI. It compares the Flux
manifests of a pull request against its base branch and posts the result as a
PR comment, with secret values kept out of the output.

---

## 🚀 GitHub Action

```yaml
name: flux-diff
on: pull_request   # pull_request_target is not supported

permissions:
  contents: read
  pull-requests: write

jobs:
  diff:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.sha }}   # the PR head, not the merge commit
          fetch-depth: 0                                    # the merge base needs the history
          persist-credentials: false

      - uses: JtheGunner/flux-diff-action@<full commit sha>
        with:
          version: v1.0.0          # flux-diff release tag; required, "latest" is rejected
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

> [!IMPORTANT]
> `.flux-diff.yaml` is read from the **base revision**, never from the pull
> request head. A PR cannot weaken its own checks by editing the file.

- One comment per PR: it is found by a hidden marker and edited on every push.
- **Fork PRs** have a read-only token. They get the diff in the job's step
  summary instead of a comment.
- `pull_request_target` is **not supported**: it would run with a write token
  next to untrusted code.
- The comment only contains masked values. Secret values never reach the
  comment, the job log or the artifact.
- flate is licensed under AGPL-3.0; flux-diff only runs it as a separate
  process and never links it.

### 🔐 Trust model of the downloads

- **flate** is checked against SHA-256 digests that are embedded in the
  flux-diff binary. They ship with the action's own code, so they are covered
  by whatever you pin.
- **flux-diff** is downloaded from the release of this repository and checked
  against the `checksums.txt` of the same release. That detects corruption and
  mixed-up assets, but not a compromised release: whoever can replace the
  archive can replace the checksum file too.
- The stronger control is to pin the action to a **full commit SHA**, as in the
  example above, and to use a release `version` you have reviewed. Release
  attestation (signed provenance) can be added in the release pipeline to
  close the gap further.

See [docs/e2e-acceptance.md](docs/e2e-acceptance.md) for a manual acceptance run.

---

## 🖥️ CLI

```sh
go build ./cmd/flux-diff
./flux-diff --base main --path .                  # diff, Markdown on stdout
./flux-diff event                                 # pr, head, base, fork of a pull_request event
./flux-diff comment --pr 7 --body-file body.md    # needs GITHUB_TOKEN, GITHUB_REPOSITORY
./flux-diff fetch-flate --dest ./bin              # checksum-verified flate download
```

---

## 🤝 Contributing

Issues and pull requests are welcome. For local development run:

```sh
go test ./...
```

---

## 📄 License

Released under the [MIT License](LICENSE).

---

<div align="center"><sub>Built for readable GitOps reviews.</sub></div>
