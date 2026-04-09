package trace

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func captureOutput(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	_ = r.Close()

	return buf.String()
}

func captureStreams(t *testing.T, fn func()) (stdout string, stderr string, panicVal any) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW

	func() {
		defer func() {
			panicVal = recover()
			_ = stdoutW.Close()
			_ = stderrW.Close()
			os.Stdout = oldStdout
			os.Stderr = oldStderr
		}()

		fn()
	}()

	stdout = readPipe(t, stdoutR)
	stderr = readPipe(t, stderrR)

	return stdout, stderr, panicVal
}

func readPipe(t *testing.T, r *os.File) string {
	t.Helper()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return buf.String()
}
