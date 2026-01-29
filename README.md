# ⚡ GoTrace

A lean function tracer for Go with hot instrumentation and pretty terminal output.

![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-green.svg)

## Features

- 🔥 **Hot instrumentation** — Instruments code in-memory, no files modified on disk
- ⏱️ **Nanosecond precision** — Uses `runtime.nanotime()` to avoid GC pressure  
- 🎨 **Pretty terminal output** — Colored call trees with lipgloss
- 🔥 **Hotpath detection** — Automatically highlights slow functions
- 📊 **Summary statistics** — Call frequency, total time, averages
- 💥 **Panic-only mode** — Buffer traces and only show them when a panic occurs

## Installation

```bash
go install github.com/napolitain/gotrace/cmd/gotrace@latest
```

## Quick Start

```bash
# Run your Go program with tracing enabled
gotrace ./cmd/myapp

# Run with arguments
gotrace ./cmd/myapp --port 8080 --verbose

# Preview what would be instrumented
gotrace --dry-run ./cmd/myapp
```

That's it! One command instruments your code in-memory, compiles it, and runs it with full tracing.

## How It Works

When you run `gotrace ./cmd/myapp`:

1. **Discovers** all Go files in your module
2. **Instruments** them in-memory (adds `defer trace.Trace()` to functions)
3. **Compiles** the instrumented code in a temporary directory
4. **Runs** the binary, forwarding all arguments
5. **Cleans up** the temporary files automatically

**No files are modified on disk** — your source code stays clean.

### Module Wiring

GoTrace automatically adds the required `require` and `replace` directives to a temporary copy of your `go.mod` so the trace package is available during compilation.

## Output Example

```
→ main() [main.go:10 g1]
  → fibonacci(10) [main.go:21 g1]
    → fibonacci(9) [main.go:21 g1]
      → fibonacci(8) [main.go:21 g1]
        ← fibonacci 12.34µs
      ← fibonacci 45.67µs
    ← fibonacci 89.01µs 
  ← fibonacci 156.78ms 🔥 HOT
Fibonacci(10) = 55

╔════════════════════╗
║ ⚡ GoTrace Summary ║
╚════════════════════╝

  📈 177 total calls   ⏱  3.24ms total time   📦 2 unique functions

🔥 Top 10 Slowest Calls
  ────────────────────────────────────────────────────────────
   1. fibonacci                          2.98ms  [main.go:21]
   2. fibonacci                          2.02ms  [main.go:21]
   3. fibonacci                          1.26ms  [main.go:21]

📊 Call Frequency
  ────────────────────────────────────────────────────────────
  Function                        Calls        Total          Avg
  fibonacci                         177       3.24ms      18.31µs
```

## CLI Reference

```
gotrace - Hot function tracing for Go

Usage: gotrace [flags] <target> [args...]

Arguments:
  target    Package directory to run (e.g., ".", "./cmd/app")
  args      Arguments forwarded to the compiled program

Flags:
  --dry-run   Preview instrumentation without running
  --verbose   Print detailed information
  --pattern   Only instrument functions matching pattern
  --filters   Comma-separated filters (e.g. 'panic')
  --until     Only instrument call path to specified function

Examples:
  gotrace .                         # Run current directory with tracing
  gotrace ./cmd/myapp               # Run specific package
  gotrace ./cmd/myapp --port 80     # Run with arguments forwarded
  gotrace --dry-run ./cmd/myapp     # Preview what would be instrumented
  gotrace --filters panic .         # Only show traces when panic occurs
  gotrace --until "DB.Query" .      # Only trace call path to DB.Query
```

## Filtering Modes

### Panic-Only Mode

By default, gotrace shows all function calls in real-time. With `--filters panic`, traces are buffered and only displayed when a panic occurs:

```bash
# Run with panic filter
gotrace --filters panic ./cmd/myapp
```

**Normal mode (default):**
```
→ main() [main.go:32 g1]
  → goodFunc() [main.go:10 g1]
  ← goodFunc 0ns
  → anotherFunc() [main.go:15 g1]
  ← anotherFunc 0ns
  → badFunc() [main.go:20 g1]
    💥 PANIC badFunc: something went wrong!
```

**Panic-only mode (`--filters panic`):**
```
(no output for successful calls)

💥 PANIC DETECTED - Trace leading to panic:

→ main() [main.go:32 g1]
  → goodFunc() [main.go:10 g1]
  ← goodFunc 0ns
  → badFunc() [main.go:20 g1]
    💥 PANIC badFunc: something went wrong!
```

This is perfect for debugging — keep tracing enabled but silent until something goes wrong!

### Call Path Mode

With `--until`, gotrace uses static call graph analysis to only instrument functions that are in the call path to a specific target function:

```bash
# Only trace the path to handleRequest
gotrace --until "handleRequest" ./cmd/server

# Trace path to a method (use Type.Method format)
gotrace --until "Database.Query" ./cmd/app
```

**Example output:**
```
→ main() [main.go:10 g1]
  → Server.Start() [server.go:25 g1]
    → handleRequest() [handler.go:42 g1]
    ← handleRequest 1.23ms
  ← Server.Start 5.67ms
← main 5.70ms
```

Functions not in the call path (like utility functions, logging, etc.) are not traced, keeping output focused on what matters.

## Manual Usage

You can also use the trace package directly in your code:

```go
package main

import "github.com/napolitain/gotrace/trace"

func main() {
    defer trace.Trace("main")()
    
    result := compute(42)
    trace.PrintSummary()
}

func compute(n int) int {
    defer trace.Trace("compute", n)()  // Logs function arguments
    return n * 2
}
```

Run with:
```bash
go run .
```

## API Reference

```go
// Basic trace
defer trace.Trace("functionName")()

// With arguments (captured in output)
defer trace.Trace("functionName", arg1, arg2)()

// With return value capture (requires named returns)
func compute(n int) (result int) {
    defer trace.Trace("compute", n)(&result)
    result = n * 2
    return
}

// Print summary at end of program
trace.PrintSummary()

// Get slowest calls
hot := trace.GetHotPaths()

// Reset all traces
trace.Reset()
```

**Note:** Automatic return value capture requires named return values. The CLI instruments with `defer trace.Trace("fn")()` which captures arguments but not returns. For return capture, manually modify to use named returns and pass the address.

## Performance

The trace package adds ~100-500ns overhead per traced function call. This is designed for debugging and development — the overhead is negligible for understanding program behavior.

## Testing

Run standard tests:

```bash
go test ./...
```

Optional integration tests (requires submodule init):

```bash
git submodule update --init --recursive
go test -tags integration ./test/...
```

## Project Structure

```
gotrace/
├── cmd/gotrace/        # CLI tool with hot instrumentation
│   ├── main.go         # CLI entry point and AST instrumentation
│   └── runner.go       # Hot run pipeline (copy, instrument, build, execute)
├── trace/
│   └── trace.go        # Tracer implementation
└── example/            # Example usage
```

## License

MIT License - see [LICENSE](LICENSE) for details.
