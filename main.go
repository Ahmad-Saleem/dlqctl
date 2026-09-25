package main

import (
	"os"

	"github.com/ahmad-saleem/dlqctl/cmd"
)

// Set by GoReleaser via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cmd.Execute(version); err != nil {
		os.Exit(1)
	}
}
