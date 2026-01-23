//go:build !debug

package trace

var noop = func(...any) {}

func Trace(string, ...any) func(...any) { return noop }

type Entry struct {
	Name     string
	Args     []any
	Returns  []any
	Depth    int32
	StartNs  int64
	EndNs    int64
	Duration int64
	GID      uint64
	File     string
	Line     int
	Panicked bool
	PanicVal any
}

func GetTraces() []Entry     { return nil }
func GetHotPaths() []Entry   { return nil }
func Reset()                 {}
func SetThresholds(_, _ int64) {}
func SetColorize(_ bool)     {}
func ExportPerfetto(_ string) error { return nil }
func PrintSummary()          {}
