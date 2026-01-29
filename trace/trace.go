package trace

import (
	"cmp"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	_ "unsafe"

	"github.com/charmbracelet/lipgloss"
)

//go:linkname nanotime runtime.nanotime
func nanotime() int64

// Styles using lipgloss
var (
	funcStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ECDC4"))
	argsStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38181"))
	fileStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	fastStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ECDC4"))
	warmStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFE66D"))
	hotStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B"))
	enterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#95E1D3"))
	exitStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))
	panicStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#FF0000"))
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4")).BorderStyle(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("#7D56F4")).Padding(0, 1)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFE66D"))
)

// Thresholds for hotpath detection (nanoseconds)
var (
	warnThresholdNs atomic.Int64 // Default: 1ms
	hotThresholdNs  atomic.Int64 // Default: 10ms
)

var (
	depth        int32
	mu           sync.Mutex
	traces       []Entry
	colorize     atomic.Bool
	panicStacks  map[uint64][]string // Per-goroutine call stacks
	panicMu      sync.Mutex
	panicPrinted atomic.Bool
)

func init() {
	warnThresholdNs.Store(1_000_000) // 1ms
	hotThresholdNs.Store(10_000_000) // 10ms
	colorize.Store(os.Getenv("NO_COLOR") == "")
	panicStacks = make(map[uint64][]string)
}

// Entry represents a single trace record for a function call.
type Entry struct {
	Name     string // Function name
	Args     []any  // Arguments passed to the function
	Returns  []any  // Return values (if captured)
	Depth    int32  // Call stack depth
	StartNs  int64  // Start time in nanoseconds
	EndNs    int64  // End time in nanoseconds
	Duration int64  // Duration in nanoseconds
	GID      uint64 // Goroutine ID
	File     string // Source file name
	Line     int    // Line number
	Panicked bool   // Whether the function panicked
	PanicVal any    // Panic value if panicked
}

// Trace logs function entry/exit with timing. Use with defer:
//
//	defer trace.Trace("functionName", args...)()
func Trace(name string, args ...any) func(...any) {
	d := atomic.AddInt32(&depth, 1)
	start := nanotime()
	gid := getGID()
	_, file, line, _ := runtime.Caller(1)
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		file = file[idx+1:]
	}

	indent := strings.Repeat("  ", int(d-1))
	printEntry(indent, name, args, file, line, gid)

	return func(returns ...any) {
		end := nanotime()
		dur := end - start

		var panicked bool
		var panicVal any
		if r := recover(); r != nil {
			panicked = true
			panicVal = r
			printPanic(indent, name, dur, r)
			defer panic(r)
		} else {
			printExit(indent, name, dur, returns)
		}

		mu.Lock()
		traces = append(traces, Entry{
			Name: name, Args: args, Returns: returns,
			Depth: d, StartNs: start, EndNs: end, Duration: dur,
			GID: gid, File: file, Line: line,
			Panicked: panicked, PanicVal: panicVal,
		})
		mu.Unlock()
		atomic.AddInt32(&depth, -1)
	}
}

func printEntry(indent, name string, args []any, file string, line int, gid uint64) {
	argsStr := ""
	if len(args) > 0 {
		argsStr = formatArgs(args)
	}
	if colorize.Load() {
		fmt.Printf("%s%s %s%s %s\n",
			indent,
			enterStyle.Render("→"),
			funcStyle.Render(name),
			argsStyle.Render("("+argsStr+")"),
			fileStyle.Render(fmt.Sprintf("[%s:%d g%d]", file, line, gid)))
	} else {
		fmt.Printf("%s→ %s(%s) [%s:%d g%d]\n", indent, name, argsStr, file, line, gid)
	}
}

func printExit(indent, name string, dur int64, returns []any) {
	durStr := formatDuration(dur)
	retStr := ""
	if len(returns) > 0 {
		retStr = " → " + argsStyle.Render(formatArgs(returns))
	}

	var styledDur, hotTag string
	if dur >= hotThresholdNs.Load() {
		styledDur = hotStyle.Render(durStr)
		hotTag = " " + hotStyle.Render("🔥 HOT")
	} else if dur >= warnThresholdNs.Load() {
		styledDur = warmStyle.Render(durStr)
	} else {
		styledDur = fastStyle.Render(durStr)
	}

	if colorize.Load() {
		fmt.Printf("%s%s %s%s %s%s\n", indent, exitStyle.Render("←"), fileStyle.Render(name), retStr, styledDur, hotTag)
	} else {
		fmt.Printf("%s← %s%s (%s)\n", indent, name, retStr, durStr)
	}
}

