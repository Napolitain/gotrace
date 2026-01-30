package main

import (
	"bytes"
	"fmt"
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

	// Validate flag combinations
	if *functionFlag != "" && (*from != "" || *until != "") {
		return fmt.Errorf("--function cannot be used with --from or --until")
	}

	// Find module root
	moduleRoot, err := findModuleRoot(absTarget)
	if err != nil {
		return fmt.Errorf("find module root: %w", err)
	}
	if moduleRoot == "" {
		return fmt.Errorf("no go.mod found for %s", absTarget)
	}

	// Handle call graph filtering based on --from and --until flags
	if *from != "" || *until != "" {
		if *verbose {
			if *from != "" && *until != "" {
				fmt.Printf("Building call graph to find path from %q to %q...\n", *from, *until)
			} else if *from != "" {
				fmt.Printf("Building call graph to find callees from %q...\n", *from)
			} else {
				fmt.Printf("Building call graph to find path to %q...\n", *until)
			}
		}

		graph, prog, err := buildCallGraph(moduleRoot)
		if err != nil {
			return fmt.Errorf("build call graph: %w", err)
		}

		var funcs map[string]bool
		switch {
		case *from != "" && *until != "":
			// Path segment: from source to target
			funcs, err = findPathSegment(graph, prog, *from, *until)
			if err != nil {
				return fmt.Errorf("find path segment: %w", err)
			}
			if *verbose {
				fmt.Printf("Will instrument %d functions in path from %q to %q\n", len(funcs), *from, *until)
			}
		case *from != "":
			// Forward: from source to all callees
			funcs, err = findCalleesFrom(graph, prog, *from)
			if err != nil {
				return fmt.Errorf("find callees: %w", err)
			}
			if *verbose {
				fmt.Printf("Will instrument %d functions called from %q\n", len(funcs), *from)
			}
		default:
			// Backward: all callers to target (existing behavior)
			funcs, err = findCallersTo(graph, prog, *until)
			if err != nil {
				return fmt.Errorf("find callers: %w", err)
			}
			if *verbose {
				fmt.Printf("Will instrument functions in call path to %q\n", *until)
			}
		}
		allowedFuncs = funcs
	}

	// If --function is specified, only instrument that function
	if *functionFlag != "" {
		if *verbose {
			fmt.Printf("Micro-benchmark mode: only tracing %q\n", *functionFlag)
		}
		targetFunction = *functionFlag
	}

	// Create temp directory for instrumented code
	tempDir, err := os.MkdirTemp("", "gotrace-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	if !*verbose {
		defer os.RemoveAll(tempDir)
	} else {
		fmt.Printf("Temp directory (not cleaned up in verbose mode): %s\n", tempDir)
	}

	// Copy and instrument the entire module
	if err := copyAndInstrumentModule(moduleRoot, tempDir); err != nil {
		return fmt.Errorf("instrument module: %w", err)
	}

	// Determine the relative path from module root to target
	relTarget, err := filepath.Rel(moduleRoot, absTarget)
	if err != nil {
		return fmt.Errorf("relative path: %w", err)
	}

	// Run go mod tidy to sync dependencies after adding gotrace import
	if err := runGoModTidy(tempDir); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
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
			// Skip test/projects - these are submodules for testing, not part of the target
			if rel == filepath.Join("test", "projects") {
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

		// Instrument the Go file using source-level injection (preserves all comments/directives)
		instrumented, err := instrumentFileText(path, content)
		if err != nil {
			return fmt.Errorf("instrument %s: %w", path, err)
		}

		return os.WriteFile(destPath, instrumented, 0644)
	})
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

// runGoModTidy runs go mod tidy in the given directory
func runGoModTidy(dir string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if *verbose {
		fmt.Printf("Running go mod tidy in %s...\n", dir)
	}

	return cmd.Run()
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

	// Setup PMU counters (will be inherited by child with enable_on_exec)
	if err := InitPMUForChild(); err != nil {
		return fmt.Errorf("setup PMU: %w", err)
	}

	// Handle signals - forward to child process
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	if err := cmd.Start(); err != nil {
		signal.Stop(sigCh)
		ReadAndClosePMU() // cleanup PMU on error
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

	// Read PMU counters after child exits
	pmuCounters := ReadAndClosePMU()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Print PMU summary even on non-zero exit
			PrintPMUSummary(pmuCounters)
			os.Exit(exitErr.ExitCode())
		}
		return err
	}

	// Print PMU summary
	PrintPMUSummary(pmuCounters)

	return nil
}
