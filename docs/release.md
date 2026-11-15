# Releasing flux-diff

This page describes how a flux-diff release is cut and verified. Pushing a tag
is a manual, maintainer-only step.

## Tag naming

- Releases use `vMAJOR.MINOR.PATCH`, for example `v0.1.0`.
- Release candidates add a pre-release suffix, for example `v0.1.0-rc.1`.
  GoReleaser marks every tag that contains `-` as a GitHub pre-release.
- The composite action accepts both forms for its `version` input
  (`v0.1.0` and `v0.1.0-rc.1`), so a release candidate can be exercised end to
  end before the final tag is cut.

## What the release workflow produces

Pushing a tag that matches `v*` runs `.github/workflows/release.yaml`. It
builds with GoReleaser (version pinned in the workflow) and publishes to the
GitHub release of the tag:

| Asset | Content |
|---|---|
| `flux-diff_<version>_linux_amd64.tar.gz` | `flux-diff`, `LICENSE`, `README.md` |
| `flux-diff_<version>_linux_arm64.tar.gz` | same, for arm64 |
| `flux-diff_<version>_darwin_arm64.tar.gz` | same, for Apple silicon |
| `checksums.txt` | `<sha256>  <asset file name>` for each archive |

`<version>` is the tag without the leading `v`. The action downloads exactly
these names, so they must not change.

The workflow also creates a signed build provenance attestation for the
archives and `checksums.txt`.

## Before tagging

1. Make sure `main` is green and contains everything for the release.
2. Finish the changelog: rename `## [Unreleased]` content into a new
   `## [<version>] - <YYYY-MM-DD>` section, leave an empty `## [Unreleased]`
   above it, and add the comparison links at the bottom of `CHANGELOG.md`.
3. Optionally rehearse the build locally. Nothing is published:

   ```sh
   goreleaser check
   goreleaser release --snapshot --clean --skip=publish
   ```

   Inspect `dist/`: three archives and `checksums.txt`.
4. Tag and push the tag:

   ```sh
   git tag -s v0.1.0-rc.1 -m "v0.1.0-rc.1"
   git push origin v0.1.0-rc.1
   ```

## Verifying a release

Download the assets of the release into an empty directory, then check the
checksums:

```sh
gh release download v0.1.0 --repo JtheGunner/flux-diff-action --dir flux-diff-release
cd flux-diff-release
sha256sum -c checksums.txt          # on macOS: shasum -a 256 -c checksums.txt
```

Verify the build provenance of an archive and of the checksum file:

```sh
gh attestation verify flux-diff_0.1.0_linux_amd64.tar.gz --repo JtheGunner/flux-diff-action
gh attestation verify checksums.txt --repo JtheGunner/flux-diff-action
```

A matching checksum only proves the file was not corrupted; the attestation
proves it was built by this repository's release workflow.

## End-to-end acceptance

After a release exists, run the manual checks in
[e2e-acceptance.md](e2e-acceptance.md) in a sandbox repository. Reference the
action by the full commit SHA of the tagged commit and set `version` to the
release tag. A first release is accepted when the acceptance steps pass with
that tag.
