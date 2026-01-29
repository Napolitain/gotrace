package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/modfile"
)

const (
	traceModule = "github.com/napolitain/gotrace"
	tracePkg    = traceModule + "/trace"
)

var (
	dryRun       = flag.Bool("dry-run", false, "show what would change without modifying")
	verbose      = flag.Bool("verbose", false, "print detailed info")
	pattern      = flag.String("pattern", "", "only instrument functions matching pattern")
	filters      = flag.String("filters", "", "comma-separated filters (e.g. 'panic')")
	until        = flag.String("until", "", "only instrument call path to this function")
	from         = flag.String("from", "", "trace from this function (use with --until for segments)")
	pmu          = flag.Bool("pmu", false, "collect hardware performance counters (Linux only)")
	functionFlag = flag.String("function", "", "micro-benchmark a specific function only")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `gotrace - Hot function tracing for Go

Usage: gotrace [flags] <target> [args...]

Instruments your Go code in-memory, compiles, and runs it with tracing enabled.
No files are modified on disk.

Arguments:
  target    Package directory to run (e.g., ".", "./cmd/app")
  args      Arguments forwarded to the compiled program

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  gotrace .                       # Run current directory with tracing
  gotrace ./cmd/app               # Run specific package
  gotrace ./cmd/app --port 80     # Run with arguments forwarded
  gotrace --dry-run ./cmd/app     # Preview instrumentation without running
  gotrace --filters panic .       # Only show traces when panic occurs
  gotrace --until "DB.Query" .    # Only trace call path to DB.Query
  gotrace --pmu .                 # Include hardware performance counters (Linux)
`)
	}
	flag.Parse()

	if flag.NArg() < 1 {
		fatal(fmt.Errorf("usage: gotrace <target> [args...]\nRun 'gotrace --help' for more information"))
	}

	target := flag.Arg(0)
	args := flag.Args()[1:] // Everything after target goes to the program

	info, err := os.Stat(target)
	if err != nil {
		fatal(err)
	}
	if !info.IsDir() {
		fatal(fmt.Errorf("target must be a directory containing a Go package"))
	}

	if *dryRun {
		if err := previewInstrumentation(target); err != nil {
			fatal(err)
		}
		return
	}

	if err := RunHot(target, args); err != nil {
		fatal(err)
	}
}

func previewInstrumentation(target string) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	moduleRoot, err := findModuleRoot(absTarget)
	if err != nil {
		return err
	}
	if moduleRoot == "" {
		return fmt.Errorf("no go.mod found for %s", absTarget)
	}

	fmt.Printf("Would instrument module at: %s\n", moduleRoot)
	fmt.Printf("Target package: %s\n\n", absTarget)

	return filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "testdata" || (strings.HasPrefix(base, ".") && base != "." && base != "..") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fset := token.NewFileSet()
		node, parseErr := parser.ParseFile(fset, path, content, parser.ParseComments)
		if parseErr != nil {
			return nil // Skip unparseable files
		}

		// Check if instrumentation would modify
		hasFunc := false
		ast.Inspect(node, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok && fn.Body != nil && len(fn.Body.List) > 0 {
				if *pattern == "" || strings.Contains(funcName(fn), *pattern) {
					if !hasTraceDefer(fn.Body) {
						hasFunc = true
						return false
					}
				}
			}
			return true
		})

		if hasFunc {
			rel, _ := filepath.Rel(moduleRoot, path)
			fmt.Printf("  Would instrument: %s\n", rel)
		}
		return nil
	})
}

// allowedFuncs is set when --until is used to filter instrumentation to call path only.
// allowedFuncs restricts instrumentation to specific functions when --until is used.
var allowedFuncs map[string]bool

// targetFunction is set when --function is used for micro-benchmark mode.
var targetFunction string

