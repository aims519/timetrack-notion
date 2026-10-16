package main

import (
	"context"
	"os"

	"github.com/aims519/timetrack-notion/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
