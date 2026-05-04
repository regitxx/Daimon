package main

import (
	"flag"
	"fmt"
	"os"
)

// cmdIdentity routes the `identity` subcommand surface. v0.1 ships only `get`.
func cmdIdentity(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon identity get")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "get":
		return cmdIdentityGet(rest)
	default:
		return fmt.Errorf("daimon identity: unknown subcommand %q (only 'get' in v0.1)", sub)
	}
}

// cmdIdentityGet calls daimon.identity.get against the running daemon and
// prints the DID. Does NOT auto-spawn — if the daemon isn't running, the
// principal needs to unlock first (auto-spawning here would silently start a
// locked daemon and immediately fail with CodeIdentityLocked, which is more
// confusing than the explicit hint).
func cmdIdentityGet(args []string) error {
	fs := flag.NewFlagSet("daimon identity get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var result struct {
		DID        string   `json:"did"`
		PublicKey  string   `json:"public_key"`
		DIDMethods []string `json:"did_methods"`
	}
	if err := daemonCall("daimon.identity.get", nil, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	fmt.Printf("DID:          %s\n", result.DID)
	fmt.Printf("Public key:   %s\n", result.PublicKey)
	fmt.Printf("DID methods:  %v\n", result.DIDMethods)
	return nil
}
