package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/wallet"
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
		return fmt.Errorf("usage: daimon wallet <list|create|address|sign|show-mnemonic|recover> [args]")
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
	case "recover":
		return cmdWalletRecover(rest)
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

// --- daimon wallet recover ---------------------------------------------------

// cmdWalletRecover is the offline counterpart to `daimon wallet
// show-mnemonic`: it writes a fresh wallet keystore at
// $DAIMON_HOME/wallet.keystore from a user-supplied BIP-39 phrase, so the
// daimon derives every wallet from that pre-existing seed instead of a
// freshly generated one. Typical use: "I already have a 24-word backup from
// MetaMask / Phantom / Rabby / my own paper backup, I want my daimon to use
// THAT seed."
//
// Hard refusal if a keystore already exists at the target path. Recovery on
// top of a populated keystore would orphan every wallet derived from the
// previous mnemonic; making the user physically move the old file first
// keeps the irreversible part of the workflow under their conscious
// control, not the CLI's.
//
// Talks to the disk directly — never the daemon socket — because the daemon,
// if running and unlocked, is already holding a different keystore in memory
// and a live seed swap is exactly the kind of half-state our crypto layer is
// not designed for. The user must restart the daemon after recovery so the
// new keystore is loaded through the normal unlock pipeline.
//
// Both the mnemonic and the password are read no-echo. The mnemonic is the
// most sensitive secret a daimon holds (the password gates one keystore;
// the mnemonic regenerates EVERY key the daimon will ever derive), so
// putting it on the command line — where it would land in shell history,
// process tables, and `ps -ef` — is not offered as an option.
func cmdWalletRecover(args []string) error {
	fs := flag.NewFlagSet("daimon wallet recover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon wallet recover")
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	walletPath := filepath.Join(home, "wallet.keystore")

	// Pre-flight: refuse before any prompting if a keystore already
	// exists. RecoverInto re-checks atomically; the early stat saves the
	// user from typing out a 24-word phrase only to learn the operation
	// was impossible from the start.
	if _, err := os.Stat(walletPath); err == nil {
		return fmt.Errorf("a wallet keystore already exists at %s\n\n"+
			"Recovery cannot proceed without overwriting it, which would orphan\n"+
			"every wallet derived from the current seed. If you really want to\n"+
			"start fresh from a different mnemonic, move the existing file out\n"+
			"of the way first (e.g. `mv %s %s.backup`) and re-run this command.",
			walletPath, walletPath, walletPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat keystore: %w", err)
	}

	// Pre-flight banner. The mnemonic the user is about to type is THE
	// most sensitive secret the daimon will ever hold — surface that
	// before they paste/type it in, not after.
	fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")
	fmt.Fprintln(os.Stderr, "Wallet recovery — import an existing BIP-39 seed.")
	fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────────────────────────")
	fmt.Fprintln(os.Stderr, "This writes a new wallet keystore at:")
	fmt.Fprintf(os.Stderr, "  %s\n", walletPath)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "The password you choose here MUST be the same as your daimon")
	fmt.Fprintln(os.Stderr, "unlock password — daimond loads the wallet keystore using that")
	fmt.Fprintln(os.Stderr, "password on every unlock. If they differ, wallet RPCs will")
	fmt.Fprintln(os.Stderr, "silently disable themselves at next unlock (you'll see a stderr")
	fmt.Fprintln(os.Stderr, "log line on daimond startup).")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Your input is hidden. Paste or type the 12- or 24-word phrase as")
	fmt.Fprintln(os.Stderr, "a single line, words separated by spaces.")
	fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")

	phrase, err := readPassword("Recovery phrase: ")
	if err != nil {
		return err
	}
	defer zero(phrase)
	if len(phrase) == 0 {
		return errors.New("recovery phrase must not be empty")
	}

	m, err := wallet.ParseMnemonic(string(phrase))
	if err != nil {
		// Don't echo back what they typed — even partial leakage of a
		// near-valid mnemonic is more information than a thief should
		// get from a CLI error message. Tell them the word count we
		// expected and that the BIP-39 checksum failed; that's enough
		// to debug a real typo.
		return fmt.Errorf("invalid mnemonic — check the word list, spelling, and order " +
			"(must be a valid 12- or 24-word BIP-39 phrase)")
	}
	// Feedback so the user knows their input was accepted, without ever
	// echoing the phrase itself.
	fmt.Fprintf(os.Stderr, "Accepted %d-word phrase (BIP-39 checksum valid).\n", len(m.Words))

	pw1, err := readPassword("Choose a password: ")
	if err != nil {
		return err
	}
	defer zero(pw1)
	if len(pw1) == 0 {
		return errors.New("password must not be empty")
	}
	pw2, err := readPassword("Confirm password:  ")
	if err != nil {
		return err
	}
	defer zero(pw2)
	if string(pw1) != string(pw2) {
		return errors.New("passwords did not match")
	}

	if err := wallet.RecoverInto(walletPath, m, pw1); err != nil {
		// ErrKeystoreExists can race in here only if the file appeared
		// between the pre-flight stat and the RecoverInto write — vanishingly
		// unlikely outside intentionally hostile concurrent use, but surface
		// it cleanly anyway.
		if errors.Is(err, wallet.ErrKeystoreExists) {
			return fmt.Errorf("a wallet keystore appeared at %s during recovery — aborted", walletPath)
		}
		return fmt.Errorf("recover: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Wallet keystore written.")
	fmt.Fprintf(os.Stderr, "  Path:  %s (mode 0600)\n", walletPath)
	fmt.Fprintf(os.Stderr, "  Seed:  %d words, encrypted under your chosen password\n", len(m.Words))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next: `daimon unlock` to bring up the daemon against this seed,")
	fmt.Fprintln(os.Stderr, "then `daimon wallet create --chain evm:base` to derive your first")
	fmt.Fprintln(os.Stderr, "wallet from it. The derived address should match what your other")
	fmt.Fprintln(os.Stderr, "wallet shows for the same seed at path m/44'/60'/0'/0/0.")
	return nil
}

