package main

import (
	"os"

	"github.com/aims519/timetrack-notion/internal/cli"
)

func main() {
	err := cli.NewRootCmd().Execute()
	if err != nil {
		os.Exit(exitCode(err))
	}
}

// TODO add other exit codes as per §8 of DESIGN.md
func exitCode(err error) int {
	return 1
}
