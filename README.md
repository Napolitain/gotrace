# ⚡ GoTrace

A lean function tracer for Go with hot instrumentation and pretty terminal output.

## Features

- 🔥 **Hot instrumentation** — Instruments code in-memory, no files modified on disk
- ⏱️ **Nanosecond precision** — Uses `runtime.nanotime()` to avoid GC pressure  
- 🎨 **Pretty terminal output** — Colored call trees with lipgloss
- 🔥 **Hotpath detection** — Automatically highlights slow functions
- 📊 **Summary statistics** — Call frequency, total time, averages
- 🎯 **Call graph filtering** — Trace specific paths with `--from`, `--until`, `--function`
- 💥 **Panic-only mode** — Buffer traces and only show them when a panic occurs
- 🔧 **PMU counters** — Hardware performance counters on Linux

## Installation

```bash
go install github.com/napolitain/gotrace/cmd/gotrace@latest
```

## Quick Start

```bash
gotrace ./cmd/myapp              # Trace your program
gotrace --function "fibonacci" . # Micro-benchmark a specific function
gotrace --until "DB.Query" .     # Trace only the path to a function
gotrace --pmu .                  # Include hardware counters (Linux)
```

## Output Example

```
→ main() [main.go:10 g1]
  → fibonacci(10) [main.go:21 g1]
    → fibonacci(9) [main.go:21 g1]
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
```

## CLI Reference

```
gotrace [flags] <target> [args...]

Flags:
  --dry-run    Preview instrumentation without running
  --verbose    Print detailed information
  --pattern    Only instrument functions matching pattern
  --filters    Comma-separated filters (e.g. 'panic')
  --until      Trace call path TO this function
  --from       Trace FROM this function (callees)
  --function   Micro-benchmark a single function
  --pmu        Hardware performance counters (Linux)

Examples:
  gotrace .                           # Trace current directory
  gotrace ./cmd/myapp --port 80       # Forward args to program
  gotrace --filters panic .           # Only show traces on panic
  gotrace --until "DB.Query" .        # Trace path to DB.Query
  gotrace --from "Server.Start" .     # Trace from Server.Start
  gotrace --from "A" --until "B" .    # Trace segment A → B
  gotrace --function "fibonacci" .    # Micro-benchmark function
```

## Call Graph Modes

| Flags | Behavior |
|-------|----------|
| `--until "B"` | main() → B (backward) |
| `--from "A"` | A → callees (forward) |
| `--from "A" --until "B"` | A → B (segment) |
| `--function "A"` | Only A (micro-benchmark) |

## Function Micro-Benchmark

```bash
gotrace --function "fibonacci" ./example
```

```
╔═════════════════════════════╗
║ 🎯 Function Micro-Benchmark ║
╚═════════════════════════════╝

  Function: fibonacci
  Invocations:     177
  Total Time:      206.54ms

  📊 Timing Distribution
    Min:      0ns       Mean:     1.17ms
    Max:      32.73ms   Median:   508.40µs
    P95:      4.62ms    P99:      16.28ms
```

## Hardware Counters (Linux)

```bash
gotrace --pmu ./cmd/myapp
```

```
🔧 Hardware Counters (process total)
  CPU Cycles:           12,456,789
  Instructions:         45,678,901    (3.67 IPC)
  Cache Misses:             12,345    (1.00% miss rate)
  Branch Misses:             5,678
```

Requires: `sudo sysctl kernel.perf_event_paranoid=-1`

## How It Works

1. **Discovers** all Go files in your module
2. **Instruments** them in-memory (adds `defer trace.Trace()`)
3. **Compiles** in a temporary directory
4. **Runs** the binary, forwarding arguments
5. **Cleans up** automatically

**No files are modified on disk.**

## Manual Usage

```go
import "github.com/napolitain/gotrace/trace"

func main() {
    defer trace.Trace("main")()
    result := compute(42)
    trace.PrintSummary()
}

func compute(n int) int {
    defer trace.Trace("compute", n)()
    return n * 2
}
```

## Performance

~100-500ns overhead per traced call. Designed for debugging and development.

## License

MIT License - see [LICENSE](LICENSE) for details.
