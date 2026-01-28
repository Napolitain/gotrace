package main

import (
	"bytes"
	"errors"
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
	marker      = "// +gotrace:instrumented"
	traceModule = "github.com/napolitain/gotrace"
	tracePkg    = traceModule + "/trace"
)

var (
	dryRun  = flag.Bool("dry-run", false, "show what would change without modifying")
	verbose = flag.Bool("verbose", false, "print detailed info")
	pattern = flag.String("pattern", "", "only instrument functions matching pattern")
	filters = flag.String("filters", "", "comma-separated filters (e.g. 'panic')")
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
  gotrace .                    # Run current directory with tracing
  gotrace ./cmd/app            # Run specific package
  gotrace ./cmd/app --port 80  # Run with arguments forwarded
  gotrace --dry-run ./cmd/app  # Preview instrumentation without running
  gotrace --filters panic .    # Only show traces when panic occurs
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

	// Add PrintSummary to main if present
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			return true
		}
		summaryCall := &ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("trace"), Sel: ast.NewIdent("PrintSummary")},
			},
		}
		fn.Body.List = append(fn.Body.List, summaryCall)
		return false
	})

	return modified
}

func removeInstrumentation(node *ast.File) {
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		var filtered []ast.Stmt
		for _, stmt := range fn.Body.List {
			if !isTraceDefer(stmt) && !isTraceSummary(stmt) {
				filtered = append(filtered, stmt)
			}
		}
		fn.Body.List = filtered
		return true
	})

	removeImport(node, tracePkg)
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

func removeImport(node *ast.File, path string) {
	quoted := fmt.Sprintf("%q", path)
	for i, decl := range node.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		var kept []ast.Spec
		for _, spec := range gen.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok || imp.Path.Value != quoted {
				kept = append(kept, spec)
			}
		}
		gen.Specs = kept
		if len(kept) == 0 {
			node.Decls = append(node.Decls[:i], node.Decls[i+1:]...)
		}
		return
	}
}

func addMarker(content []byte) []byte {
	lines := bytes.Split(content, []byte("\n"))
	var result [][]byte
	for i, line := range lines {
		result = append(result, line)
		// Add marker after package line
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("package ")) {
			result = append(result, []byte(marker))
			result = append(result, lines[i+1:]...)
			break
		}
	}
	return bytes.Join(result, []byte("\n"))
}

func removeMarker(content []byte) []byte {
	lines := bytes.Split(content, []byte("\n"))
	var result [][]byte
	for _, line := range lines {
		if bytes.Equal(bytes.TrimSpace(line), []byte(marker)) {
			continue
		}
		result = append(result, line)
	}
	return cleanupBlanks(bytes.Join(result, []byte("\n")))
}

func cleanupBlanks(content []byte) []byte {
	// Clean consecutive blank lines and blank lines after opening braces or before closing braces
	lines := bytes.Split(content, []byte("\n"))
	var result [][]byte
	prevBlank := false
	for i, line := range lines {
		isBlank := len(bytes.TrimSpace(line)) == 0
		// Skip blank line after opening brace
		if isBlank && i > 0 {
			prevLine := bytes.TrimSpace(lines[i-1])
			if bytes.HasSuffix(prevLine, []byte("{")) {
				continue
			}
		}
		// Skip blank line before closing brace
		if isBlank && i+1 < len(lines) {
			nextLine := bytes.TrimSpace(lines[i+1])
			if bytes.HasPrefix(nextLine, []byte("}")) {
				continue
			}
		}
		// Skip consecutive blank lines
		if isBlank && prevBlank {
			continue
		}
		result = append(result, line)
		prevBlank = isBlank
	}
	return bytes.Join(result, []byte("\n"))
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

func syncModuleDeps(moduleRoot string) error {
	modPath := filepath.Join(moduleRoot, "go.mod")
	content, err := os.ReadFile(modPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	modulePath, err := readModulePath(modPath)
	if err != nil {
		return err
	}
	if modulePath == traceModule {
		return nil
	}
	needsTrace, err := moduleUsesTrace(moduleRoot)
	if err != nil {
		return err
	}
	mod, err := modfile.Parse(modPath, content, nil)
	if err != nil {
		return err
	}

	changed := false
	if needsTrace {
		localRoot := findLocalGotraceRoot()
		traceVersion := resolveTraceVersion()
		if localRoot == "" {
			if traceVersion == "v0.0.0" {
				return fmt.Errorf("unable to locate local %s module; run gotrace from its repo or install the module", traceModule)
			}
		} else {
			replacePath := localRoot
			if rel, err := filepath.Rel(moduleRoot, localRoot); err == nil {
				replacePath = rel
			}
			if !hasReplace(mod, traceModule, replacePath) {
				if err := mod.AddReplace(traceModule, "", replacePath, ""); err != nil {
					return err
				}
				changed = true
			}
		}
		if !hasRequire(mod, traceModule) {
			if err := mod.AddRequire(traceModule, traceVersion); err != nil {
				return err
			}
			changed = true
		}
		if mod.Go == nil || mod.Go.Version == "" {
			if err := mod.AddGoStmt("1.21"); err != nil {
				return err
			}
			changed = true
		}
	} else {
		if dropRequire(mod, traceModule) {
			changed = true
		}
		if dropReplace(mod, traceModule) {
			changed = true
		}
	}

	if !changed {
		return nil
	}
	formatted, err := mod.Format()
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("  Would update: %s\n", modPath)
		return nil
	}
	return os.WriteFile(modPath, formatted, 0644)
}

func moduleUsesTrace(moduleRoot string) (bool, error) {
	var found bool
	errFound := errors.New("trace import found")
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "testdata" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			if path != moduleRoot {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				} else if err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(content, []byte(tracePkg)) {
			return nil
		}
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, content, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, imp := range node.Imports {
			if imp.Path.Value == fmt.Sprintf("%q", tracePkg) {
				found = true
				return errFound
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errFound) {
			return true, nil
		}
		return false, err
	}
	return found, nil
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

func dropRequire(mod *modfile.File, path string) bool {
	if err := mod.DropRequire(path); err != nil {
		return false
	}
	return true
}

func dropReplace(mod *modfile.File, path string) bool {
	if err := mod.DropReplace(path, ""); err != nil {
		return false
	}
	return true
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
