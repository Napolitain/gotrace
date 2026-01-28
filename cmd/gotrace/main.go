package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
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
	forceAdd    = flag.Bool("add", false, "force add instrumentation")
	forceRemove = flag.Bool("remove", false, "force remove instrumentation")
	dryRun      = flag.Bool("dry-run", false, "show what would change without modifying")
	verbose     = flag.Bool("verbose", false, "print detailed info")
	pattern     = flag.String("pattern", "", "only instrument functions matching pattern")
	filters     = flag.String("filters", "", "comma-separated filters (e.g. 'panic')")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `gotrace - Function tracing for Go

Usage: gotrace [flags] [path]

Toggle instrumentation in your Go project. Run once to add tracing,
run again to remove it. Instrumented code requires -tags debug to build.

Arguments:
  path    Directory or file (default: current directory)

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  gotrace              # Toggle in current directory
  gotrace ./cmd/app    # Toggle in specific directory  
  gotrace --dry-run .  # Preview changes
  gotrace --remove .   # Force remove
  gotrace --filters panic .  # Only show traces when panic occurs

Running instrumented code:
  go run -tags debug .
  go build -tags debug -o myapp .
`)
	}
	flag.Parse()

	target := "."
	if flag.NArg() > 0 {
		target = flag.Arg(0)
	}

	if *forceAdd && *forceRemove {
		fatal(fmt.Errorf("cannot use --add and --remove together"))
	}

	info, err := os.Stat(target)
	if err != nil {
		fatal(err)
	}

	if info.IsDir() {
		err = processDirectory(target)
	} else {
		err = processFile(target)
	}

	if err != nil {
		fatal(err)
	}
}

func processDirectory(dir string) error {
	// Collect packages (directories with .go files)
	packages := make(map[string][]string)
	moduleRoots := make(map[string]struct{})

	err := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip hidden dirs (but not . or ..), vendor, testdata
		base := filepath.Base(path)
		if fi.IsDir() {
			if base == "vendor" || base == "testdata" {
				return filepath.SkipDir
			}
			// Skip hidden directories, but allow . as starting point
			if strings.HasPrefix(base, ".") && base != "." && base != ".." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip files with build tags that exclude debug (the trace stub)
		content, err := os.ReadFile(path)
		if err == nil && bytes.Contains(content, []byte("//go:build !debug")) {
			return nil
		}
		// Skip files with //go:build debug (already trace-enabled)
		if err == nil && bytes.Contains(content, []byte("//go:build debug")) {
			return nil
		}
		pkgDir := filepath.Dir(path)
		packages[pkgDir] = append(packages[pkgDir], path)
		moduleRoot, err := findModuleRoot(pkgDir)
		if err != nil {
			return err
		}
		if moduleRoot != "" {
			moduleRoots[moduleRoot] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for pkgDir, files := range packages {
		if err := processPackage(pkgDir, files); err != nil {
			return err
		}
	}
	if *dryRun {
		return nil
	}
	for moduleRoot := range moduleRoots {
		if err := syncModuleDeps(moduleRoot); err != nil {
			return err
		}
	}
	return nil
}

func processPackage(pkgDir string, files []string) error {
	// Check if any file is instrumented
	instrumented := false
	for _, f := range files {
		if isInstrumented(f) {
			instrumented = true
			break
		}
	}

	// Determine action
	shouldAdd := !instrumented
	if *forceAdd {
		shouldAdd = true
	}
	if *forceRemove {
		shouldAdd = false
	}

	if shouldAdd {
		return instrumentPackage(pkgDir, files)
	}
	return uninstrumentPackage(pkgDir, files)
}

func isInstrumented(filename string) bool {
	content, err := os.ReadFile(filename)
	if err != nil {
		return false
	}
	return bytes.Contains(content, []byte(marker))
}

func instrumentPackage(pkgDir string, files []string) error {
	if *verbose || *dryRun {
		fmt.Printf("Instrumenting %s\n", pkgDir)
	}

	var pkgName string
	for _, filename := range files {
		content, err := os.ReadFile(filename)
		if err != nil {
			return err
		}

		// Skip already instrumented
		if bytes.Contains(content, []byte(marker)) {
			continue
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filename, err)
		}

		pkgName = node.Name.Name

		// Skip files with no functions
		hasFunc := false
		ast.Inspect(node, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncDecl); ok {
				hasFunc = true
				return false
			}
			return true
		})
		if !hasFunc {
			continue
		}

		// Instrument
		modified := instrumentAST(node)
		if !modified {
			continue
		}

		// Format
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, node); err != nil {
			return fmt.Errorf("format %s: %w", filename, err)
		}

		// Add marker after package line
		output := addMarker(buf.Bytes())

		if *dryRun {
			fmt.Printf("  Would modify: %s\n", filename)
			continue
		}

		if err := os.WriteFile(filename, output, 0644); err != nil {
			return err
		}
		if *verbose {
			fmt.Printf("  Instrumented: %s\n", filename)
		}
	}

	if pkgName == "" {
		return nil
	}

	fmt.Printf("✓ Instrumented %s (run with: go run -tags debug .)\n", pkgDir)
	return nil
}

func uninstrumentPackage(pkgDir string, files []string) error {
	if *verbose || *dryRun {
		fmt.Printf("Removing instrumentation from %s\n", pkgDir)
	}

	for _, filename := range files {
		content, err := os.ReadFile(filename)
		if err != nil {
			return err
		}

		// Skip non-instrumented
		if !bytes.Contains(content, []byte(marker)) {
			continue
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filename, err)
		}

		// Remove instrumentation
		removeInstrumentation(node)

		// Format
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, node); err != nil {
			return fmt.Errorf("format %s: %w", filename, err)
		}

		// Remove marker
		output := removeMarker(buf.Bytes())

		if *dryRun {
			fmt.Printf("  Would restore: %s\n", filename)
			continue
		}

		if err := os.WriteFile(filename, output, 0644); err != nil {
			return err
		}
		if *verbose {
			fmt.Printf("  Restored: %s\n", filename)
		}
	}

	fmt.Printf("✓ Removed instrumentation from %s\n", pkgDir)
	return nil
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

func processFile(filename string) error {
	if err := processPackage(filepath.Dir(filename), []string{filename}); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}
	moduleRoot, err := findModuleRoot(filepath.Dir(filename))
	if err != nil {
		return err
	}
	if moduleRoot == "" {
		return nil
	}
	return syncModuleDeps(moduleRoot)
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
