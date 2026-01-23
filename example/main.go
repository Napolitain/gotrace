package main

import (
	"fmt"
)

func main() {
	result := fibonacci(10)
	fmt.Println("Fibonacci(10) =", result)

	calc := &Calculator{}
	sum := calc.Add(15, 27)
	fmt.Println("15 + 27 =", sum)
}

func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}

type Calculator struct{}

func (c *Calculator) Add(a, b int) int {
	return a + b
}
