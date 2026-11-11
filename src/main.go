package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"multi-wallet-cli/cli"
)

func main() {
	setup := flag.Bool("setup", false, "run first-time wallet setup")
	flag.Parse()

	if err := cli.Run(context.Background(), cli.Options{Setup: *setup}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
