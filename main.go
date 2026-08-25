package main

import (
	"os"

	"github.com/smeltery/beam/cmd"
)

var version = "dev"

func main() {
	os.Exit(cmd.Run(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
