# AGENTS

This file provides guidance for AI assistants (including Claude Code at claude.ai/code) and human developers when working with code in this repository.

## Commands

### Testing and Quality Checks
```bash
# Run all tests, formatting, vetting and linting
make check

# Run tests only
make test

# Run tests with race detection
make race

# Run tests with coverage
make coverage
make coverage-html

# Code quality checks
make fmt           # Format code
make vet           # Run go vet
make lint          # Run golint
make staticcheck   # Run staticcheck
```

### Building
```bash
# Simple build
make build-simple

# Build with goreleaser
make build

# Build snapshot
make build-snapshot
```

### Running a Single Test
```bash
# Run specific test by name
go test -v -run TestSessionLifecycle ./bot

# Run tests in specific package
go test -v ./bot
go test -v ./config
go test -v ./util
```

## Architecture Overview

REPLbot is a chat bot for Slack and Discord that provides interactive REPL and shell access directly from chat interfaces. Users can run programming environments, shells, and interactive tools collaboratively within their chat channels.

### Core Components

1. **Bot Core** (`bot/bot.go`): Main orchestration, SSH server, event handling, and session lifecycle management. The bot manages concurrent sessions, enforces resource limits, and routes messages between chat platforms and terminal sessions.

2. **Platform Connectors**: 
   - `bot/conn_slack.go`: Slack RTM API integration with channel/thread support
   - `bot/conn_discord.go`: Discord API with thread management
   - `bot/conn_mem.go`: In-memory connector for testing

3. **Session Management** (`bot/session.go`): Manages terminal sessions via tmux, handles recording with asciinema, provides web terminal access via ttyd, and manages user permissions. Sessions can be controlled via channel messages, threads, or split mode.

4. **Configuration** (`config/config.go`): Manages bot configuration, discovers and validates REPL scripts, and auto-detects platform from token format (Slack tokens start with `xoxb-`).

5. **Utilities**:
   - `util/tmux.go`: tmux session lifecycle and command execution
   - `util/util.go`: Random string generation and helper functions

### Key Design Patterns

- **Container Isolation**: All REPL scripts run in Docker containers with resource limits
- **Event-Driven Architecture**: Asynchronous event handling for chat messages
- **Session State Management**: Complex state machine for session lifecycle
- **Platform Abstraction**: Common interface (`bot.Connector`) for different chat platforms
- **Terminal Multiplexing**: Uses tmux for session persistence and sharing

### Testing Requirements

The test environment requires:
- tmux ≥ 2.6 (core dependency)
- Docker (for REPL script execution)
- vim (for terminal interaction tests)
- asciinema (optional, for recording tests)
- ttyd (optional, for web terminal tests)

Tests use the in-memory connector to simulate bot interactions without requiring actual Slack/Discord connections. The CI pipeline runs tests in a Docker container with all dependencies pre-installed.

## Development Guidelines

- Use Go 1.17 or newer.
- Format Go code with `go fmt` (or `make fmt`) before committing.
- Run unit tests with `go test ./...`. Many tests need `tmux`, `vim`, `asciinema`, and `ttyd`; they can be installed the same way as in `.github/workflows/test.yaml`.
- For a full suite of checks including vetting and linting, run `make check`.
- Security checks run via `.github/workflows/security.yaml` and include `govulncheck`, `gosec`, dependency license validation, and container image scans with Trivy. When changing dependencies or Dockerfiles, ensure these tools pass locally.

## Commit Guidelines

- Keep commit messages concise and descriptive.
- Update documentation and examples when behavior or configuration options change.