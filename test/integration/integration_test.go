//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	submodulePath  = "test/projects/golang-example"
	submoduleHello = "test/projects/golang-example/hello"
)

func TestGotraceIntegration_ToggleSubmodule(t *testing.T) {
	root := repoRoot(t)
	relativeSubmodule := filepath.FromSlash(submodulePath)
	submodule := filepath.Join(root, relativeSubmodule)
	if !isSubmoduleReady(submodule) {
		t.Skip("submodule not initialized; run: git submodule update --init --recursive")
	}

	t.Cleanup(func() {
		_ = runCmd(root, "go", "run", "./cmd/gotrace", "--remove", relativeSubmodule)
	})

	if err := runCmd(root, "go", "run", "./cmd/gotrace", "--add", relativeSubmodule); err != nil {
		t.Fatalf("add instrumentation: %v", err)
	}
	if err := runCmd(root, "go", "run", "./cmd/gotrace", "--remove", relativeSubmodule); err != nil {
		t.Fatalf("remove instrumentation: %v", err)
	}
}

func TestGotraceIntegration_DebugBuild(t *testing.T) {
	root := repoRoot(t)
	helloRel := filepath.FromSlash(submoduleHello)
	helloPath := filepath.Join(root, helloRel)
	if !isSubmoduleReady(helloPath) {
		t.Skip("submodule not initialized; run: git submodule update --init --recursive")
	}

	t.Cleanup(func() {
		_ = runCmd(root, "go", "run", "./cmd/gotrace", "--remove", helloRel)
	})

	workFile := filepath.Join(t.TempDir(), "go.work")
	if err := writeGoWork(workFile, root, helloPath); err != nil {
		t.Fatalf("write go.work: %v", err)
	}
	t.Setenv("GOWORK", workFile)

	if err := runCmd(root, "go", "run", "./cmd/gotrace", "--add", helloRel); err != nil {
		t.Fatalf("add instrumentation: %v", err)
	}

	if err := runCmd(helloPath, "go", "run", "-tags", "debug", "."); err != nil {
		t.Fatalf("debug run: %v", err)
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
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false
	}
	return true
}

func runCmd(dir string, args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, output.String())
	}

	return nil
}

func writeGoWork(path, root, module string) error {
	content := fmt.Sprintf("go 1.24\n\nuse (\n\t%s\n\t%s\n)\n", root, module)
	return os.WriteFile(path, []byte(content), 0644)
}
