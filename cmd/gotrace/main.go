package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

var (
	write      = flag.Bool("w", false, "write result to source file")
	pkgPath    = flag.String("pkg", "github.com/napolitain/gotrace/trace", "trace package import path")
	skipMain   = flag.Bool("skip-main", false, "skip main() function")
	skipInit   = flag.Bool("skip-init", false, "skip init() functions")
	pattern    = flag.String("pattern", "", "only instrument functions matching pattern")
	removeFlag = flag.Bool("remove", false, "remove tracing instrumentation")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gotrace [flags] <file.go|directory>\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	target := flag.Arg(0)
	info, err := os.Stat(target)
	if err != nil {
		fatal(err)
	}

	if info.IsDir() {
		err = filepath.Walk(target, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return processFile(path)
		})
	} else {
		err = processFile(target)
	}

	if err != nil {
		fatal(err)
	}
}

func processFile(filename string) error {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", filename, err)
	}

	if *removeFlag {
		removeInstrumentation(node)
	} else {
		instrument(node)
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return fmt.Errorf("format %s: %w", filename, err)
	}

	if *write {
		return os.WriteFile(filename, buf.Bytes(), 0644)
	}
	fmt.Println(buf.String())
	return nil
}

func instrument(node *ast.File) {
	needsImport := false

	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil || len(fn.Body.List) == 0 {
			return true
		}

		name := funcName(fn)
		if *skipMain && name == "main" {
			return true
		}
		if *skipInit && name == "init" {
			return true
		}
		if *pattern != "" && !strings.Contains(name, *pattern) {
			return true
		}
		if hasTraceDefer(fn.Body) {
			return true
		}

		// Build args list: function name + all parameter names
		args := []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", name)}}
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				for _, paramName := range field.Names {
					args = append(args, ast.NewIdent(paramName.Name))
				}
			}
		}

		deferStmt := &ast.DeferStmt{
			Call: &ast.CallExpr{
				Fun: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("trace"),
						Sel: ast.NewIdent("Trace"),
					},
					Args: args,
				},
			},
		}

		fn.Body.List = append([]ast.Stmt{deferStmt}, fn.Body.List...)
		needsImport = true
		return true
	})

	if needsImport {
		addImport(node, *pkgPath)
	}
}

func removeInstrumentation(node *ast.File) {
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		var filtered []ast.Stmt
		for _, stmt := range fn.Body.List {
			if !isTraceDefer(stmt) {
				filtered = append(filtered, stmt)
			}
		}
		fn.Body.List = filtered
		return true
	})

	removeImport(node, *pkgPath)
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
	return ok && id.Name == "trace" && sel.Sel.Name == "Trace"
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
	for _, decl := range node.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		var kept []ast.Spec
		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)
			if imp.Path.Value != quoted {
				kept = append(kept, spec)
			}
		}
		gen.Specs = kept
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gotrace: %v\n", err)
	os.Exit(1)
}
