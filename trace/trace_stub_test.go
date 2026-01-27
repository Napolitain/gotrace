//go:build !debug

package trace

import "testing"

func TestTraceStub_Noops(t *testing.T) {
	Reset()
	SetThresholds(1, 2)
	SetColorize(true)

	end := Trace("stub", 1, "a")
	end("ret")

	if got := GetTraces(); got != nil {
		t.Fatalf("expected nil traces, got %v", got)
	}
	if got := GetHotPaths(); got != nil {
		t.Fatalf("expected nil hot paths, got %v", got)
	}

	out := captureOutput(t, func() {
		PrintSummary()
	})
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}
