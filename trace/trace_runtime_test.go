package trace

import (
	"strings"
	"testing"
)

func TestTraceOnPanic_RecordsAndPrintsPanic(t *testing.T) {
	Reset()
	SetColorize(false)
	SetThresholds(0, 0)
	t.Cleanup(Reset)

	stdout, stderr, panicVal := captureStreams(t, func() {
		func() {
			defer TraceOnPanic("explode", 7)()
			panic("boom")
		}()
	})

	if panicVal != "boom" {
		t.Fatalf("expected recovered panic value boom, got %#v", panicVal)
	}
	if stdout == "" {
		t.Fatal("expected panic output on stdout")
	}
	if stderr == "" {
		t.Fatal("expected panic stack output on stderr")
	}
	if got := len(GetTraces()); got != 1 {
		t.Fatalf("expected 1 trace, got %d", got)
	}

	trace := GetTraces()[0]
	if !trace.Panicked {
		t.Fatal("expected trace to be marked as panicked")
	}
	if trace.PanicVal != "boom" {
		t.Fatalf("expected panic value boom, got %#v", trace.PanicVal)
	}
}

func TestTraceOnPanic_IsSilentOnNormalExit(t *testing.T) {
	Reset()
	SetColorize(false)
	t.Cleanup(Reset)

	stdout, stderr, panicVal := captureStreams(t, func() {
		func() {
			defer TraceOnPanic("quiet", 1)()
		}()
	})

	if panicVal != nil {
		t.Fatalf("did not expect panic, got %#v", panicVal)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("expected no output on normal TraceOnPanic exit, got stdout=%q stderr=%q", stdout, stderr)
	}
	if got := len(GetTraces()); got != 1 {
		t.Fatalf("expected 1 trace, got %d", got)
	}
	if GetTraces()[0].Panicked {
		t.Fatal("did not expect normal exit to be marked as panicked")
	}
}

func TestPrintFunctionStats_FormatsAggregates(t *testing.T) {
	Reset()
	SetColorize(false)
	SetThresholds(1_500, 3_000)
	t.Cleanup(Reset)

	mu.Lock()
	traces = []Entry{
		{Name: "work", Duration: 1_000},
		{Name: "work", Duration: 2_000},
		{Name: "work", Duration: 4_000},
		{Name: "other", Duration: 10_000},
	}
	mu.Unlock()

	out := captureOutput(t, func() {
		PrintFunctionStats("work")
	})

	for _, fragment := range []string{
		"Function Micro-Benchmark",
		"Function: work",
		"Invocations:     3",
		"Total Time:      7.00µs",
		"Min:           1.00µs",
		"P95:           2.00µs",
		"P99:           2.00µs",
		"Std Dev:       1.25µs",
	} {
		if !containsTrimmed(out, fragment) {
			t.Fatalf("expected output to contain %q, got:\n%s", fragment, out)
		}
	}
}

func TestPrintFunctionStats_NoInvocations(t *testing.T) {
	Reset()
	SetColorize(false)
	t.Cleanup(Reset)

	out := captureOutput(t, func() {
		PrintFunctionStats("missing")
	})

	if !containsTrimmed(out, "No invocations recorded") {
		t.Fatalf("expected missing-function message, got:\n%s", out)
	}
}

func TestTrace_SummaryOnlySuppressesLiveOutput(t *testing.T) {
	Reset()
	SetColorize(false)
	oldSummaryOnly := summaryOnly.Load()
	SetSummaryOnly(true)
	t.Cleanup(func() {
		SetSummaryOnly(oldSummaryOnly)
		Reset()
	})

	out := captureOutput(t, func() {
		func() {
			defer Trace("quiet", 1)()
		}()
	})

	if out != "" {
		t.Fatalf("expected no live trace output in summary-only mode, got %q", out)
	}
	if got := len(GetTraces()); got != 1 {
		t.Fatalf("expected 1 recorded trace, got %d", got)
	}

	summary := captureOutput(t, func() {
		PrintSummary()
	})
	if !containsTrimmed(summary, "GoTrace Summary") || !containsTrimmed(summary, "quiet") {
		t.Fatalf("expected summary output to remain available, got:\n%s", summary)
	}
}

func containsTrimmed(output, fragment string) bool {
	return len(output) > 0 && strings.Contains(output, fragment)
}
