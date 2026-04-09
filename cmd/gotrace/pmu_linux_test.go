//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestFormatCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value uint64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 12, want: "12"},
		{value: 1_234, want: "1,234"},
		{value: 12_345_678, want: "12,345,678"},
	}

	for _, tt := range tests {
		if got := formatCount(tt.value); got != tt.want {
			t.Fatalf("formatCount(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestPMUDisabledHelpers(t *testing.T) {
	oldPMU := *pmu
	*pmu = false
	t.Cleanup(func() {
		*pmu = oldPMU
		globalPMU = nil
	})

	if PMUEnabled() {
		t.Fatal("expected PMUEnabled to be false when --pmu is disabled")
	}
	if err := InitPMUForChild(); err != nil {
		t.Fatalf("InitPMUForChild: %v", err)
	}
	if got := ReadAndClosePMU(); got != (PMUCounters{}) {
		t.Fatalf("expected zero counters when PMU is disabled, got %#v", got)
	}
}

func TestPrintPMUSummary_FormatsCounters(t *testing.T) {
	oldPMU := *pmu
	*pmu = true
	t.Cleanup(func() {
		*pmu = oldPMU
	})

	out := captureCombinedOutput(t, func() {
		PrintPMUSummary(PMUCounters{
			CPUCycles:       2_000,
			Instructions:    4_000,
			CacheReferences: 1_000,
			CacheMisses:     25,
			BranchMisses:    7,
		})
	})

	for _, fragment := range []string{
		"Hardware Counters",
		"2,000",
		"4,000",
		"2.00 IPC",
		"25",
		"2.50% miss rate",
		"7",
	} {
		if !strings.Contains(out, fragment) {
			t.Fatalf("expected PMU summary to contain %q, got:\n%s", fragment, out)
		}
	}
}
