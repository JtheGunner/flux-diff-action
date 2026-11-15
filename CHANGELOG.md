# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- The Markdown comment folds several `SOPSUnreadable` or `ExternalSource` notices
  into one collapsed block per code with a counter. Failures and other warnings
  stay individual; text and JSON output are unchanged.

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

[Unreleased]: https://github.com/JtheGunner/flux-diff-action/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/JtheGunner/flux-diff-action/releases/tag/v0.1.0
