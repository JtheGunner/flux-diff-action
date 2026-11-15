# flux-diff — project instructions

Go CLI (`flux-diff`) plus a thin GitHub Action. It runs the [flate](https://github.com/home-operations/flate) CLI as a **separate process** to render a Flux/Kustomize repository at a base revision and the working tree, masks secrets in the result, and posts the diff as one self-updating PR comment. It does not render or diff manifests itself. Only deltas from the global rules in `~/.claude/rules/` live here.

## Invariants (never break these)

- **Masking before any sink.** Secret values are masked in the result model before formatting, logging, error construction, or artifact writing. No code path may print an unmasked value. A test must fail if a known fixture secret appears in any output format.
- **flate always runs with `Secret` skipped.** `guard.EnforceSkipKinds` forces `--skip-kinds Secret` (and `CustomResourceDefinition`) and rejects any configuration or environment that would weaken it.
- **flate stays a separate process (AGPL-3.0).** Never import, link, vendor or bundle flate code. flux-diff is MIT.
- **Never decrypt SOPS.** Encrypted inputs are left unresolved and the unit is reported as partial.
- **No silent partial results.** A unit flate cannot fully render is reported as `partial` or `failed` with a reason; it is never dropped or shown as "no changes". Known upstream limit: flate stays silent for a unit that is broken identically in base and head (see README, Limits).
- **Trusted configuration only.** The Action reads `.flux-diff.yaml` from the base revision, never from the PR head. Registry credentials are accepted only through `--registry-config`, never from the config file.
- **Never touch the user's working tree.** Other revisions are read through git, never checked out in place.
- **Verified downloads.** flate archives are checked against `internal/flate/checksums.txt` before extraction; flux-diff archives against the release `checksums.txt`.
- **Fixtures use fake secrets only**, clearly marked (e.g. `FAKE-DO-NOT-USE`).

## Layout

```text
cmd/flux-diff/        CLI entrypoint and subcommands (fetch-flate, event, comment)
internal/flate/       run the flate CLI, parse its JSON/stderr, checksum-verified download
internal/guard/       skip-kinds enforcement, secret masking, MaskString
internal/config/      .flux-diff.yaml parsing and mapping to flate flags
internal/format/      markdown / text / json output, truncation
internal/ghcomment/   single self-updating PR comment, fork fallback
internal/ghevent/     pull_request event payload
internal/gitrev/      read files from git revisions
internal/model/       shared result types
action.yml            GitHub Action (composite)
.goreleaser.yaml      release build; contract with action.yml, guarded by release_contract_test.go
testdata/             flate output samples, golden files, e2e fixture
```

## Conventions

- Run before handing off: `gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test ./...`.
- Golden tests: update with `go test ./... -update`, then review the golden diff by hand before committing.
- **Bumping flate** (`flate.PinnedVersion`, `MinVersion`, the `flate-version` default in `action.yml`, and the checksum lines in `internal/flate/checksums.txt`) is its own change. Re-verify digests as described in `checksums.txt`, refresh the samples in `testdata/flate`, and run the tests against the real binary.
- `action.yml` rules: no `${{ }}` expression inside a `run:` script (use `env:`), `GITHUB_TOKEN` only in the comment step, third-party actions pinned to a full commit SHA.
- Release asset names are a contract between `action.yml` and `.goreleaser.yaml`; change both together. Release steps are in `docs/release.md`.
- PR-comment and CLI output is developer-facing: English.
- Go package names are single lowercase words; files are `snake_case.go`.

## Workflow

- Work is tracked as YouTrack tickets (use the `youtrack-task` skill).
- `docs/superpowers/` (design and plan) is intentionally **not versioned**: it is excluded via `.git/info/exclude`. Never `git add -f` it. Those documents predate the decision to wrap flate and describe an own render engine; the code and this file are authoritative.
- Public repo: keep README, LICENSE and CHANGELOG.md current.