func printPanic(indent, name string, dur int64, panicVal any) {
	if colorize.Load() {
		fmt.Printf("%s%s %s: %s (%s)\n", indent, panicStyle.Render("💥 PANIC"), funcStyle.Render(name), hotStyle.Render(fmt.Sprintf("%v", panicVal)), formatDuration(dur))
	} else {
		fmt.Printf("%s💥 PANIC %s: %v (%s)\n", indent, name, panicVal, formatDuration(dur))
	}
}

func formatDuration(ns int64) string {
	switch {
	case ns < 1_000:
		return fmt.Sprintf("%dns", ns)
	case ns < 1_000_000:
		return fmt.Sprintf("%.2fµs", float64(ns)/1e3)
	case ns < 1_000_000_000:
		return fmt.Sprintf("%.2fms", float64(ns)/1e6)
	default:
		return fmt.Sprintf("%.2fs", float64(ns)/1e9)
	}
}

func formatArgs(args []any) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = fmt.Sprintf("%v", arg)
	}
	return strings.Join(parts, ", ")
}

func getGID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	var gid uint64
	fmt.Sscanf(string(b), "goroutine %d ", &gid)
	return gid
}

// GetTraces returns a copy of all collected trace entries.
// The returned slice is safe to modify.
func GetTraces() []Entry {
	mu.Lock()
	defer mu.Unlock()
	cp := make([]Entry, len(traces))
	copy(cp, traces)
	return cp
}

// Reset clears all traces, resets call depth, and clears panic state.
// Call this between test runs or to start fresh.
func Reset() {
	mu.Lock()
	traces = traces[:0]
	mu.Unlock()
	atomic.StoreInt32(&depth, 0)

	panicMu.Lock()
	panicPrinted.Store(false)
	panicStacks = make(map[uint64][]string)
	panicMu.Unlock()
}

// GetHotPaths returns entries whose duration exceeds the hot threshold (default 10ms).
func GetHotPaths() []Entry {
	mu.Lock()
	defer mu.Unlock()
	var hot []Entry
	for _, e := range traces {
		if e.Duration >= hotThresholdNs.Load() {
			hot = append(hot, e)
		}
	}
	return hot
}

// SetThresholds configures hotpath detection thresholds in nanoseconds.
// warnNs is the threshold for yellow highlighting (default 1ms).
// hotNs is the threshold for red "HOT" marking (default 10ms).
func SetThresholds(warnNs, hotNs int64) {
	warnThresholdNs.Store(warnNs)
	hotThresholdNs.Store(hotNs)
}

// SetColorize enables/disables color output.
// Color is enabled by default unless NO_COLOR environment variable is set.
func SetColorize(enabled bool) {
	colorize.Store(enabled)
}

