//go:build !linux

package main

import "fmt"

// PMUCounters holds hardware performance counter values (stub for non-Linux)
type PMUCounters struct {
	CPUCycles       uint64
	Instructions    uint64
	CacheReferences uint64
	CacheMisses     uint64
	BranchMisses    uint64
}

// PMUGroup is a stub for non-Linux systems
type PMUGroup struct{}

// SetupPMU is a no-op on non-Linux systems
func SetupPMU(pid int) (*PMUGroup, error) {
	if *pmu {
		fmt.Println("Warning: --pmu is only supported on Linux")
	}
	return nil, nil
}

// Read returns zero counters on non-Linux systems
func (g *PMUGroup) Read() (PMUCounters, error) {
	return PMUCounters{}, nil
}

// Close is a no-op on non-Linux systems
func (g *PMUGroup) Close() {}

// PMUEnabled returns false on non-Linux systems
func PMUEnabled() bool {
	return false
}

// PrintPMUSummary is a no-op on non-Linux systems
func PrintPMUSummary(counters PMUCounters) {}

// InitPMUForChild is a no-op on non-Linux systems
func InitPMUForChild() error {
	if *pmu {
		fmt.Println("Warning: --pmu is only supported on Linux")
	}
	return nil
}

// ReadAndClosePMU returns zero counters on non-Linux systems
func ReadAndClosePMU() PMUCounters {
	return PMUCounters{}
}
