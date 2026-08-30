// Command billy is a self-hosted Alexa skill endpoint for Audiobookshelf.
//
// At this stage it does one thing: capture the raw bytes of every Alexa
// request so they can serve as a corpus for building signature verification.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is the whole program, parameterised on its arguments and output streams
// so it can be exercised from tests without a process boundary.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if *showVersion {
		if _, err := fmt.Fprintln(stdout, buildVersion()); err != nil {
			return 1
		}
		return 0
	}

	return 0
}
