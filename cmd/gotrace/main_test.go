package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessDirectory_ToggleInstrumentation(t *testing.T) {
	setFlags(false, false, false, "")

	tempDir := t.TempDir()
	fixtureDir := filepath.Join("testdata", "fixtures", "project")
	if err := copyDir(fixtureDir, tempDir); err != nil {
		t.Fatalf("copy fixtures: %v", err)
	}
	goModPath := filepath.Join(tempDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module example.com/project\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := processDirectory(tempDir); err != nil {
		t.Fatalf("instrument: %v", err)
	}

	mainPath := filepath.Join(tempDir, "main.go")
	assertContains(t, mainPath, marker)
	assertContains(t, mainPath, "defer trace.Trace(\"main\")()")
	assertContains(t, mainPath, "trace.PrintSummary()")
	assertContains(t, mainPath, tracePkg)

	helperPath := filepath.Join(tempDir, "helper.go")
	assertContains(t, helperPath, "defer trace.Trace(\"fibonacci\", n)()")

	methodPath := filepath.Join(tempDir, "methods.go")
	assertContains(t, methodPath, "defer trace.Trace(\"Calculator.Add\", a, b)()")

	subPath := filepath.Join(tempDir, "subpkg", "sub.go")
	assertContains(t, subPath, "defer trace.Trace(\"Do\", n)()")

	assertNotContains(t, filepath.Join(tempDir, "nofunc.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, "buildtag_debug.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, "buildtag_nodebug.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, "file_test.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, "vendor", "vendor.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, "testdata", "skip.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, "cmd", "app", "app.go"), marker)
	assertNotContains(t, filepath.Join(tempDir, ".hidden", "hidden.go"), marker)
	assertContains(t, goModPath, traceModule)

	if err := processDirectory(tempDir); err != nil {
		t.Fatalf("uninstrument: %v", err)
	}

	assertNotContains(t, mainPath, marker)
	assertNotContains(t, mainPath, "trace.Trace")
	assertNotContains(t, mainPath, "trace.PrintSummary")
	assertNotContains(t, mainPath, tracePkg)
	assertNotContains(t, helperPath, "trace.Trace")
	assertNotContains(t, methodPath, "trace.Trace")
	assertNotContains(t, subPath, "trace.Trace")
	assertNotContains(t, goModPath, traceModule)
}

func setFlags(add, remove, dry bool, pat string) {
	*forceAdd = add
	*forceRemove = remove
	*dryRun = dry
	*verbose = false
	*pattern = pat
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}

func assertContains(t *testing.T, path, needle string) {
	t.Helper()
	content := readFile(t, path)
	if !strings.Contains(content, needle) {
		t.Fatalf("expected %s to contain %q", path, needle)
	}
}

func assertNotContains(t *testing.T, path, needle string) {
	t.Helper()
	content := readFile(t, path)
	if strings.Contains(content, needle) {
		t.Fatalf("expected %s to not contain %q", path, needle)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
