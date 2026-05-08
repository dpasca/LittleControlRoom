package main

import (
	"os"

	"lcroom/internal/lcagent"
)

func main() {
	os.Exit(lcagent.Run(os.Args[1:], os.Stdout, os.Stderr))
}
