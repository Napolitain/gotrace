# Development Guidelines

## Code Quality Tools

When developing this project, always use the following tools:

### Formatting
```bash
gofmt -w .
```

### Linting
```bash
golangci-lint run
```

Install golangci-lint if not available:
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Development Workflow

1. **Before committing**, always run:
   ```bash
   gofmt -w .
   golangci-lint run
   go test ./...
   ```

2. **Code style:**
   - Use `gofmt` for consistent formatting
   - Use modern Go idioms (slices, cmp packages for Go 1.21+)
   - Use `atomic.Bool` and `atomic.Int64` for thread-safe global state
   - Prefer early returns over nested conditionals

3. **Testing:**
   - Run `go test ./...` for unit tests
   - Run `go test -tags integration ./test/...` for integration tests (requires submodule init)
