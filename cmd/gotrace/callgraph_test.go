package main

import (
	"path/filepath"
	"testing"
)

func TestFindCallersTo_ReturnsTransitiveCallers(t *testing.T) {
	moduleRoot := writeCallGraphFixture(t)

	graph, prog, err := buildCallGraph(moduleRoot)
	if err != nil {
		t.Fatalf("buildCallGraph: %v", err)
	}

	callers, err := findCallersTo(graph, prog, "leaf")
	if err != nil {
		t.Fatalf("findCallersTo: %v", err)
	}

	assertSetContains(t, callers, "leaf", "mid", "entry", "main")
	assertSetNotContains(t, callers, "helper", "Calculator.Add", "Do")
}

func TestFindCalleesFrom_HandlesMethodsAndPackageQualifiedNames(t *testing.T) {
	moduleRoot := writeCallGraphFixture(t)

	graph, prog, err := buildCallGraph(moduleRoot)
	if err != nil {
		t.Fatalf("buildCallGraph: %v", err)
	}

	callees, err := findCalleesFrom(graph, prog, "main.helper")
	if err != nil {
		t.Fatalf("findCalleesFrom: %v", err)
	}

	assertSetContains(t, callees, "helper", "Calculator.Add", "Do")
	assertSetNotContains(t, callees, "mid", "leaf")
}

func TestFindPathSegment_ReturnsOnlyFunctionsOnPath(t *testing.T) {
	moduleRoot := writeCallGraphFixture(t)

	graph, prog, err := buildCallGraph(moduleRoot)
	if err != nil {
		t.Fatalf("buildCallGraph: %v", err)
	}

	segment, err := findPathSegment(graph, prog, "entry", "leaf")
	if err != nil {
		t.Fatalf("findPathSegment: %v", err)
	}

	assertSetContains(t, segment, "entry", "mid", "leaf")
	assertSetNotContains(t, segment, "helper", "Calculator.Add", "Do")
}

func TestBuildCallGraph_ReturnsErrorsForBrokenPackages(t *testing.T) {
	moduleRoot := t.TempDir()
	writeTestFile(t, moduleRoot, "go.mod", "module example.com/broken\n\ngo 1.24\n")
	writeTestFile(t, moduleRoot, "main.go", "package main\n\nfunc main() {\n\tif\n}\n")

	if _, _, err := buildCallGraph(moduleRoot); err == nil {
		t.Fatal("expected buildCallGraph to fail for invalid Go source")
	}
}

func writeCallGraphFixture(t *testing.T) string {
	t.Helper()

	moduleRoot := t.TempDir()
	writeTestFile(t, moduleRoot, "go.mod", "module example.com/callgraphtest\n\ngo 1.24\n")
	writeTestFile(t, moduleRoot, "main.go", `package main

import "example.com/callgraphtest/subpkg"

func main() {
	entry()
}

func entry() {
	mid()
	helper()
}

func mid() {
	leaf()
}

func leaf() {}

type Calculator struct{}

func (Calculator) Add(a, b int) int {
	return a + b
}

func helper() {
	var c Calculator
	_ = c.Add(1, 2)
	_ = subpkg.Do(3)
}
`)
	writeTestFile(t, moduleRoot, filepath.Join("subpkg", "sub.go"), `package subpkg

func Do(n int) int {
	return n * 2
}
`)

	return moduleRoot
}

func assertSetContains(t *testing.T, set map[string]bool, names ...string) {
	t.Helper()

	for _, name := range names {
		if !set[name] {
			t.Fatalf("expected set to contain %q, got %#v", name, set)
		}
	}
}

func assertSetNotContains(t *testing.T, set map[string]bool, names ...string) {
	t.Helper()

	for _, name := range names {
		if set[name] {
			t.Fatalf("expected set to not contain %q, got %#v", name, set)
		}
	}
}
