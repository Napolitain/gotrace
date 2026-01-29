//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// PMUCounters holds hardware performance counter values
type PMUCounters struct {
	CPUCycles       uint64
	Instructions    uint64
	CacheReferences uint64
	CacheMisses     uint64
	BranchMisses    uint64
}

// PMUGroup manages a group of perf event file descriptors
type PMUGroup struct {
	fds    []int
	leader int
}

// perf_event_attr structure for perf_event_open syscall
type perfEventAttr struct {
	Type               uint32
	Size               uint32
	Config             uint64
	SamplePeriodOrFreq uint64
	SampleType         uint64
	ReadFormat         uint64
	Flags              uint64
	WakeupEventsOrWM   uint32
	BPType             uint32
	BPAddrOrConfig1    uint64
	BPLenOrConfig2     uint64
	BranchSampleType   uint64
	SampleRegsUser     uint64
	SampleStackUser    uint32
	ClockID            int32
	SampleRegsIntr     uint64
	AuxWatermark       uint32
	SampleMaxStack     uint16
	Reserved2          uint16
}

// Constants for perf_event_open
const (
	PERF_TYPE_HARDWARE = 0

	PERF_COUNT_HW_CPU_CYCLES       = 0
	PERF_COUNT_HW_INSTRUCTIONS     = 1
	PERF_COUNT_HW_CACHE_REFERENCES = 2
	PERF_COUNT_HW_CACHE_MISSES     = 3
	PERF_COUNT_HW_BRANCH_MISSES    = 5

	PERF_FLAG_FD_CLOEXEC = 1 << 3
)

// Attribute flags
const (
	attrFlagDisabled    = 1 << 0
	attrFlagInherit     = 1 << 1
	attrFlagExcludeKern = 1 << 7
	attrFlagExcludeHV   = 1 << 8
	attrFlagEnableOnExec = 1 << 12
)

var pmuCounterConfigs = []struct {
	name   string
	config uint64
}{
	{"cpu_cycles", PERF_COUNT_HW_CPU_CYCLES},
	{"instructions", PERF_COUNT_HW_INSTRUCTIONS},
	{"cache_references", PERF_COUNT_HW_CACHE_REFERENCES},
	{"cache_misses", PERF_COUNT_HW_CACHE_MISSES},
	{"branch_misses", PERF_COUNT_HW_BRANCH_MISSES},
}

// perfEventOpen wraps the perf_event_open syscall
func perfEventOpen(attr *perfEventAttr, pid, cpu, groupFD int, flags uint) (int, error) {
	ret, _, errno := syscall.Syscall6(
		syscall.SYS_PERF_EVENT_OPEN,
		uintptr(unsafe.Pointer(attr)),
		uintptr(pid),
		uintptr(cpu),
		uintptr(groupFD),
		uintptr(flags),
		0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(ret), nil
}

// SetupPMU creates a PMU group for the given process ID (0 = calling process)
// Returns nil if PMU is not available or not requested
func SetupPMU(pid int) (*PMUGroup, error) {
	if !*pmu {
		return nil, nil
	}

	group := &PMUGroup{
		fds:    make([]int, 0, len(pmuCounterConfigs)),
		leader: -1,
	}

	for i, cfg := range pmuCounterConfigs {
		attr := perfEventAttr{
			Type:   PERF_TYPE_HARDWARE,
			Size:   uint32(unsafe.Sizeof(perfEventAttr{})),
			Config: cfg.config,
			Flags:  attrFlagDisabled | attrFlagExcludeKern | attrFlagExcludeHV | attrFlagInherit | attrFlagEnableOnExec,
		}

		groupFD := -1
		if i > 0 {
			groupFD = group.leader
		}

		fd, err := perfEventOpen(&attr, pid, -1, groupFD, PERF_FLAG_FD_CLOEXEC)
		if err != nil {
			group.Close()
			return nil, fmt.Errorf("perf_event_open for %s: %w (try: sudo sysctl kernel.perf_event_paranoid=-1)", cfg.name, err)
		}

		if i == 0 {
			group.leader = fd
		}
		group.fds = append(group.fds, fd)
	}

	return group, nil
}

// Read reads current counter values from all PMU counters
func (g *PMUGroup) Read() (PMUCounters, error) {
	if g == nil {
		return PMUCounters{}, nil
	}

	var counters PMUCounters
	buf := make([]byte, 8)

	for i, fd := range g.fds {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			return counters, fmt.Errorf("read counter %d: %w", i, err)
		}
		if n != 8 {
			return counters, fmt.Errorf("short read: got %d bytes", n)
		}

		value := *(*uint64)(unsafe.Pointer(&buf[0]))

		switch i {
		case 0:
			counters.CPUCycles = value
		case 1:
			counters.Instructions = value
		case 2:
			counters.CacheReferences = value
		case 3:
			counters.CacheMisses = value
		case 4:
			counters.BranchMisses = value
		}
	}

	return counters, nil
}

// Close closes all file descriptors in the PMU group
func (g *PMUGroup) Close() {
	if g == nil {
		return
	}
	for _, fd := range g.fds {
		if fd >= 0 {
			syscall.Close(fd)
		}
	}
	g.fds = nil
}

// PMUEnabled returns true if PMU collection is requested and available
func PMUEnabled() bool {
	return *pmu
}

// PrintPMUSummary prints PMU counter summary to stdout
func PrintPMUSummary(counters PMUCounters) {
	if !*pmu {
		return
	}

	fmt.Println()
	fmt.Println("🔧 Hardware Counters (process total)")
	fmt.Println("  " + string([]byte{0xe2, 0x94, 0x80}[0:3]) + strings.Repeat(string([]byte{0xe2, 0x94, 0x80}), 59))

	fmt.Printf("  CPU Cycles:        %15s\n", formatCount(counters.CPUCycles))
	
	ipc := float64(0)
	if counters.CPUCycles > 0 {
		ipc = float64(counters.Instructions) / float64(counters.CPUCycles)
	}
	fmt.Printf("  Instructions:      %15s    (%.2f IPC)\n", formatCount(counters.Instructions), ipc)
	
	fmt.Printf("  Cache References:  %15s\n", formatCount(counters.CacheReferences))
	
	cacheMissRate := float64(0)
	if counters.CacheReferences > 0 {
		cacheMissRate = float64(counters.CacheMisses) / float64(counters.CacheReferences) * 100
	}
	fmt.Printf("  Cache Misses:      %15s    (%.2f%% miss rate)\n", formatCount(counters.CacheMisses), cacheMissRate)
	
	fmt.Printf("  Branch Misses:     %15s\n", formatCount(counters.BranchMisses))
}

// formatCount formats a number with thousands separators
func formatCount(n uint64) string {
	if n == 0 {
		return "0"
	}
	
	s := fmt.Sprintf("%d", n)
	
	// Add thousands separators
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// Global PMU group for the traced process
var globalPMU *PMUGroup

// InitPMUForChild sets up PMU to be inherited by child process
func InitPMUForChild() error {
	if !*pmu {
		return nil
	}
	
	var err error
	globalPMU, err = SetupPMU(0) // 0 = current process, will be inherited
	if err != nil {
		return err
	}
	return nil
}

// ReadAndClosePMU reads final PMU values and closes
func ReadAndClosePMU() PMUCounters {
	if globalPMU == nil {
		return PMUCounters{}
	}
	
	counters, err := globalPMU.Read()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to read PMU counters: %v\n", err)
	}
	globalPMU.Close()
	globalPMU = nil
	return counters
}
