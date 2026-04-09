package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHot_RunsInstrumentedModuleAndForwardsArgs(t *testing.T) {
	resetInstrumentationGlobals(t)
	t.Setenv("NO_COLOR", "1")

	moduleRoot := writeRunHotModule(t)

	before, err := os.ReadFile(filepath.Join(moduleRoot, "main.go"))
	if err != nil {
		t.Fatalf("ReadFile(before): %v", err)
	}

	output := captureCombinedOutput(t, func() {
		if err := RunHot(moduleRoot, []string{"alpha", "beta"}); err != nil {
			t.Fatalf("RunHot: %v", err)
		}
	})

	if !strings.Contains(output, "args: [alpha beta]") {
		t.Fatalf("expected forwarded args in output, got:\n%s", output)
	}
	if !strings.Contains(output, "count=2") {
		t.Fatalf("expected traced program output, got:\n%s", output)
	}
	if !strings.Contains(output, "→ main()") {
		t.Fatalf("expected trace entry for main, got:\n%s", output)
	}
	if !strings.Contains(output, "GoTrace Summary") {
		t.Fatalf("expected trace summary, got:\n%s", output)
	}

	after, err := os.ReadFile(filepath.Join(moduleRoot, "main.go"))
	if err != nil {
		t.Fatalf("ReadFile(after): %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("expected RunHot to leave original source unchanged")
	}
}

func TestRunHot_SummaryFlagSuppressesLiveTraceOutput(t *testing.T) {
	resetInstrumentationGlobals(t)
	t.Setenv("NO_COLOR", "1")
	*summary = true

	moduleRoot := writeRunHotModule(t)

	output := captureCombinedOutput(t, func() {
		if err := RunHot(moduleRoot, []string{"alpha", "beta"}); err != nil {
			t.Fatalf("RunHot: %v", err)
		}
	})

	if strings.Contains(output, "→ main()") || strings.Contains(output, "← main") {
		t.Fatalf("expected summary mode to suppress live trace output, got:\n%s", output)
	}
	if !strings.Contains(output, "args: [alpha beta]") {
		t.Fatalf("expected program output to remain visible, got:\n%s", output)
	}
	if !strings.Contains(output, "GoTrace Summary") {
		t.Fatalf("expected summary output in summary mode, got:\n%s", output)
	}
}

func TestRunHot_FunctionFlagPrintsFunctionStatsInSummaryMode(t *testing.T) {
	resetInstrumentationGlobals(t)
	t.Setenv("NO_COLOR", "1")
	*summary = true
	*functionFlag = "greet"

	moduleRoot := writeRunHotModule(t)

	output := captureCombinedOutput(t, func() {
		if err := RunHot(moduleRoot, []string{"alpha", "beta"}); err != nil {
			t.Fatalf("RunHot: %v", err)
		}
	})

	if strings.Contains(output, "→ greet") || strings.Contains(output, "← greet") {
		t.Fatalf("expected summary mode to suppress live function trace output, got:\n%s", output)
	}
	if !strings.Contains(output, "Function Micro-Benchmark") {
		t.Fatalf("expected function stats output, got:\n%s", output)
	}
	if strings.Contains(output, "GoTrace Summary") {
		t.Fatalf("did not expect global summary in --function mode, got:\n%s", output)
	}
}

func TestRunHot_RejectsNonDirectoryTargets(t *testing.T) {
	resetInstrumentationGlobals(t)

	filePath := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := RunHot(filePath, nil)
	if err == nil || !strings.Contains(err.Error(), "target must be a directory") {
		t.Fatalf("expected non-directory target error, got %v", err)
	}
}

func TestRunHot_RequiresModuleRoot(t *testing.T) {
	resetInstrumentationGlobals(t)

	moduleRoot := t.TempDir()
	writeTestFile(t, moduleRoot, "main.go", "package main\n\nfunc main() {}\n")

	err := RunHot(moduleRoot, nil)
	if err == nil || !strings.Contains(err.Error(), "no go.mod found") {
		t.Fatalf("expected missing go.mod error, got %v", err)
	}
}

func TestRunHot_RejectsIncompatibleFlagCombinations(t *testing.T) {
	resetInstrumentationGlobals(t)
	*functionFlag = "helper"
	*from = "main"

	err := RunHot(t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "--function cannot be used with --from or --until") {
		t.Fatalf("expected incompatible flag combination error, got %v", err)
	}
}

func TestRunBinary_StartError(t *testing.T) {
	resetInstrumentationGlobals(t)

	err := runBinary(filepath.Join(t.TempDir(), "missing-binary"), nil)
	if err == nil || !strings.Contains(err.Error(), "start:") {
		t.Fatalf("expected start error, got %v", err)
	}
}

func writeRunHotModule(t *testing.T) string {
	t.Helper()

	moduleRoot := t.TempDir()
	writeTestFile(t, moduleRoot, "go.mod", "module example.com/runhot\n\ngo 1.24\n")

	mainSource := `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Printf("args: %v\n", os.Args[1:])
	greet(os.Args[1:])
}

func greet(args []string) {
	fmt.Printf("count=%d\n", len(args))
}
`
	writeTestFile(t, moduleRoot, "main.go", mainSource)

	return moduleRoot
}

func captureCombinedOutput(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}

	os.Stdout = w
	os.Stderr = w

	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}

	return buf.String()
}
