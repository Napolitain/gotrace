//go:build !debug

package trace

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

var noop = func(...any) {}

func Trace(name string, args ...any) func(...any) {
	// Runtime guard: detect if caller is instrumented
	_, file, _, ok := runtime.Caller(1)
	if ok && isInstrumented(file) {
		fmt.Fprintln(os.Stderr, "\n⚠️  ERROR: Running instrumented code without -tags debug")
		fmt.Fprintln(os.Stderr, "   This code has gotrace instrumentation but is running in production mode.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "   To run with tracing:  go run -tags debug .")
		fmt.Fprintln(os.Stderr, "   To remove tracing:    gotrace --remove .")
		fmt.Fprintln(os.Stderr, "")
		os.Exit(1)
	}
	return noop
}

var checkedFiles = make(map[string]bool)

func isInstrumented(file string) bool {
	if v, ok := checkedFiles[file]; ok {
		return v
	}
	content, err := os.ReadFile(file)
	if err != nil {
		checkedFiles[file] = false
		return false
	}
	instrumented := strings.Contains(string(content), "// +gotrace:instrumented")
	checkedFiles[file] = instrumented
	return instrumented
}

type Entry struct {
	Name     string
	Args     []any
	Returns  []any
	Depth    int32
	StartNs  int64
	EndNs    int64
	Duration int64
	GID      uint64
	File     string
	Line     int
	Panicked bool
	PanicVal any
}

func GetTraces() []Entry       { return nil }
func GetHotPaths() []Entry     { return nil }
func Reset()                   {}
func SetThresholds(_, _ int64) {}
func SetColorize(_ bool)       {}
func PrintSummary()            {}
