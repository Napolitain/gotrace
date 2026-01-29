package main

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// buildCallGraph builds a call graph for the module at the given path.
// It returns the graph and the SSA program for further analysis.
func buildCallGraph(moduleRoot string) (*callgraph.Graph, *ssa.Program, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: moduleRoot,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, nil, fmt.Errorf("load packages: %w", err)
	}

	// Check for package errors
	var errs []string
	for _, pkg := range pkgs {
		for _, err := range pkg.Errors {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("package errors: %s", strings.Join(errs, "; "))
	}

	// Build SSA program
	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.SanityCheckFunctions)
	prog.Build()

	// Filter to only non-nil packages
	var validPkgs []*ssa.Package
	for _, pkg := range ssaPkgs {
		if pkg != nil {
			validPkgs = append(validPkgs, pkg)
		}
	}

	if len(validPkgs) == 0 {
		return nil, nil, fmt.Errorf("no valid packages found")
	}

	// Build call graph using CHA (Class Hierarchy Analysis)
	graph := cha.CallGraph(prog)

	return graph, prog, nil
}

// findCallersTo finds all functions that are in the call path to the target function.
// It returns a set of function names (including receiver types for methods).
func findCallersTo(graph *callgraph.Graph, prog *ssa.Program, target string) (map[string]bool, error) {
	// Find the target node(s) in the graph
	targetNodes := findFunctionNodes(graph, prog, target)
	if len(targetNodes) == 0 {
		return nil, fmt.Errorf("function %q not found in call graph", target)
	}

	// BFS backwards from target to find all callers
	callers := make(map[string]bool)
	visited := make(map[*callgraph.Node]bool)
	queue := make([]*callgraph.Node, 0, len(targetNodes))

	for _, node := range targetNodes {
		queue = append(queue, node)
		visited[node] = true
		callers[formatFuncName(node.Func)] = true
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Visit all callers (incoming edges)
		for _, edge := range current.In {
			caller := edge.Caller
			if caller == nil || visited[caller] {
				continue
			}
			visited[caller] = true

			name := formatFuncName(caller.Func)
			if name != "" && name != "<root>" {
				callers[name] = true
			}

			queue = append(queue, caller)
		}
	}

	return callers, nil
}

// findFunctionNodes finds all SSA function nodes matching the target name.
// Supports formats: "funcName", "Type.Method", "pkg.funcName"
func findFunctionNodes(graph *callgraph.Graph, prog *ssa.Program, target string) []*callgraph.Node {
	var nodes []*callgraph.Node

	for fn, node := range graph.Nodes {
		if fn == nil {
			continue
		}
		if matchesFunctionName(fn, target) {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// matchesFunctionName checks if an SSA function matches the target name.
func matchesFunctionName(fn *ssa.Function, target string) bool {
	// Get the simple name
	name := fn.Name()

	// Check direct match
	if name == target {
		return true
	}

	// Check with receiver type (Type.Method)
	if recv := fn.Signature.Recv(); recv != nil {
		typeName := getTypeName(recv.Type())
		fullName := typeName + "." + name
		if fullName == target {
			return true
		}
		// Also try without pointer
		if strings.HasPrefix(target, "*") {
			if "*"+fullName == target {
				return true
			}
		}
	}

	// Check with package path (pkg.funcName)
	if fn.Pkg != nil {
		pkgName := fn.Pkg.Pkg.Name()
		fullName := pkgName + "." + name
		if fullName == target {
			return true
		}
	}

	return false
}

// getTypeName extracts the type name from a types.Type, handling pointers.
func getTypeName(t types.Type) string {
	switch typ := t.(type) {
	case *types.Pointer:
		return getTypeName(typ.Elem())
	case *types.Named:
		return typ.Obj().Name()
	default:
		return t.String()
	}
}

// formatFuncName returns a consistent function name for instrumentation matching.
func formatFuncName(fn *ssa.Function) string {
	if fn == nil {
		return ""
	}

	name := fn.Name()

	// Skip synthetic functions
	if strings.HasPrefix(name, "$") || name == "init" {
		return ""
	}

	// Add receiver type for methods
	if recv := fn.Signature.Recv(); recv != nil {
		typeName := getTypeName(recv.Type())
		return typeName + "." + name
	}

	return name
}

// findCalleesFrom finds all functions called by the source function (and their callees).
// It returns a set of function names (including receiver types for methods).
func findCalleesFrom(graph *callgraph.Graph, prog *ssa.Program, source string) (map[string]bool, error) {
	// Find the source node(s) in the graph
	sourceNodes := findFunctionNodes(graph, prog, source)
	if len(sourceNodes) == 0 {
		return nil, fmt.Errorf("function %q not found in call graph", source)
	}

	// BFS forward from source to find all callees
	callees := make(map[string]bool)
	visited := make(map[*callgraph.Node]bool)
	queue := make([]*callgraph.Node, 0, len(sourceNodes))

	for _, node := range sourceNodes {
		queue = append(queue, node)
		visited[node] = true
		callees[formatFuncName(node.Func)] = true
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Visit all callees (outgoing edges)
		for _, edge := range current.Out {
			callee := edge.Callee
			if callee == nil || visited[callee] {
				continue
			}
			visited[callee] = true

			name := formatFuncName(callee.Func)
			if name != "" && name != "<root>" {
				callees[name] = true
			}

			queue = append(queue, callee)
		}
	}

	return callees, nil
}

// findPathSegment finds functions in the call path FROM source TO target.
// It intersects callers of target with callees of source.
func findPathSegment(graph *callgraph.Graph, prog *ssa.Program, source, target string) (map[string]bool, error) {
	// Get all callees from source (forward)
	callees, err := findCalleesFrom(graph, prog, source)
	if err != nil {
		return nil, err
	}

	// Get all callers to target (backward)
	callers, err := findCallersTo(graph, prog, target)
	if err != nil {
		return nil, err
	}

	// Intersect: only functions that are both reachable from source AND lead to target
	segment := make(map[string]bool)
	for fn := range callees {
		if callers[fn] {
			segment[fn] = true
		}
	}

	if len(segment) == 0 {
		return nil, fmt.Errorf("no call path found from %q to %q", source, target)
	}

	return segment, nil
}
