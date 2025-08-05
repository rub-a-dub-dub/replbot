#!/bin/bash
# Install pre-commit hook for Go formatting

set -e

echo "Installing pre-commit hook for Go formatting..."

# Create pre-commit hook
cat > .git/hooks/pre-commit << 'EOF'
#!/bin/bash
# Pre-commit hook: Format Go code

echo "Running go fmt..."
go fmt ./...

echo "Running go mod tidy..."
go mod tidy

# Re-add any files that were formatted
git add -u

echo "Code formatting complete!"
EOF

chmod +x .git/hooks/pre-commit

echo "✅ Pre-commit hook installed!"
echo "Now your Go code will be automatically formatted before each commit."