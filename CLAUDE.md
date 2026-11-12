# flux-diff — project instructions

Go CLI (`flux-diff`) plus a thin GitHub Action that renders a Flux/Kustomize repository at two git revisions and posts a secret-safe manifest diff as a PR comment. Only deltas from the global rules in `~/.claude/rules/` live here.

## Invariants (never break these)

- **Masking before any sink.** Secret values are masked in the diff model before formatting, logging, error construction, or artifact writing. No code path may print an unmasked value. A test must fail if a known fixture secret appears in any output format.
- **Offline and read-only.** No cluster access, no `kubectl`, no apply. Network is allowed only to fetch Helm charts from sources declared in the repo.
- **Never decrypt SOPS.** Encrypted inputs are left unresolved and the unit is reported as partial.
- **No silent partial results.** A unit that cannot be fully rendered is reported as `partial` or `failed` with a reason; it is never dropped or shown as "no changes".
- **Never touch the user's working tree.** Revisions are materialised into temp dirs.
- **Fixtures use fake secrets only**, clearly marked (e.g. `FAKE-DO-NOT-USE`).

## Layout

```text
cmd/flux-diff/        CLI entrypoint
internal/model/       shared types (Unit, Object, Change, Status)
internal/gitrev/      revision materialisation, merge base
internal/discover/    Flux Kustomization/HelmRelease discovery
internal/render/      kustomize + Flux substitution rendering
internal/helm/        HelmRelease rendering
internal/diff/        semantic object diff
internal/mask/        secret masking
internal/normalize/   noise reduction
internal/config/      .flux-diff.yaml
internal/format/      text / markdown / json output
action.yml            GitHub Action (composite)
testdata/             fixture repos and golden files
```

## Conventions

- Run before handing off: `gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test ./...`.
- Golden tests: update with `go test ./... -update`, then review the golden diff by hand before committing.
- Flux and Helm library versions are pinned in `go.mod`; bumping them is its own change with its own test run.
- PR-comment and CLI output is developer-facing: English.
- Go package names are single lowercase words; files are `snake_case.go`.

## Workflow

- Work is tracked as YouTrack tickets (use the `youtrack-task` skill). The ticket breakdown follows the local plan in `docs/superpowers/plans/`.
- `docs/superpowers/` (design and plan) is intentionally **not versioned**: it is excluded via `.git/info/exclude`. Never `git add -f` it.
- Public repo: keep README, LICENSE and (from the first release) CHANGELOG.md current.