func instrumentAST(node *ast.File) bool {
	modified := false

	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil || len(fn.Body.List) == 0 {
			return true
		}

		name := funcName(fn)
		if *pattern != "" && !strings.Contains(name, *pattern) {
			return true
		}
		// If --until is specified, only instrument functions in the call path
		if allowedFuncs != nil && !allowedFuncs[name] {
			return true
		}
		// If --function is specified, only instrument that specific function
		if targetFunction != "" && name != targetFunction {
			return true
		}
		if hasTraceDefer(fn.Body) {
			return true
		}

		// Build trace call with args
		args := []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", name)}}
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				for _, paramName := range field.Names {
					args = append(args, ast.NewIdent(paramName.Name))
				}
			}
		}

		// Choose trace function based on filters
		traceFuncName := "Trace"
		if *filters == "panic" {
			traceFuncName = "TraceOnPanic"
		}

		deferStmt := &ast.DeferStmt{
			Call: &ast.CallExpr{
				Fun: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("trace"),
						Sel: ast.NewIdent(traceFuncName),
					},
					Args: args,
				},
			},
		}

		fn.Body.List = append([]ast.Stmt{deferStmt}, fn.Body.List...)
		modified = true
		return true
	})

	if modified {
		addImport(node, tracePkg)
	}

	// Add PrintSummary or PrintFunctionStats to main if present
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			return true
		}
		var summaryCall *ast.ExprStmt
		if targetFunction != "" {
			// --function mode: use PrintFunctionStats
			summaryCall = &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun:  &ast.SelectorExpr{X: ast.NewIdent("trace"), Sel: ast.NewIdent("PrintFunctionStats")},
					Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", targetFunction)}},
				},
			}
		} else {
			// Normal mode: use PrintSummary
			summaryCall = &ast.ExprStmt{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{X: ast.NewIdent("trace"), Sel: ast.NewIdent("PrintSummary")},
				},
			}
		}
		fn.Body.List = append(fn.Body.List, summaryCall)
		return false
	})

	return modified
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0].Type
		var typeName string
		switch t := recv.(type) {
		case *ast.Ident:
			typeName = t.Name
		case *ast.StarExpr:
			if id, ok := t.X.(*ast.Ident); ok {
				typeName = id.Name
			}
		}
		if typeName != "" {
			return typeName + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

func hasTraceDefer(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		if isTraceDefer(stmt) {
			return true
		}
	}
	return false
}

func isTraceDefer(stmt ast.Stmt) bool {
	def, ok := stmt.(*ast.DeferStmt)
	if !ok {
		return false
	}
	call, ok := def.Call.Fun.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "trace" {
		return false
	}
	// Match both Trace and TraceOnPanic
	return sel.Sel.Name == "Trace" || sel.Sel.Name == "TraceOnPanic"
}

func isTraceSummary(stmt ast.Stmt) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "trace" && sel.Sel.Name == "PrintSummary"
}

func addImport(node *ast.File, path string) {
	for _, imp := range node.Imports {
		if imp.Path.Value == fmt.Sprintf("%q", path) {
			return
		}
	}

	newImport := &ast.ImportSpec{
		Path: &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", path)},
	}

	for _, decl := range node.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			gen.Specs = append(gen.Specs, newImport)
			return
		}
	}

	node.Decls = append([]ast.Decl{&ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: []ast.Spec{newImport},
	}}, node.Decls...)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gotrace: %v\n", err)
	os.Exit(1)
}

func findModuleRoot(dir string) (string, error) {
	current := dir
	for {
		modPath := filepath.Join(current, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			abs, err := filepath.Abs(current)
			if err != nil {
				return "", err
			}
			return abs, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		current = parent
	}
}

func findLocalGotraceRoot() string {
	if root := findModuleRootByModulePathFromCwd(); root != "" {
		return root
	}
	exe, err := os.Executable()
	if err == nil {
		if root := findModuleRootByModulePath(filepath.Dir(exe)); root != "" {
			return root
		}
	}
	return ""
}

func readModulePath(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mod, err := modfile.Parse(path, content, nil)
	if err != nil {
		return "", err
	}
	if mod.Module == nil {
		return "", fmt.Errorf("module path not found in %s", path)
	}
	return mod.Module.Mod.Path, nil
}

func hasRequire(mod *modfile.File, path string) bool {
	for _, req := range mod.Require {
		if req.Mod.Path == path {
			return true
		}
	}
	return false
}

func hasReplace(mod *modfile.File, oldPath, newPath string) bool {
	for _, rep := range mod.Replace {
		if rep.Old.Path != oldPath {
			continue
		}
		if newPath == "" {
			return true
		}
		if filepath.Clean(rep.New.Path) == filepath.Clean(newPath) {
			return true
		}
	}
	return false
}

func resolveTraceVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == traceModule {
		if version := strings.TrimSpace(info.Main.Version); version != "" && version != "(devel)" {
			return version
		}
	}
	return "v0.0.0"
}

func findModuleRootByModulePathFromCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return findModuleRootByModulePath(cwd)
}

func findModuleRootByModulePath(startDir string) string {
	current := startDir
	for {
		modPath := filepath.Join(current, "go.mod")
		modPathValue, err := readModulePath(modPath)
		if err == nil && modPathValue == traceModule {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
