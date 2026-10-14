package main

import (
	"errors"
	"os"

	"github.com/aims519/timetrack-notion/internal/cli"
	"github.com/aims519/timetrack-notion/internal/clierr"
)

func main() {
	err := cli.NewRootCmd().Execute()
	if err != nil {
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	var cliError *clierr.Error
	if errors.As(err, &cliError) {
		return cliError.Code.ExitCode()
	}
	return clierr.Internal.ExitCode() // unknown internal error
}
