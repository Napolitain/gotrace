# ⚡ GoTrace

A lean, zero-overhead function tracer for Go with pretty terminal output and Perfetto export.

![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-green.svg)

## Features

- 🚀 **Zero runtime cost in production** — Uses build tags to compile out all tracing
- ⏱️ **Nanosecond precision** — Uses `runtime.nanotime()` to avoid GC pressure
- 🎨 **Pretty terminal output** — Colored call trees with lipgloss
- 🔥 **Hotpath detection** — Automatically highlights slow functions
- 📊 **Summary statistics** — Call frequency, total time, averages
- 📈 **Perfetto export** — Native protobuf format for visualization
- 💥 **Panic tracking** — Captures and logs panics with timing
- 🔄 **Return values** — Optional capture of function return values
- 🧵 **Goroutine aware** — Tracks goroutine IDs in output

## Installation

```bash
go get github.com/napolitain/gotrace
```

Install the instrumenter CLI:

```bash
go install github.com/napolitain/gotrace/cmd/gotrace@latest
```

## Quick Start

### Manual Instrumentation

```go
package main

import "github.com/napolitain/gotrace/trace"

func main() {
    defer trace.Trace("main")()
    
    result := compute(42)
    // ...
}

func compute(n int) int {
    defer trace.Trace("compute", n)()  // Log function args
    // ...
    return n * 2
}
```

### Automatic Instrumentation

Use the `gotrace` CLI to automatically instrument your code:

```bash
# Preview changes
gotrace ./src/

# Apply changes in-place
gotrace -w ./src/

# Remove instrumentation
gotrace -w -remove ./src/
```

### Running

```bash
# Development (with tracing)
go run -tags debug .

# Production (zero overhead)
go run .
```

## Output Example

```
→ main() [main.go:10 g1]
  → fibonacci(5) [main.go:32 g1]
    → fibonacci(4) [main.go:32 g1]
      → fibonacci(3) [main.go:32 g1]
        ← fibonacci → 3 221.27µs
      ← fibonacci → 4 356.23µs
    ← fibonacci → 5 562.58µs
Result: 5
  → Calculator.Add(10, 20) [main.go:44 g1]
  ← Calculator.Add → 30 23.04µs

╔════════════════════╗
║ ⚡ GoTrace Summary ║
╚════════════════════╝

  📈 17 total calls   ⏱  1.81ms total time   📦 3 unique functions

🔥 Top 10 Slowest Calls
  ────────────────────────────────────────────────────────────
   1. fibonacci                        562.58µs  [main.go:32]
   2. fibonacci                        356.23µs  [main.go:32]

📊 Call Frequency
  ────────────────────────────────────────────────────────────
  Function                        Calls        Total          Avg
  fibonacci                          15       1.76ms     117.63µs
```

## Perfetto Visualization

Export traces to Perfetto's native protobuf format:

```go
trace.ExportPerfetto("trace.pftrace")
```

Open the file at [ui.perfetto.dev](https://ui.perfetto.dev) for interactive visualization.

## API Reference

### Tracing Functions

```go
// Basic trace
defer trace.Trace("functionName")()

// With arguments
defer trace.Trace("functionName", arg1, arg2)()

// With return value capture
defer trace.Trace("functionName", arg1)(returnVal)
```

### Configuration

```go
// Set hotpath thresholds (default: 1ms warn, 10ms hot)
trace.SetThresholds(500_000, 5_000_000)  // 500µs, 5ms

// Disable colors
trace.SetColorize(false)
```

### Export & Analysis

```go
// Export to Perfetto
trace.ExportPerfetto("trace.pftrace")

// Print summary
trace.PrintSummary()

// Get raw traces
entries := trace.GetTraces()

// Get hot paths only
hot := trace.GetHotPaths()

// Reset traces
trace.Reset()
```

## CLI Reference

```
Usage: gotrace [flags] <file.go|directory>

Flags:
  -w            Write result to source file (in-place)
  -remove       Remove tracing instrumentation
  -pkg string   Trace package import path (default "github.com/napolitain/gotrace/trace")
  -skip-main    Skip main() function
  -skip-init    Skip init() functions  
  -pattern      Only instrument functions matching pattern
```

## How It Works

1. **Build Tags**: The `trace` package has two implementations:
   - `trace.go` (build tag: `debug`) — Full tracing with output
   - `trace_stub.go` (build tag: `!debug`) — No-op functions that compile away

2. **AST Instrumentation**: The `gotrace` CLI parses Go source files and injects `defer trace.Trace()()` calls at function entry points.

3. **Timing**: Uses `runtime.nanotime()` via `//go:linkname` to avoid `time.Time` allocations.

## Performance

| Mode | Overhead |
|------|----------|
| Production (`go build`) | **Zero** — stub functions are inlined away |
| Debug (`go build -tags debug`) | ~100-500ns per traced call |

## Project Structure

```
gotrace/
├── cmd/gotrace/     # CLI instrumenter
├── trace/
│   ├── trace.go     # Active tracer (//go:build debug)
│   └── trace_stub.go # No-op stub (//go:build !debug)
└── example/         # Example usage
```

## License

MIT License - see [LICENSE](LICENSE) for details.
