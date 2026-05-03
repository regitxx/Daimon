// Package main is the entry point for daimond, the Daimon Protocol reference daemon.
//
// daimond is the long-running local process that holds a principal's identity,
// memory, and activity log, and routes LLM provider calls. See SPEC.md for the
// protocol design.
package main

import (
	"fmt"
	"os"

	"github.com/regitxx/Daimon/internal/identity"
)

const version = "v0.1.0-dev"

func main() {
	fmt.Fprintf(os.Stderr, "daimond %s — Day Zero\n", version)
	fmt.Fprintln(os.Stderr, "Daimon Protocol reference implementation")
	fmt.Fprintln(os.Stderr, "https://github.com/regitxx/Daimon")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Demonstrating the identity primitive.")
	fmt.Fprintln(os.Stderr, "Generating an ephemeral Ed25519 keypair and did:key…")
	fmt.Fprintln(os.Stderr)

	id, err := identity.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "DID:  %s\n", id.DID())

	msg := []byte("daimon day zero")
	sig, err := id.Sign(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	verified := id.Verify(msg, sig)
	fmt.Fprintf(os.Stderr, "Sign/verify roundtrip: %v\n", verified)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "RPC server arrives in the next milestone.")
}
