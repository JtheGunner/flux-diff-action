# End-to-end acceptance run

This is a manual check that the GitHub Action works on real pull requests. Run
it in a throw-away sandbox repository after a flux-diff release exists.

## Prerequisites

- A published flux-diff release with the assets `flux-diff_<version>_<os>_<arch>.tar.gz`
  (version without the leading `v`) and `checksums.txt`.
- A sandbox repository that contains a small Flux/Kustomize tree. The fixture
  in `testdata/e2e` of this repository is a good start. Use fake secret values
  only.
- A workflow in the sandbox repository:

  ```yaml
  on: pull_request
  permissions:
    contents: read
    pull-requests: write
  jobs:
    diff:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
          with:
            ref: ${{ github.event.pull_request.head.sha }}
            fetch-depth: 0
            persist-credentials: false
        - uses: JtheGunner/flux-diff-action@<commit sha>
          with:
            version: v<released version>
            path: <directory of the Flux tree>
  ```

## Steps and expected results

1. **Same-repository PR with a change.** Open a pull request that edits a
   ConfigMap and a Secret value.
   - The job succeeds.
   - Exactly one comment appears, starting with a hidden `<!-- flux-diff -->`
     marker. It shows the ConfigMap change.
   - The Secret value appears masked. The new and old plain values appear
     nowhere in the comment or in the job log.
2. **Push another commit to the same PR.**
   - The existing comment is edited. No second comment is created.
3. **Weaken the configuration in the PR.** Add `.flux-diff.yaml` changes that
   would hide a kind or remove a mask pattern.
   - The comment is rendered with the configuration of the base revision. The
     weaker head configuration has no effect.
4. **Large diff.** Change enough objects to exceed `max-comment-size`.
   - The comment ends with a truncation notice.
   - The run has an artifact named `flux-diff-full` with the complete report.
5. **Fork PR.** Open a pull request from a fork.
   - The job succeeds without writing a comment.
   - The diff appears in the job's step summary.
6. **Failure handling.** Introduce an unrenderable unit.
   - By default the step succeeds and shows a warning, and the comment lists
     the unit as failed.
   - With `fail-on-error: true` the step fails.
7. **Tampered download.** Re-run with a `version` whose `checksums.txt` does not
   match its archive (for example in a copy of the release).
   - The job fails before running anything from the archive.

## Cleaning up

Delete the sandbox repository, or close its pull requests and remove the
artifact from the finished runs.
