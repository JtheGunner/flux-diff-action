# flate hangs on a SOPS Secret plus a supplied Secret

`flate diff` (tested with v0.7.3) can spin at about 200% CPU and never finish.
This directory holds a script that builds a small repository showing it. It is a
reproduction for bug reports and for checking a newer flate; it is not part of
the Go test suite.

## Run it

```sh
./repro.sh <new-directory> [path-to-flate] [timeout-seconds]
```

The script creates a two-commit Git repository in `<new-directory>`, runs

```sh
flate diff -o json --no-progress --base HEAD~1 --supply supply.yaml \
  --skip-kinds Secret,CustomResourceDefinition
```

and stops flate after the time limit (30 seconds by default). It prints whether
flate was still running (the hang reproduced) or finished. It needs `git`, and
network access to download the `velero` chart once.

## What triggers it

The diff only hangs when all four of these are true. Without any one of them it
finishes in about a second.

| Ingredient | In this repository |
|---|---|
| A SOPS-encrypted Secret in the tree | `velero/secret.sops.yaml` |
| `--supply` for a Secret with the same name | `supply.yaml` |
| A change that alters the rendered HelmRelease between base and head | the second commit flips `snapshotsEnabled` |
| A parent Flux Kustomization that points at the Kustomization holding the HelmRelease | `clusters/apps.yaml` → `apps/velero.yaml` |

With `--base HEAD` (no change) or without `--supply` flate returns immediately.
flate prints `SOPSUnreadable … supply dummy values for references;
re-encryption cannot be compared` for the encrypted Secret.

All Secret values are fake. The SOPS file only has the shape of an encrypted
file and is marked `FAKE-DO-NOT-USE`.

## Effect on flux-diff

flux-diff passes the `supply` paths from `.flux-diff.yaml` to flate, so a
repository that follows the advice to supply stand-ins for SOPS Secrets can hit
this. The `flate-timeout` input (default 10 minutes) ends such a run and reports
it as failed in the pull request comment.
