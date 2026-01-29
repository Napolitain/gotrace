package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstrumentAST_AddsDeferTrace(t *testing.T) {
	t.Parallel()
	src := `package main

func hello() {
	println("hello")
}
`
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	modified := instrumentAST(node)
	if !modified {
		t.Fatal("expected AST to be modified")
	}

	// Check that defer trace.Trace was added
	var hasTraceDefer bool
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "hello" {
			if len(fn.Body.List) > 0 {
				if isTraceDefer(fn.Body.List[0]) {
					hasTraceDefer = true
				}
			}
		}
		return true
	})

	if !hasTraceDefer {
		t.Fatal("expected defer trace.Trace to be added")
	}
}

func TestInstrumentAST_SkipsAlreadyInstrumented(t *testing.T) {
	t.Parallel()
	src := `package main

import "github.com/napolitain/gotrace/trace"

func hello() {
	defer trace.Trace("hello")()
	println("hello")
}
`
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	modified := instrumentAST(node)
	if modified {
		t.Fatal("expected AST to not be modified for already instrumented code")
	}
}

func TestInstrumentAST_InstrumentsWithArgs(t *testing.T) {
	t.Parallel()
	src := `package main

func add(a, b int) int {
	return a + b
}
`
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	modified := instrumentAST(node)
	if !modified {
		t.Fatal("expected AST to be modified")
	}

	// Verify the function was instrumented
	var deferStmt *ast.DeferStmt
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "add" {
			if len(fn.Body.List) > 0 {
				if d, ok := fn.Body.List[0].(*ast.DeferStmt); ok {
					deferStmt = d
				}
			}
		}
		return true
	})

	if deferStmt == nil {
		t.Fatal("expected defer statement to be added")
	}
}

func TestInstrumentAST_AddsPrintSummaryToMain(t *testing.T) {
	t.Parallel()
	src := `package main

func main() {
	println("hello")
}
`
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	modified := instrumentAST(node)
	if !modified {
		t.Fatal("expected AST to be modified")
	}

	// Check that PrintSummary was added to main
	var hasPrintSummary bool
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
			for _, stmt := range fn.Body.List {
				if isTraceSummary(stmt) {
					hasPrintSummary = true
				}
			}
		}
		return true
	})

	if !hasPrintSummary {
		t.Fatal("expected trace.PrintSummary to be added to main")
	}
}

func TestInstrumentFile_ReturnsOriginalIfNoFunctions(t *testing.T) {
	t.Parallel()
	src := `package main

var x = 42
`
	result, err := instrumentFile("test.go", []byte(src))
	if err != nil {
		t.Fatalf("instrumentFile: %v", err)
	}

	if !bytes.Equal(result, []byte(src)) {
		t.Fatal("expected original content to be returned for file without functions")
	}
}

func TestInstrumentFile_HandlesMethodReceivers(t *testing.T) {
	t.Parallel()
	src := `package main

type Calculator struct{}

func (c *Calculator) Add(a, b int) int {
	return a + b
}
`
	result, err := instrumentFile("test.go", []byte(src))
	if err != nil {
		t.Fatalf("instrumentFile: %v", err)
	}

	if !strings.Contains(string(result), `trace.Trace("Calculator.Add"`) {
		t.Fatalf("expected method receiver to be included in trace name, got:\n%s", result)
	}
}

func TestFuncName_ReturnsCorrectNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		src      string
		expected string
	}{
		{`package main; func hello() {}`, "hello"},
		{`package main; type T struct{}; func (t T) Method() {}`, "T.Method"},
		{`package main; type T struct{}; func (t *T) Method() {}`, "T.Method"},
	}

	for _, tt := range tests {
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, "test.go", tt.src, 0)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		var name string
		ast.Inspect(node, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				name = funcName(fn)
				return false
			}
			return true
		})

		if name != tt.expected {
			t.Errorf("funcName() = %q, want %q", name, tt.expected)
		}
	}
}

func TestCopyAndInstrumentModule_SkipsVendorAndTestdata(t *testing.T) {
	t.Parallel()
	tempSrc := t.TempDir()
	tempDst := t.TempDir()

	// Create a minimal module structure
	os.WriteFile(filepath.Join(tempSrc, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tempSrc, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0644)
	
	os.MkdirAll(filepath.Join(tempSrc, "vendor"), 0755)
	os.WriteFile(filepath.Join(tempSrc, "vendor", "vendor.go"), []byte("package vendor\n"), 0644)
	
	os.MkdirAll(filepath.Join(tempSrc, "testdata"), 0755)
	os.WriteFile(filepath.Join(tempSrc, "testdata", "test.go"), []byte("package testdata\n"), 0644)

	err := copyAndInstrumentModule(tempSrc, tempDst)
	if err != nil {
		t.Fatalf("copyAndInstrumentModule: %v", err)
	}

	// vendor and testdata should not be copied
	if _, err := os.Stat(filepath.Join(tempDst, "vendor")); !os.IsNotExist(err) {
		t.Error("vendor directory should not be copied")
	}
	if _, err := os.Stat(filepath.Join(tempDst, "testdata")); !os.IsNotExist(err) {
		t.Error("testdata directory should not be copied")
	}

	// main.go should be instrumented
	content, _ := os.ReadFile(filepath.Join(tempDst, "main.go"))
	if !strings.Contains(string(content), "trace.Trace") {
		t.Error("main.go should be instrumented")
	}
}

func TestPreviewInstrumentation_ListsFilesToInstrument(t *testing.T) {
	// Create temp directory with a simple Go file
	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0644)

	// Capture output
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	*dryRun = true
	err := previewInstrumentation(tempDir)
	*dryRun = false

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("previewInstrumentation: %v", err)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "main.go") {
		t.Errorf("expected output to mention main.go, got:\n%s", output)
	}
}

func TestInstrumentAST_WithAllowedFuncs(t *testing.T) {
	// NOTE: Not parallel because it modifies global allowedFuncs
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
	// Save and restore global state
	oldAllowed := allowedFuncs
	defer func() { allowedFuncs = oldAllowed }()

	// Only allow "main" and "helper" to be instrumented
	allowedFuncs = map[string]bool{
		"main":   true,
		"helper": true,
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	modified := instrumentAST(node)
	if !modified {
		t.Fatal("expected AST to be modified")
	}

	// Check which functions were instrumented
	instrumented := make(map[string]bool)
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Body != nil {
			if len(fn.Body.List) > 0 && isTraceDefer(fn.Body.List[0]) {
				instrumented[fn.Name.Name] = true
			}
		}
		return true
	})

	if !instrumented["main"] {
		t.Error("expected main to be instrumented")
	}
	if !instrumented["helper"] {
		t.Error("expected helper to be instrumented")
	}
	if instrumented["other"] {
		t.Error("expected other to NOT be instrumented (not in allowedFuncs)")
	}
}
