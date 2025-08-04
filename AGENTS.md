# AGENTS

This repository contains the Go sources for REPLbot.

## Development

- Use Go 1.17 or newer.
- Format Go code with `go fmt` (or `make fmt`) before committing.
- Run unit tests with `go test ./...`. Many tests need `tmux`, `vim`, `asciinema`, and `ttyd`; they can be installed the same way as in `.github/workflows/test.yaml`.
- For a full suite of checks including vetting and linting, run `make check`.
- Security checks run via `.github/workflows/security.yaml` and include `govulncheck`, `gosec`, dependency license validation, and container image scans with Trivy. When changing dependencies or Dockerfiles, ensure these tools pass locally.

## Commit Guidelines

- Keep commit messages concise and descriptive.
- Update documentation and examples when behavior or configuration options change.

