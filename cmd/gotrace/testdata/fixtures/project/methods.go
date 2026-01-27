package main

type Calculator struct{}

func (c *Calculator) Add(a, b int) int {
	return a + b
}
