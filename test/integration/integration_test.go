//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	submodulePath  = "test/projects/golang-example"
	submoduleHello = "test/projects/golang-example/hello"
)

func TestGotraceIntegration_HotRun(t *testing.T) {
	root := repoRoot(t)
	helloRel := filepath.FromSlash(submoduleHello)
	helloPath := filepath.Join(root, helloRel)
	if !isSubmoduleReady(helloPath) {
		t.Skip("submodule not initialized; run: git submodule update --init --recursive")
	}

	workFile := filepath.Join(t.TempDir(), "go.work")
	if err := writeGoWork(workFile, root, helloPath); err != nil {
		t.Fatalf("write go.work: %v", err)
	}

	// Run gotrace with hot instrumentation
	output, err := runCmdOutput(root, "go", "run", "./cmd/gotrace", helloRel)
	if err != nil {
		t.Fatalf("hot run failed: %v\nOutput: %s", err, output)
	}

	// Verify trace output is present
	if !strings.Contains(output, "→") || !strings.Contains(output, "←") {
		t.Errorf("expected trace output with entry/exit arrows, got:\n%s", output)
	}
}

func TestGotraceIntegration_DryRun(t *testing.T) {
	root := repoRoot(t)
	helloRel := filepath.FromSlash(submoduleHello)
	helloPath := filepath.Join(root, helloRel)
	if !isSubmoduleReady(helloPath) {
		t.Skip("submodule not initialized; run: git submodule update --init --recursive")
	}

	// Run gotrace with --dry-run
	output, err := runCmdOutput(root, "go", "run", "./cmd/gotrace", "--dry-run", helloRel)
	if err != nil {
		t.Fatalf("dry-run failed: %v\nOutput: %s", err, output)
	}

	// Verify dry-run output mentions files that would be instrumented
	if !strings.Contains(output, "Would instrument") {
		t.Errorf("expected dry-run output to mention 'Would instrument', got:\n%s", output)
	}
}

func TestGotraceIntegration_NoFilesModified(t *testing.T) {
	root := repoRoot(t)
	helloRel := filepath.FromSlash(submoduleHello)
	helloPath := filepath.Join(root, helloRel)
	if !isSubmoduleReady(helloPath) {
		t.Skip("submodule not initialized; run: git submodule update --init --recursive")
	}

	// Read original file content
	helloGoPath := filepath.Join(helloPath, "hello.go")
	originalContent, err := os.ReadFile(helloGoPath)
	if err != nil {
		t.Fatalf("read original file: %v", err)
	}

	workFile := filepath.Join(t.TempDir(), "go.work")
	if err := writeGoWork(workFile, root, helloPath); err != nil {
		t.Fatalf("write go.work: %v", err)
	}

	// Run gotrace
	_, err = runCmdOutput(root, "go", "run", "./cmd/gotrace", helloRel)
	if err != nil {
		t.Fatalf("hot run failed: %v", err)
	}

	// Verify file was not modified
	afterContent, err := os.ReadFile(helloGoPath)
	if err != nil {
		t.Fatalf("read file after run: %v", err)
	}

	if !bytes.Equal(originalContent, afterContent) {
		t.Error("source file was modified on disk - hot instrumentation should not modify files")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}

func isSubmoduleReady(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	// Check for .git file or directory (submodules use .git file pointing to parent)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	// Also check if there are any .go files (fallback for detached submodules)
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

func runCmdOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	return output.String(), err
}

func writeGoWork(path, root, module string) error {
	content := fmt.Sprintf("go 1.24\n\nuse (\n\t%s\n\t%s\n)\n", root, module)
	return os.WriteFile(path, []byte(content), 0644)
}
