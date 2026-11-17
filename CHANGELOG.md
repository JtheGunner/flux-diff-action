# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `flate-timeout` input and `--flate-timeout` flag (default 10 minutes) limit the
  run time of each flate process. A hung flate is killed and the comment reports
  the run as failed with the reason, instead of the job hanging until its own
  time limit with no comment.

## [0.2.0] - 2026-11-16

### Added

- Units that fail identically in base and head are now named in the comment as
  "Already broken in base and head", with the units they block. The reason is
  intentionally not shown, because its text cannot be masked for a unit that has
  no objects in the diff. flux-diff finds them with one extra `flate build` of
  the head (Secrets skipped) and keeps the failures that flate's diff did not
  report. The notice does not change the exit code; `--strict` turns it into a
  failure like any other partial result. Text and JSON output carry it too
  (`BrokenInBothSides`). If `flate build` exits 1 without a failure line in a
  known format, the run fails with a fixed error instead of looking clean.

### Changed

- The Markdown comment folds several `SOPSUnreadable` or `ExternalSource` notices
  into one collapsed block per code with a counter. Failures and other warnings
  stay individual; text and JSON output are unchanged.
- Keys matched by `maskPatterns` stay readable in the diff; only their value is
  masked. Previously the key name was often replaced by `<masked>` as well.
  A secret value of at least 8 characters is still removed from key names
  (plain, base64, JSON-escaped); a key equal to a secret value is masked at any
  length. Shorter values are not matched inside key names.
- A numeric secret value is masked wherever the number appears, including
  values shorter than 8 digits.

## [0.1.0] - 2026-11-15

First release.

### Added

- `flux-diff` CLI that runs the [flate](https://github.com/home-operations/flate)
  CLI as a separate process at a base revision and the working tree and renders
  the result as Markdown, text or JSON.
- GitHub Action (composite) that posts the diff as one pull request comment and
  edits that comment on every push.
- Comment truncation at a configurable size, with the full report uploaded as
  the `flux-diff-full` artifact.
- Fork pull requests get the diff in the job's step summary instead of a
  comment.
- `.flux-diff.yaml` repository configuration (`supply`, `apiVersions`,
  `stripAttr`, `stripField`, `extraSkipKinds`, `maskPatterns`, `kubeVersion`,
  `allowMissingKinds`), read from the base revision by the Action.
- `flux-diff fetch-flate`, `event` and `comment` subcommands used by the Action.
- Release pipeline: tag-triggered builds for linux/amd64, linux/arm64 and
  darwin/arm64 with `checksums.txt` and build provenance attestations.
- Example pull request workflow in `examples/workflow.yaml`.

### Security

- Secret guard: flate always runs with `Secret` skipped, secret values and
  values matching `maskPatterns` are masked as `<masked>` or
  `<masked, changed>` before any output is written.
- SOPS-encrypted inputs are never decrypted.
- flate downloads are verified against SHA-256 digests embedded in flux-diff;
  flux-diff downloads are verified against the release's `checksums.txt`.
- The Action rejects `pull_request_target`, requires an explicit release tag,
  and exposes `GITHUB_TOKEN` only to the comment step.

[Unreleased]: https://github.com/JtheGunner/flux-diff-action/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/JtheGunner/flux-diff-action/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/JtheGunner/flux-diff-action/releases/tag/v0.1.0
