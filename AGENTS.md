# Project Overview

Shopware LSP is a Language Server Protocol implementation for Shopware and Symfony development. It provides IDE features (completion, go-to-definition, hover, diagnostics) for PHP, Twig, XML, and YAML files.

**Tech Stack:** Go backend with native lossless CST parsers, a shared SQLite index store, TypeScript VSCode extension.

## Build & Test Commands

```bash
# Build
go build                          # Build LSP server binary
go build ./...                    # Build all packages

# Test
go test ./...                     # Run all tests
go test -race ./internal/...      # Race detection (used in CI)
go test ./internal/php/... -v     # Test specific package
go test -run TestFeatureIndexer   # Run specific test

# Lint
golangci-lint run                 # Lint check (run before committing)

# VSCode extension
cd vscode-extension
npm install && npm run compile    # Build extension
npm run check-types               # Type check only
```

## Architecture

### Entry Point
`main.go` starts the process-level application on stdin/stdout (JSON-RPC).
`internal/app` constructs the workspace indexes and providers after the LSP
`initialize` request supplies the workspace root.

### Key Packages (`internal/`)

| Package | Purpose |
|---------|---------|
| `lsp/` | LSP protocol, server.go is the main handler |
| `lsp/completion/` | 7 completion providers (services, routes, twig, snippets, features, system config, theme) |
| `lsp/definition/` | 7 go-to-definition providers (same domains) |
| `app/` | Initialize-time application/workspace composition and resource ownership |
| `language/` | Registry of language frontends and file extensions |
| `indexer/` | Serialized file indexing coordinator and namespaced SQLite repositories |
| `uriutil/` | LSP file URI conversion |
| `symfony/` | Service container and route indexing from XML/YAML/PHP |
| `php/` | PHP class/method indexing, type inference, alias resolution |
| `twig/` | Template indexing, block tracking, extends/include parsing |
| `snippet/` | Translation key indexing from JSON files |
| `feature/` | Feature flag indexing from YAML |
| `theme/` | Theme config and icon indexing |
| `extension/` | Shopware extension metadata from composer.json |

### Provider Pattern
All LSP features use a provider interface pattern. Multiple providers can handle the same feature type, routed by document language/context.

### Indexing Flow
1. `FileScanner` detects file changes (fsnotify)
2. Files parsed with the native lossless CST frontend for their language
3. Each registered indexer processes AST nodes
4. Data is persisted in one namespaced SQLite store in the workspace cache

The server intentionally supports one workspace folder per process. It rejects
multi-root initialization explicitly; clients should launch one server process
per workspace. `fsnotify` is the single source of index file events.

## Testing Patterns

Tests use `testify/assert` and `testify/require`. Common pattern:
```go
func TestSomething(t *testing.T) {
    tempDir := t.TempDir()
    indexer, err := NewIndexer(tempDir)
    require.NoError(t, err)
    defer indexer.Close()
    // ... test logic
}
```

## Commit Guidelines

Use Conventional Commits: `type(scope): summary`
- `fix:` bug fixes
- `feat:` new features
- `build:` build system changes
- `docs:` documentation

## Debug Tool

```bash
go run cmd/debug_ast/main.go path/to/file.php  # Visualize PHP AST
```
