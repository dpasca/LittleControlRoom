package main

import (
	"os"

	"lcroom/internal/brand"
	"lcroom/internal/cli"
)

func main() {
	os.Exit(cli.Run(brand.CLIName, os.Args[1:]))
}