// PrintSummary displays a formatted summary of all traces including
// top slowest calls and call frequency statistics.
func PrintSummary() {
	mu.Lock()
	defer mu.Unlock()

	if len(traces) == 0 {
		fmt.Println("No traces collected")
		return
	}

	sorted := slices.Clone(traces)
	slices.SortFunc(sorted, func(a, b Entry) int {
		return cmp.Compare(b.Duration, a.Duration) // Descending order
	})

	counts := make(map[string]int)
	totalTime := make(map[string]int64)
	maxTime := make(map[string]int64)
	var totalDuration int64
	for _, e := range traces {
		counts[e.Name]++
		totalTime[e.Name] += e.Duration
		totalDuration += e.Duration
		if e.Duration > maxTime[e.Name] {
			maxTime[e.Name] = e.Duration
		}
	}

	var sb strings.Builder
	sb.WriteString("\n" + titleStyle.Render("⚡ GoTrace Summary") + "\n\n")
	sb.WriteString(fmt.Sprintf("  📈 %s total calls   ⏱  %s total time   📦 %s unique functions\n\n",
		funcStyle.Render(fmt.Sprintf("%d", len(traces))),
		fastStyle.Render(formatDuration(totalDuration)),
		argsStyle.Render(fmt.Sprintf("%d", len(counts)))))

	sb.WriteString(headerStyle.Render("🔥 Top 10 Slowest Calls") + "\n")
	sb.WriteString(fileStyle.Render("  "+strings.Repeat("─", 60)) + "\n")

	limit := 10
	if len(sorted) < limit {
		limit = len(sorted)
	}
	hotThreshold := hotThresholdNs.Load()
	warnThreshold := warnThresholdNs.Load()
	for i := 0; i < limit; i++ {
		e := sorted[i]
		var styledDur string
		if e.Duration >= hotThreshold {
			styledDur = hotStyle.Render(fmt.Sprintf("%12s", formatDuration(e.Duration)))
		} else if e.Duration >= warnThreshold {
			styledDur = warmStyle.Render(fmt.Sprintf("%12s", formatDuration(e.Duration)))
		} else {
			styledDur = fastStyle.Render(fmt.Sprintf("%12s", formatDuration(e.Duration)))
		}
		sb.WriteString(fmt.Sprintf("  %s %s %s  %s\n",
			fileStyle.Render(fmt.Sprintf("%2d.", i+1)),
			funcStyle.Render(fmt.Sprintf("%-28s", truncate(e.Name, 28))),
			styledDur,
			fileStyle.Render(fmt.Sprintf("[%s:%d]", e.File, e.Line))))
	}

	sb.WriteString("\n" + headerStyle.Render("📊 Call Frequency") + "\n")
	sb.WriteString(fileStyle.Render("  "+strings.Repeat("─", 60)) + "\n")

	type funcStat struct {
		name  string
		count int
		total int64
		max   int64
	}
	var stats []funcStat
	for name, count := range counts {
		stats = append(stats, funcStat{name, count, totalTime[name], maxTime[name]})
	}
	slices.SortFunc(stats, func(a, b funcStat) int {
		return cmp.Compare(b.total, a.total) // Descending order
	})

	limit = 10
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		avg := s.total / int64(s.count)
		var totalStyled, avgStyled string
		if s.max >= hotThreshold {
			totalStyled = hotStyle.Render(fmt.Sprintf("%12s", formatDuration(s.total)))
			avgStyled = hotStyle.Render(fmt.Sprintf("%12s", formatDuration(avg)))
		} else if s.max >= warnThreshold {
			totalStyled = warmStyle.Render(fmt.Sprintf("%12s", formatDuration(s.total)))
			avgStyled = warmStyle.Render(fmt.Sprintf("%12s", formatDuration(avg)))
		} else {
			totalStyled = fastStyle.Render(fmt.Sprintf("%12s", formatDuration(s.total)))
			avgStyled = fastStyle.Render(fmt.Sprintf("%12s", formatDuration(avg)))
		}
		sb.WriteString(fmt.Sprintf("  %s %s %s %s\n",
			funcStyle.Render(fmt.Sprintf("%-28s", truncate(s.name, 28))),
			argsStyle.Render(fmt.Sprintf("%8d", s.count)),
			totalStyled, avgStyled))
	}
	sb.WriteString("\n")
	fmt.Print(sb.String())
}

// PrintFunctionStats displays detailed statistics for a specific function.
// This is used with --function flag for micro-benchmark mode.
func PrintFunctionStats(name string) {
	mu.Lock()
	defer mu.Unlock()

	// Filter entries for the target function
	var durations []int64
	for _, e := range traces {
		if e.Name == name {
			durations = append(durations, e.Duration)
		}
	}

	if len(durations) == 0 {
		fmt.Printf("\n🎯 Function: %s\n", name)
		fmt.Println("  No invocations recorded")
		return
	}

	// Sort for percentile calculations
	slices.Sort(durations)

	// Calculate statistics
	count := len(durations)
	var total int64
	for _, d := range durations {
		total += d
	}

	minD := durations[0]
	maxD := durations[count-1]
	mean := total / int64(count)

	// Median
	var median int64
	if count%2 == 0 {
		median = (durations[count/2-1] + durations[count/2]) / 2
	} else {
		median = durations[count/2]
	}

	// Percentiles (with bounds checking)
	p95Idx := int(float64(count-1) * 0.95)
	p99Idx := int(float64(count-1) * 0.99)
	p95 := durations[p95Idx]
	p99 := durations[p99Idx]

	// Standard deviation
	var variance float64
	meanF := float64(mean)
	for _, d := range durations {
		diff := float64(d) - meanF
		variance += diff * diff
	}
	variance /= float64(count)
	stdDev := int64(math.Sqrt(variance))

	// Build output
	var sb strings.Builder
	sb.WriteString("\n" + titleStyle.Render("🎯 Function Micro-Benchmark") + "\n\n")
	sb.WriteString(fmt.Sprintf("  Function: %s\n", funcStyle.Render(name)))
	sb.WriteString(fileStyle.Render("  "+strings.Repeat("─", 60)) + "\n")

	sb.WriteString(fmt.Sprintf("  Invocations:     %s\n", argsStyle.Render(fmt.Sprintf("%d", count))))
	sb.WriteString(fmt.Sprintf("  Total Time:      %s\n\n", fastStyle.Render(formatDuration(total))))

	sb.WriteString(headerStyle.Render("  📊 Timing Distribution") + "\n")
	sb.WriteString(fileStyle.Render("  "+strings.Repeat("─", 60)) + "\n")

	sb.WriteString(fmt.Sprintf("    Min:           %s\n", fastStyle.Render(formatDuration(minD))))
	sb.WriteString(fmt.Sprintf("    Max:           %s\n", colorDuration(maxD)))
	sb.WriteString(fmt.Sprintf("    Mean:          %s\n", colorDuration(mean)))
	sb.WriteString(fmt.Sprintf("    Median:        %s\n", colorDuration(median)))
	sb.WriteString(fmt.Sprintf("    P95:           %s\n", colorDuration(p95)))
	sb.WriteString(fmt.Sprintf("    P99:           %s\n", colorDuration(p99)))
	sb.WriteString(fmt.Sprintf("    Std Dev:       %s\n", fastStyle.Render(formatDuration(stdDev))))

	sb.WriteString("\n")
	fmt.Print(sb.String())
}

