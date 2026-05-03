// Package main is the entry point for daimond, the Daimon Protocol reference daemon.
//
// daimond is the long-running local process that holds a principal's identity,
// memory, and activity log, and routes LLM provider calls. See SPEC.md for the
// protocol design.
package main

import (
	"fmt"
	"os"
)

const version = "v0.1.0-dev"

func main() {
	fmt.Fprintf(os.Stderr, "daimond %s\n", version)
	fmt.Fprintln(os.Stderr, "Daimon Protocol reference implementation")
	fmt.Fprintln(os.Stderr, "https://github.com/regitxx/Daimon")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Day Zero. No functionality implemented yet.")
	fmt.Fprintln(os.Stderr, "See SPEC.md for the protocol design.")
	os.Exit(0)
}
