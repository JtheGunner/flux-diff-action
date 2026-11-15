# Security policy

flux-diff keeps secret values out of pull request comments, logs and
artifacts, and downloads executables in CI. Reports about either are welcome.

## Reporting a vulnerability

Please do **not** open a public issue. Use GitHub's private reporting:
**Security → Report a vulnerability** on
[JtheGunner/flux-diff-action](https://github.com/JtheGunner/flux-diff-action/security/advisories/new).

Include the flux-diff version, how it runs (CLI or Action), and the smallest
input that reproduces the problem. Use fake secret values only.

Examples of in-scope problems:

- a secret value that appears in any output (comment, step summary, artifact,
  log, error message);
- a way for a pull request to weaken its own checks, for example through
  `.flux-diff.yaml`;
- a download that is run without its checksum being verified.

Problems in flate itself belong to the
[flate project](https://github.com/home-operations/flate).

## Supported versions

Only the latest release receives fixes.
