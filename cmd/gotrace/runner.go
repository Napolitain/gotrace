package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/mod/modfile"
)

// RunHot instruments the target in-memory, compiles, and executes it
func RunHot(target string, args []string) error {
	// Resolve target to absolute path
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}

	info, err := os.Stat(absTarget)
	if err != nil {
		return fmt.Errorf("stat target: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target must be a directory containing a Go package")
	}

	// Find module root
	moduleRoot, err := findModuleRoot(absTarget)
	if err != nil {
		return fmt.Errorf("find module root: %w", err)
	}
	if moduleRoot == "" {
		return fmt.Errorf("no go.mod found for %s", absTarget)
	}

	// If --until is specified, build call graph and find functions to instrument
	if *until != "" {
		if *verbose {
			fmt.Printf("Building call graph to find path to %q...\n", *until)
		}
		graph, prog, err := buildCallGraph(moduleRoot)
		if err != nil {
			return fmt.Errorf("build call graph: %w", err)
		}
		callers, err := findCallersTo(graph, prog, *until)
		if err != nil {
			return fmt.Errorf("find callers: %w", err)
		}
		if *verbose {
			fmt.Printf("Will instrument functions in call path to %q\n", *until)
		}
		allowedFuncs = callers
	}

	// Create temp directory for instrumented code
	tempDir, err := os.MkdirTemp("", "gotrace-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy and instrument the entire module
	if err := copyAndInstrumentModule(moduleRoot, tempDir); err != nil {
		return fmt.Errorf("instrument module: %w", err)
	}

	// Determine the relative path from module root to target
	relTarget, err := filepath.Rel(moduleRoot, absTarget)
	if err != nil {
		return fmt.Errorf("relative path: %w", err)
	}

	// Build the instrumented code
	buildTarget := filepath.Join(tempDir, relTarget)
	binaryName := "gotrace-binary"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(tempDir, binaryName)
	if err := buildInstrumented(buildTarget, binaryPath); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	// Run the binary
	return runBinary(binaryPath, args)
}

// copyAndInstrumentModule copies and instruments the entire module
func copyAndInstrumentModule(moduleRoot, tempDir string) error {
	// Check if this is the gotrace module itself
	isGotraceModule := false
	if modPath, err := readModulePath(filepath.Join(moduleRoot, "go.mod")); err == nil {
		isGotraceModule = (modPath == traceModule)
	}

	// Walk the module and process files
	return filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(tempDir, rel)

		// Skip hidden dirs, vendor, testdata
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "testdata" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(base, ".") && base != "." && base != ".." {
				return filepath.SkipDir
			}
			// Skip cmd/gotrace dir when instrumenting gotrace itself (avoid instrumenting the tool)
			if isGotraceModule {
				if rel == filepath.Join("cmd", "gotrace") {
					return filepath.SkipDir
				}
			}
			return os.MkdirAll(destPath, 0755)
		}

		// Read source file
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Handle go.mod specially - add gotrace dependency
		if filepath.Base(path) == "go.mod" {
			content, err = instrumentGoMod(content, moduleRoot)
			if err != nil {
				return fmt.Errorf("instrument go.mod: %w", err)
			}
			return os.WriteFile(destPath, content, 0644)
		}

		// Handle go.sum - copy as-is
		if filepath.Base(path) == "go.sum" {
			return os.WriteFile(destPath, content, 0644)
		}

		// Only process .go files
		if !strings.HasSuffix(path, ".go") {
			return os.WriteFile(destPath, content, 0644)
		}

		// Skip test files
		if strings.HasSuffix(path, "_test.go") {
			return os.WriteFile(destPath, content, 0644)
		}

		// Skip files with build tags that exclude normal builds
		if bytes.Contains(content, []byte("//go:build ignore")) {
			return os.WriteFile(destPath, content, 0644)
		}

		// Skip files that already import the trace package (already instrumented)
		if bytes.Contains(content, []byte(tracePkg)) {
			return os.WriteFile(destPath, content, 0644)
		}

		// Skip files in trace/ directory when instrumenting gotrace itself
		if isGotraceModule && strings.HasPrefix(rel, "trace"+string(filepath.Separator)) {
			return os.WriteFile(destPath, content, 0644)
		}

		// Instrument the Go file
		instrumented, err := instrumentFile(path, content)
		if err != nil {
			return fmt.Errorf("instrument %s: %w", path, err)
		}

		return os.WriteFile(destPath, instrumented, 0644)
	})
}

// instrumentFile instruments a single Go file in memory
func instrumentFile(filename string, content []byte) ([]byte, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	// Use existing instrumentation logic
	modified := instrumentAST(node)
	if !modified {
		return content, nil
	}

	// Format the instrumented code
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return nil, fmt.Errorf("format: %w", err)
	}

	return buf.Bytes(), nil
}

// instrumentGoMod adds the gotrace dependency to go.mod
func instrumentGoMod(content []byte, moduleRoot string) ([]byte, error) {
	mod, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return nil, err
	}

	// Don't modify gotrace's own go.mod - it already has the trace package
	if mod.Module != nil && mod.Module.Mod.Path == traceModule {
		return content, nil
	}

	// Find local gotrace root for replace directive
	localRoot := findLocalGotraceRoot()
	traceVersion := resolveTraceVersion()

	if localRoot == "" && traceVersion == "v0.0.0" {
		return nil, fmt.Errorf("unable to locate gotrace module; run from gotrace repo or ensure it's installed")
	}

	// Add require
	if !hasRequire(mod, traceModule) {
		if err := mod.AddRequire(traceModule, traceVersion); err != nil {
			return nil, err
		}
	}

	// Add replace if local
	if localRoot != "" {
		replacePath := localRoot
		// Use absolute path for temp dir builds
		if !hasReplace(mod, traceModule, replacePath) {
			if err := mod.AddReplace(traceModule, "", replacePath, ""); err != nil {
				return nil, err
			}
		}
	}

	return mod.Format()
}

// buildInstrumented compiles the instrumented code
func buildInstrumented(targetDir, outputPath string) error {
	cmd := exec.Command("go", "build", "-o", outputPath, ".")
	cmd.Dir = targetDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if *verbose {
		fmt.Printf("Building %s...\n", targetDir)
	}

	return cmd.Run()
}

// runBinary executes the compiled binary with argument forwarding
func runBinary(binaryPath string, args []string) error {
	cmd := exec.Command(binaryPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Handle signals - forward to child process
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	if err := cmd.Start(); err != nil {
		signal.Stop(sigCh)
		return fmt.Errorf("start: %w", err)
	}

	// Forward signals to child in a goroutine
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				if cmd.Process != nil {
					cmd.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	signal.Stop(sigCh)
	close(done)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}

	return nil
}
