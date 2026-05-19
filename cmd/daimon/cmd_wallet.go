package main

import (
	"flag"
	"fmt"
	"os"
)

// cmdWallet routes the `wallet` subcommand surface. Every subcommand is a
// thin wrapper over a daimon.wallet.* RPC; the CLI's job is flag parsing
// and human-friendly rendering.
//
// The wallet keystore itself is auto-created by the daemon on first
// `daimon unlock` (see cmd_unlock.go for the mnemonic-surfacing flow);
// there is no `wallet init` subcommand because the keystore's existence
// is tied to the unlock lifecycle, not to a user-initiated init step.
func cmdWallet(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon wallet <list|create|address|sign> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return cmdWalletList(rest)
	case "create":
		return cmdWalletCreate(rest)
	case "address":
		return cmdWalletAddress(rest)
	case "sign":
		return cmdWalletSign(rest)
	case "show-mnemonic":
		return cmdWalletShowMnemonic(rest)
	default:
		return fmt.Errorf("daimon wallet: unknown subcommand %q", sub)
	}
}

// --- daimon wallet list ------------------------------------------------------

type walletEntry struct {
	ID        string `json:"id"`
	Chain     string `json:"chain"`
	Path      string `json:"path"`
	Address   string `json:"address"`
	PubKey    string `json:"pubkey"`
	CreatedAt int64  `json:"created_at"`
}

func cmdWalletList(args []string) error {
	fs := flag.NewFlagSet("daimon wallet list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon wallet list [--json]")
	}

	var wallets []walletEntry
	if err := daemonCall("daimon.wallet.list", nil, &wallets); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(wallets)
	}
	if len(wallets) == 0 {
		fmt.Fprintln(os.Stderr, "no wallets — create one with `daimon wallet create --chain evm:base`")
		return nil
	}
	tw := tabPrinter(os.Stdout)
	fmt.Fprintln(tw, "CHAIN\tADDRESS\tCREATED")
	for _, w := range wallets {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", w.Chain, w.Address, formatTimestamp(w.CreatedAt))
	}
	return tw.Flush()
}

// --- daimon wallet create ----------------------------------------------------

func cmdWalletCreate(args []string) error {
	fs := flag.NewFlagSet("daimon wallet create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	chain := fs.String("chain", "", "chain label, e.g. evm:base, evm:base-sepolia (required)")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *chain == "" {
		return fmt.Errorf("--chain is required (e.g. evm:base)")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon wallet create --chain <chain> [--json]")
	}

	params := map[string]any{"chain": *chain}
	var w walletEntry
	if err := daemonCall("daimon.wallet.create", params, &w); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(w)
	}
	fmt.Printf("Wallet created.\n")
	fmt.Printf("  Chain:    %s\n", w.Chain)
	fmt.Printf("  Address:  %s\n", w.Address)
	fmt.Printf("  Path:     %s\n", w.Path)
	fmt.Printf("  ID:       %s\n", w.ID)
	return nil
}

// --- daimon wallet address ---------------------------------------------------

func cmdWalletAddress(args []string) error {
	fs := flag.NewFlagSet("daimon wallet address", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	chain := fs.String("chain", "", "chain label, e.g. evm:base (required)")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *chain == "" {
		return fmt.Errorf("--chain is required (e.g. evm:base)")
	}

	params := map[string]any{"chain": *chain}
	var out struct {
		Address string `json:"address"`
	}
	if err := daemonCall("daimon.wallet.address", params, &out); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(out)
	}
	fmt.Println(out.Address)
	return nil
}

// --- daimon wallet show-mnemonic ---------------------------------------------

// cmdWalletShowMnemonic prompts for the keystore password and re-displays
// the BIP-39 mnemonic. The password is read no-echo (same readPassword
// shim as `daimon unlock`); the mnemonic is rendered in the same
// safe-backup banner the auto-create path uses on first unlock so the
// presentation is consistent.
//
// The daemon-side handler re-runs the full Argon2id + AES-GCM-decrypt
// pipeline against the on-disk keystore — it does NOT short-circuit on
// the in-memory unlocked mnemonic. A wrong password fails the decrypt
// and the CLI surfaces "wrong password" without revealing whether the
// keystore exists or what mnemonic length it contains.
func cmdWalletShowMnemonic(args []string) error {
	fs := flag.NewFlagSet("daimon wallet show-mnemonic", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the mnemonic as a JSON array (default: safe-backup banner)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon wallet show-mnemonic [--json]")
	}

	pw, err := readPassword("Password: ")
	if err != nil {
		return err
	}
	defer zero(pw)
	if len(pw) == 0 {
		return fmt.Errorf("password must not be empty")
	}

	params := map[string]any{"password": string(pw)}
	var out struct {
		Mnemonic []string `json:"mnemonic"`
	}
	if err := daemonCall("daimon.wallet.show_mnemonic", params, &out); err != nil {
		return err
	}

	if *asJSON {
		return printJSON(out)
	}

	// Banner identical in style to cmd_unlock.go's first-unlock display.
	fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")
	fmt.Fprintln(os.Stderr, "Your wallet mnemonic — keep this private.")
	fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────────────────────────")
	fmt.Fprintln(os.Stderr, "Anyone with this phrase can derive every wallet you've created and")
	fmt.Fprintln(os.Stderr, "every wallet you will ever create from this daimon. Treat it like")
	fmt.Fprintln(os.Stderr, "a password — write it down, don't paste it anywhere, never share.")
	fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────────────────────────")
	for i := 0; i < len(out.Mnemonic); i += 4 {
		end := i + 4
		if end > len(out.Mnemonic) {
			end = len(out.Mnemonic)
		}
		line := ""
		for j := i; j < end; j++ {
			line += fmt.Sprintf("  %2d. %-9s", j+1, out.Mnemonic[j])
		}
		fmt.Fprintln(os.Stderr, line)
	}
	fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")
	return nil
}

// --- daimon wallet sign ------------------------------------------------------

// cmdWalletSign exposes the low-level signing primitive for advanced/debug
// use. Most callers will use the higher-level `daimon.payment.pay` verb
// (phase 40.3) instead, which builds the EIP-3009 digest internally.
func cmdWalletSign(args []string) error {
	fs := flag.NewFlagSet("daimon wallet sign", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	chain := fs.String("chain", "", "chain label (required)")
	digestHex := fs.String("digest", "", "32-byte digest as hex string, 0x-prefix optional (required)")
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *chain == "" || *digestHex == "" {
		return fmt.Errorf("usage: daimon wallet sign --chain <chain> --digest <hex> [--json]")
	}

	params := map[string]any{"chain": *chain, "digest_hex": *digestHex}
	var out struct {
		SignatureHex string `json:"signature_hex"`
	}
	if err := daemonCall("daimon.wallet.sign", params, &out); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(out)
	}
	fmt.Println(out.SignatureHex)
	return nil
}

