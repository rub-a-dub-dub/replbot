# Development Guide

## Code Formatting

This project uses automatic code formatting to maintain consistent code style.

### Recommended: Pre-commit Hooks

The best way to ensure your code is properly formatted is to use pre-commit hooks:

```bash
# Install pre-commit (once per machine)
pip install pre-commit

# Install hooks (once per repo)
pre-commit install

# Test it works
pre-commit run --all-files
```

This will automatically run:
- `go fmt` - Format Go code
- `go vet` - Check for Go issues  
- `go mod tidy` - Clean up go.mod
- `golangci-lint` - Advanced linting
- Trailing whitespace removal
- YAML validation

### Alternative Options

1. **GitHub Actions**: Auto-formatting will run on push/PR and commit fixes back
2. **VS Code**: Settings configured for format-on-save 
3. **Manual Git Hook**: Run `./scripts/install-hooks.sh` for basic formatting

### Manual Formatting

If you need to format code manually:

```bash
# Format all Go code
make fmt

# Check formatting (CI uses this)
make fmt-check

# Run all checks
make check
```

## Socket Mode Migration

This codebase has been migrated from Slack's deprecated RTM API to the modern Socket Mode API. Key changes:

- Dual token authentication (Bot Token + App-Level Token)
- WebSocket-based real-time communication
- Enhanced error handling and connection resilience
- Improved event acknowledgment

See the PR for detailed migration information.