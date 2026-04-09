package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstrumentFileText_SingleLineFunctionSkipsBlankIdentifiers(t *testing.T) {
	resetInstrumentationGlobals(t)

	src := `package main

func main(){ run(1, 2) }
func run(_, b int){ println(b) }
`

	result, err := instrumentFileText("test.go", []byte(src))
	if err != nil {
		t.Fatalf("instrumentFileText: %v", err)
	}

	got := string(result)
	if !strings.Contains(got, `func main(){ defer gotrace_trace.Trace("main")();`) {
		t.Fatalf("expected single-line main instrumentation, got:\n%s", got)
	}
	if !strings.Contains(got, `func run(_, b int){ defer gotrace_trace.Trace("run", b)();`) {
		t.Fatalf("expected single-line function instrumentation to skip blank identifiers, got:\n%s", got)
	}
	if strings.Contains(got, `Trace("run", _, b)`) {
		t.Fatalf("expected blank identifier to be excluded from trace args, got:\n%s", got)
	}
	if !strings.Contains(got, `gotrace_trace.PrintSummary()`) {
		t.Fatalf("expected PrintSummary to be added to main, got:\n%s", got)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "test.go", result, parser.ParseComments); err != nil {
		t.Fatalf("instrumented file should still parse: %v\n%s", err, got)
	}
}

func TestInstrumentFileText_RespectsAllowedFuncs(t *testing.T) {
	resetInstrumentationGlobals(t)
	allowedFuncs = map[string]bool{
		"main":   true,
		"helper": true,
	}

	src := `package main

func main() {
	helper()
	other()
}

func helper() {
	println("helper")
}

func other() {
	println("other")
}
`

	result, err := instrumentFileText("test.go", []byte(src))
	if err != nil {
		t.Fatalf("instrumentFileText: %v", err)
	}

	got := string(result)
	if !strings.Contains(got, `gotrace_trace.Trace("main")`) {
		t.Fatalf("expected main to be instrumented, got:\n%s", got)
	}
	if !strings.Contains(got, `gotrace_trace.Trace("helper")`) {
		t.Fatalf("expected helper to be instrumented, got:\n%s", got)
	}
	if strings.Contains(got, `gotrace_trace.Trace("other")`) {
		t.Fatalf("expected other to be skipped, got:\n%s", got)
	}
}

func TestInstrumentFileText_TargetFunctionUsesTraceOnPanicAndFunctionStats(t *testing.T) {
	resetInstrumentationGlobals(t)
	*filters = "panic"
	targetFunction = "helper"

	src := `package main

func main() {
	helper()
	other()
}

func helper() {
	println("helper")
}

func other() {
	println("other")
}
`

	result, err := instrumentFileText("test.go", []byte(src))
	if err != nil {
		t.Fatalf("instrumentFileText: %v", err)
	}

	got := string(result)
	if !strings.Contains(got, `gotrace_trace.TraceOnPanic("helper")`) {
		t.Fatalf("expected target function to use TraceOnPanic, got:\n%s", got)
	}
	if strings.Contains(got, `gotrace_trace.TraceOnPanic("other")`) || strings.Contains(got, `gotrace_trace.Trace("other")`) {
		t.Fatalf("expected non-target function to be skipped, got:\n%s", got)
	}
	if !strings.Contains(got, `gotrace_trace.PrintFunctionStats("helper")`) {
		t.Fatalf("expected main to print function stats in target-function mode, got:\n%s", got)
	}
	if strings.Contains(got, `gotrace_trace.PrintSummary()`) {
		t.Fatalf("did not expect PrintSummary in target-function mode, got:\n%s", got)
	}
}

func TestInstrumentFileText_SkipsAlreadyInstrumentedFile(t *testing.T) {
	resetInstrumentationGlobals(t)

	src := `package main

import gotrace_trace "github.com/napolitain/gotrace/trace"

func main() {
	defer gotrace_trace.Trace("main")()
	println("hello")
}
`

	result, err := instrumentFileText("test.go", []byte(src))
	if err != nil {
		t.Fatalf("instrumentFileText: %v", err)
	}
	if !bytes.Equal(result, []byte(src)) {
		t.Fatalf("expected already instrumented file to be returned unchanged, got:\n%s", result)
	}
}

func TestCopyAndInstrumentModule_SkipsHiddenDirsAndPreservesSpecialFiles(t *testing.T) {
	resetInstrumentationGlobals(t)

	tempSrc := t.TempDir()
	tempDst := t.TempDir()

	writeTestFile(t, tempSrc, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeTestFile(t, tempSrc, "main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n")

	ignored := "//go:build ignore\n\npackage main\n\nfunc ignored() {}\n"
	writeTestFile(t, tempSrc, "ignore.go", ignored)

	already := "package main\n\nimport gotrace_trace \"github.com/napolitain/gotrace/trace\"\n\nfunc already() {\n\tdefer gotrace_trace.Trace(\"already\")()\n}\n"
	writeTestFile(t, tempSrc, "already.go", already)

	writeTestFile(t, tempSrc, filepath.Join(".hidden", "hidden.go"), "package hidden\n")

	if err := copyAndInstrumentModule(tempSrc, tempDst); err != nil {
		t.Fatalf("copyAndInstrumentModule: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tempDst, ".hidden")); !os.IsNotExist(err) {
		t.Fatalf("hidden directory should not be copied")
	}

	ignoredContent, err := os.ReadFile(filepath.Join(tempDst, "ignore.go"))
	if err != nil {
		t.Fatalf("ReadFile(ignore.go): %v", err)
	}
	if string(ignoredContent) != ignored {
		t.Fatalf("expected //go:build ignore file to be copied unchanged, got:\n%s", ignoredContent)
	}

	alreadyContent, err := os.ReadFile(filepath.Join(tempDst, "already.go"))
	if err != nil {
		t.Fatalf("ReadFile(already.go): %v", err)
	}
	if string(alreadyContent) != already {
		t.Fatalf("expected already instrumented file to be copied unchanged, got:\n%s", alreadyContent)
	}

	mainContent, err := os.ReadFile(filepath.Join(tempDst, "main.go"))
	if err != nil {
		t.Fatalf("ReadFile(main.go): %v", err)
	}
	if !strings.Contains(string(mainContent), `gotrace_trace.Trace("main")`) {
		t.Fatalf("expected main.go to be instrumented, got:\n%s", mainContent)
	}
}

func resetInstrumentationGlobals(t *testing.T) {
	t.Helper()

	oldPattern := *pattern
	oldFilters := *filters
	oldFrom := *from
	oldUntil := *until
	oldSummary := *summary
	oldFunctionFlag := *functionFlag
	oldAllowed := allowedFuncs
	oldTargetFunction := targetFunction

	*pattern = ""
	*filters = ""
	*from = ""
	*until = ""
	*summary = false
	*functionFlag = ""
	allowedFuncs = nil
	targetFunction = ""

	t.Cleanup(func() {
		*pattern = oldPattern
		*filters = oldFilters
		*from = oldFrom
		*until = oldUntil
		*summary = oldSummary
		*functionFlag = oldFunctionFlag
		allowedFuncs = oldAllowed
		targetFunction = oldTargetFunction
	})
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