// colorDuration formats duration with color based on thresholds.
func colorDuration(d int64) string {
	hotThreshold := hotThresholdNs.Load()
	warnThreshold := warnThresholdNs.Load()
	if d >= hotThreshold {
		return hotStyle.Render(formatDuration(d))
	} else if d >= warnThreshold {
		return warmStyle.Render(formatDuration(d))
	}
	return fastStyle.Render(formatDuration(d))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// TraceOnPanic buffers traces silently and only outputs them when a panic occurs.
// This is useful for debugging - keep tracing enabled but silent until something goes wrong.
// Use with defer:
//
//	defer trace.TraceOnPanic("functionName", args...)()
func TraceOnPanic(name string, args ...any) func(...any) {
	d := atomic.AddInt32(&depth, 1)
	start := nanotime()
	gid := getGID()
	_, file, line, _ := runtime.Caller(1)
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		file = file[idx+1:]
	}

	indent := strings.Repeat("  ", int(d-1))
	argsStr := ""
	if len(args) > 0 {
		argsStr = formatArgs(args)
	}

	// Push entry onto this goroutine's stack
	entryMsg := fmt.Sprintf("%s→ %s(%s) [%s:%d g%d]", indent, name, argsStr, file, line, gid)
	panicMu.Lock()
	panicStacks[gid] = append(panicStacks[gid], entryMsg)
	panicMu.Unlock()

	return func(returns ...any) {
		end := nanotime()
		dur := end - start

		if r := recover(); r != nil {
			// Panic detected - dump this goroutine's call stack (only once)
			panicMu.Lock()
			if !panicPrinted.Load() {
				panicPrinted.Store(true)
				fmt.Fprintln(os.Stderr, "\n"+panicStyle.Render("💥 PANIC DETECTED - Trace leading to panic:")+"\n")
				if stack, exists := panicStacks[gid]; exists {
					for _, msg := range stack {
						fmt.Fprintln(os.Stderr, msg)
					}
				}
				fmt.Fprintln(os.Stderr, "")
			}
			panicMu.Unlock()

			// Print the panic exit
			printPanic(indent, name, dur, r)

			// Store in traces for analysis
			mu.Lock()
			traces = append(traces, Entry{
				Name: name, Args: args, Returns: returns,
				Depth: d, StartNs: start, EndNs: end, Duration: dur,
				GID: gid, File: file, Line: line,
				Panicked: true, PanicVal: r,
			})
			mu.Unlock()

			atomic.AddInt32(&depth, -1)
			panic(r) // re-throw
		}

		// Normal exit - pop this entry from stack
		panicMu.Lock()
		if stack, exists := panicStacks[gid]; exists && len(stack) > 0 {
			// Pop: remove last entry (this function's entry)
			panicStacks[gid] = stack[:len(stack)-1]
			// Clean up empty stacks to prevent map growth
			if len(panicStacks[gid]) == 0 {
				delete(panicStacks, gid)
			}
		}
		panicMu.Unlock()

		// Store in traces for analysis
		mu.Lock()
		traces = append(traces, Entry{
			Name: name, Args: args, Returns: returns,
			Depth: d, StartNs: start, EndNs: end, Duration: dur,
			GID: gid, File: file, Line: line,
		})
		mu.Unlock()

		atomic.AddInt32(&depth, -1)
	}
}
