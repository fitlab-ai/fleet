package main

import (
	"os"

	"github.com/fitlab-ai/fleet/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Dependencies{In: os.Stdin, Out: os.Stdout}))
}
