package trace

import (
	"strings"
	"testing"
)

func TestTraceDebug_RecordsAndSummarizes(t *testing.T) {
	Reset()
	SetColorize(false)
	SetThresholds(0, 0)

	captureOutput(t, func() {
		func() {
			defer Trace("work", 1, "a")()
		}()
	})

	traces := GetTraces()
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].Name != "work" {
		t.Fatalf("expected trace name work, got %q", traces[0].Name)
	}
	if len(traces[0].Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(traces[0].Args))
	}
	if traces[0].Duration < 0 {
		t.Fatalf("expected non-negative duration, got %d", traces[0].Duration)
	}

	hot := GetHotPaths()
	if len(hot) != 1 {
		t.Fatalf("expected 1 hot path, got %d", len(hot))
	}

	out := captureOutput(t, func() {
		PrintSummary()
	})
	if !strings.Contains(out, "GoTrace Summary") {
		t.Fatalf("expected summary output, got %q", out)
	}
	if !strings.Contains(out, "work") {
		t.Fatalf("expected function name in summary, got %q", out)
	}

	Reset()
	if got := len(GetTraces()); got != 0 {
		t.Fatalf("expected traces reset, got %d", got)
	}
}
