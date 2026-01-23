package main

import (
	"fmt"

	"github.com/napolitain/gotrace/trace"
)

func main() {
	defer trace.Trace("main")()

	result := fibonacci(5)
	fmt.Println("Result:", result)

	// Test return value capture
	c := &Calculator{}
	sum := c.Add(10, 20)
	fmt.Println("Sum:", sum)

	// Test panic recovery
	func() {
		defer func() { recover() }()
		riskyOperation()
	}()

	// Export and summary
	trace.ExportPerfetto("trace.pftrace")
	trace.PrintSummary()
}

func fibonacci(n int) int {
	defer trace.Trace("fibonacci", n)(n) // Return value captured here

	if n <= 1 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}

type Calculator struct{}

func (c *Calculator) Add(a, b int) int {
	result := a + b
	defer trace.Trace("Calculator.Add", a, b)(result)

	return result
}

func riskyOperation() {
	defer trace.Trace("riskyOperation")()
	panic("something went wrong!")
}
