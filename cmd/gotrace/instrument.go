package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// insertion represents a text insertion at a specific byte position
type insertion struct {
	pos  int
	text string
}

// instrumentFileText instruments a Go file using source-level text injection.
// This preserves all comments, directives (go:embed, go:generate, etc.), and formatting.
func instrumentFileText(filename string, content []byte) ([]byte, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
	if err != nil {
		// Return original content - let go build report syntax errors
		return content, nil
	}

	// Check if already instrumented
	for _, imp := range node.Imports {
		if imp.Path.Value == fmt.Sprintf("%q", tracePkg) {
			return content, nil
		}
	}

	var insertions []insertion
	var hasInstrumentation bool

	// Collect function insertions
	ast.Inspect(node, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil || len(fn.Body.List) == 0 {
			return true
		}

		name := funcName(fn)

		// Apply filters
		if *pattern != "" && !strings.Contains(name, *pattern) {
			return true
		}
		if allowedFuncs != nil && !allowedFuncs[name] {
			return true
		}
		if targetFunction != "" && name != targetFunction {
			return true
		}

		// Check if already has trace defer
		if hasTraceDefer(fn.Body) {
			return true
		}

		// Build parameter list (skip blank identifiers)
		var params []string
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				for _, paramName := range field.Names {
					if paramName.Name != "_" {
						params = append(params, paramName.Name)
					}
				}
			}
		}

		// Choose trace function based on filters
		traceFuncName := "Trace"
		if *filters == "panic" {
			traceFuncName = "TraceOnPanic"
		}

		// Get position right after opening brace
		lbracePos := fset.Position(fn.Body.Lbrace).Offset

		// Check if this is a single-line function (content on same line as {)
		// We need to detect if there's code after { on the same line
		isSingleLine := false
		if lbracePos+1 < len(content) {
			// Look for non-whitespace before newline
			for i := lbracePos + 1; i < len(content) && content[i] != '\n'; i++ {
				if content[i] != ' ' && content[i] != '\t' {
					isSingleLine = true
					break
				}
			}
		}

		// Build defer statement
		var deferText string
		if len(params) > 0 {
			if isSingleLine {
				// For single-line functions, use semicolon to separate statements
				deferText = fmt.Sprintf(" defer %s.%s(%q, %s)();",
					tracePkgAlias, traceFuncName, name, strings.Join(params, ", "))
			} else {
				deferText = fmt.Sprintf("\n\tdefer %s.%s(%q, %s)()",
					tracePkgAlias, traceFuncName, name, strings.Join(params, ", "))
			}
		} else {
			if isSingleLine {
				deferText = fmt.Sprintf(" defer %s.%s(%q)();",
					tracePkgAlias, traceFuncName, name)
			} else {
				deferText = fmt.Sprintf("\n\tdefer %s.%s(%q)()",
					tracePkgAlias, traceFuncName, name)
			}
		}

		insertions = append(insertions, insertion{pos: lbracePos + 1, text: deferText})
		hasInstrumentation = true

		return true
	})

	if !hasInstrumentation {
		return content, nil
	}

	// Add import insertion after package declaration
	// Find end of package line
	pkgEndOffset := fset.Position(node.Name.End()).Offset
	for pkgEndOffset < len(content) && content[pkgEndOffset] != '\n' {
		pkgEndOffset++
	}

	importText := fmt.Sprintf("\nimport %s %q\n", tracePkgAlias, tracePkg)
	insertions = append(insertions, insertion{pos: pkgEndOffset + 1, text: importText})

	// Add PrintSummary/PrintFunctionStats to main function
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil || fn.Body == nil {
			continue
		}

		// Get position right before closing brace
		rbracePos := fset.Position(fn.Body.Rbrace).Offset

		var summaryText string
		if targetFunction != "" {
			summaryText = fmt.Sprintf("\n\t%s.PrintFunctionStats(%q)", tracePkgAlias, targetFunction)
		} else {
			summaryText = fmt.Sprintf("\n\t%s.PrintSummary()", tracePkgAlias)
		}
		insertions = append(insertions, insertion{pos: rbracePos, text: summaryText})
		break
	}

	// Sort insertions by position descending (apply from end to start)
	sort.Slice(insertions, func(i, j int) bool {
		return insertions[i].pos > insertions[j].pos
	})

	// Apply insertions
	result := content
	for _, ins := range insertions {
		if ins.pos < 0 || ins.pos > len(result) {
			continue
		}
		result = append(result[:ins.pos], append([]byte(ins.text), result[ins.pos:]...)...)
	}

	return result, nil
}
