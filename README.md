<div align="center">

# 🔍 flux-diff

**A readable, secret-safe Flux manifest diff, posted as a pull request comment.**

[![License: MIT](https://img.shields.io/badge/license-MIT-22c55e?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27-3776ab?style=flat-square&logo=go&logoColor=white)](go.mod)
[![Status](https://img.shields.io/badge/status-early%20development-f59e0b?style=flat-square)](#-status)

</div>

---

## 📖 What it is

`flux-diff` is a small Go CLI and GitHub Action built around the
[flate](https://github.com/home-operations/flate) CLI. It compares the Flux
manifests of a pull request against its base branch and posts the result as a
PR comment, with secret values kept out of the output.

---

## 🚧 Status

Early development. The CLI currently only reports its version:

```sh
go build ./cmd/flux-diff
./flux-diff --version
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
