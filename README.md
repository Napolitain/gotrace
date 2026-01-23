# ⚡ GoTrace

A lean function tracer for Go with zero production overhead and pretty terminal output.

![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-green.svg)

## Features

- 🚀 **Zero runtime cost in production** — Uses build tags to compile out all tracing
- ⏱️ **Nanosecond precision** — Uses `runtime.nanotime()` to avoid GC pressure  
- 🎨 **Pretty terminal output** — Colored call trees with lipgloss
- 🔥 **Hotpath detection** — Automatically highlights slow functions
- 📊 **Summary statistics** — Call frequency, total time, averages
- 🔒 **Safe workflow** — Cannot accidentally run instrumented code without knowing

## Installation

```bash
go install github.com/napolitain/gotrace/cmd/gotrace@latest
```

## Quick Start

```bash
# 1. Instrument your project (toggle on)
gotrace .

# 2. Run with tracing enabled
go run -tags debug .

# 3. Remove instrumentation (toggle off)
gotrace .
```

That's it! Running `gotrace .` again toggles instrumentation off.

## How It Works

### Safety First

When you instrument code, gotrace creates a guard file that **prevents compilation** without `-tags debug`:

```bash
$ gotrace .
✓ Instrumented myproject (run with: go run -tags debug .)

$ go build .
# myproject
./gotrace_guard.go:11:9: undefined: __GOTRACE_INSTRUMENTED_RUN_WITH_TAGS_DEBUG__
```

This ensures you'll never accidentally commit or deploy instrumented code.

### Toggle Workflow

| State | `gotrace .` | `go build .` | `go build -tags debug .` |
|-------|-------------|--------------|--------------------------|
| Clean | Instruments | ✅ Builds | ✅ Builds |
| Instrumented | Removes | ❌ Error | ✅ Builds with tracing |

## Output Example

```
→ main
  → fibonacci(10)
    → fibonacci(9)
      → fibonacci(8)
        ← fibonacci 12.34µs
      ← fibonacci 45.67µs
    ← fibonacci 89.01µs
  ← fibonacci 156.78µs
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
gotrace - Function tracing instrumentation for Go

Usage: gotrace [flags] [path]

Arguments:
  path    Directory to instrument (default: current directory)

Flags:
  --add       Force add instrumentation
  --remove    Force remove instrumentation
  --dry-run   Preview changes without modifying files
  --verbose   Print detailed information
  --pattern   Only instrument functions matching pattern

Examples:
  gotrace                 # Toggle instrumentation in current directory
  gotrace ./cmd/myapp     # Toggle instrumentation in specific package
  gotrace --dry-run .     # Preview changes
  gotrace --remove .      # Force remove all instrumentation
```

## Manual Usage

You can also use the trace package directly:

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

Build with tracing:
```bash
go run -tags debug .
```

Build without tracing (zero overhead):
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

| Mode | Overhead |
|------|----------|
| Production (`go build`) | **Zero** — stub functions are completely inlined away |
| Debug (`go build -tags debug`) | ~100-500ns per traced call |

## Project Structure

```
gotrace/
├── cmd/gotrace/        # CLI instrumenter
├── trace/
│   ├── trace.go        # Active tracer (//go:build debug)
│   └── trace_stub.go   # No-op stub (//go:build !debug)
└── example/            # Example usage
```

## License

MIT License - see [LICENSE](LICENSE) for details.
