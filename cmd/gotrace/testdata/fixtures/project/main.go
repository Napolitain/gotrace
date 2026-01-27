package main

import "fmt"

func main() {
	fmt.Println("hello")
	greet("world")
}

func greet(name string) string {
	return "hi " + name
}
