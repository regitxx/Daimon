package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/wallet"
)

// cmdRotatePassword changes the at-rest encryption password for both the
// identity and (if present) wallet keystores. The Ed25519 identity key
// and BIP-39 mnemonic are unchanged — only the Argon2id-derived KEK is
// rotated — so the daimon's DID, audit chain, and every wallet address
// derived from the seed are all preserved across the rotate.
//
// Offline-only by design. A live rotate on a running daemon would
// desynchronise the in-memory unlocked key from the on-disk keystore,
// and the daemon's wstore (if any) would keep operating under the old
// password without knowing the file has changed. This command refuses
// to run while the daemon is listening on the socket.
//
// Lockstep: rotates BOTH keystores under a single new password. The
// daimon's lifecycle assumes one password covers both files (`daimon
// unlock` uses the same password for identity.LoadFromKeystore +
// wallet.Open); rotating only one would silently disable wallet RPCs
// at next unlock. The CLI surface here doesn't expose a partial-rotate
// flag — if you really want different passwords for the two files, the
// underlying `wallet.RotatePassword` / `identity.RotatePassword` package
// functions are the right tool, not this CLI.
//
// Failure handling: each keystore is re-encrypted to a sibling
// `.rotate-tmp` file, verified by re-decrypting under the new password,
// then atomically renamed into place. If the identity rename succeeds
// but the wallet rename fails, the system is in a half-rotated state:
// identity on the new password, wallet on the old. `daimon doctor`
// surfaces this as "wallet keystore not loaded" on next unlock, and the
// user can recover by re-running rotate-password (the wallet still has
// the old password) or by removing the wallet keystore + re-recovering
// from mnemonic. The error message points at this remediation.
func cmdRotatePassword(args []string) error {
	fs := flag.NewFlagSet("daimon rotate-password", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon rotate-password")
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	identityPath := daimonhome.KeystorePath(home)
	walletPath := filepath.Join(home, "wallet.keystore")

	// Pre-flight 1: identity keystore must exist. There's nothing to
	// rotate if `daimon init` hasn't run yet.
	if _, err := os.Stat(identityPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no identity keystore at %s — run `daimon init` first", identityPath)
	} else if err != nil {
		return fmt.Errorf("stat identity keystore: %w", err)
	}

	// Pre-flight 2: refuse to run while daimond is listening on the
	// socket. A live rotate would desynchronise the running daemon's
	// in-memory state from the on-disk keystore.
	socket, _, err := daimonhome.SocketPath(home)
	if err == nil {
		if conn, derr := net.DialTimeout("unix", socket, 200*time.Millisecond); derr == nil {
			_ = conn.Close()
			return fmt.Errorf("daimond is currently listening on %s — stop it before rotating "+
				"the password (`pkill daimond`, then re-run this command)", socket)
		}
	}

	walletExists := false
	if _, err := os.Stat(walletPath); err == nil {
		walletExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat wallet keystore: %w", err)
	}

	// --- Pre-flight banner ---------------------------------------------
	fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")
	fmt.Fprintln(os.Stderr, "Rotate the at-rest password for this daimon's keystores.")
	fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────────────────────────")
	fmt.Fprintf(os.Stderr, "  identity: %s\n", identityPath)
	if walletExists {
		fmt.Fprintf(os.Stderr, "  wallet:   %s\n", walletPath)
	} else {
		fmt.Fprintln(os.Stderr, "  wallet:   (none — only identity will be rotated)")
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Your DID, mnemonic, derived addresses, and audit chain are all")
	fmt.Fprintln(os.Stderr, "preserved — only the Argon2id KEK changes. The next `daimon unlock`")
	fmt.Fprintln(os.Stderr, "will use the new password.")
	fmt.Fprintln(os.Stderr, "═══════════════════════════════════════════════════════════════════")

	// --- Prompt for old + new + confirm ---------------------------------
	oldPw, err := readPassword("Current password: ")
	if err != nil {
		return err
	}
	defer zero(oldPw)
	if len(oldPw) == 0 {
		return errors.New("current password must not be empty")
	}

	newPw, err := readPassword("New password:     ")
	if err != nil {
		return err
	}
	defer zero(newPw)
	if len(newPw) == 0 {
		return errors.New("new password must not be empty")
	}
	confirm, err := readPassword("Confirm new:      ")
	if err != nil {
		return err
	}
	defer zero(confirm)
	if string(newPw) != string(confirm) {
		return errors.New("new passwords did not match")
	}
	if string(oldPw) == string(newPw) {
		return errors.New("new password is identical to the current password — nothing to do")
	}

	// --- Pre-flight 3: verify the OLD password decrypts both keystores
	// BEFORE writing anything. This catches typos in the current-password
	// prompt without leaving the system in a half-rotated state.
	if _, err := identity.LoadFromKeystore(identityPath, oldPw); err != nil {
		if errors.Is(err, identity.ErrWrongPassword) {
			return errors.New("current password does not match the identity keystore — refusing to rotate")
		}
		return fmt.Errorf("verify current password against identity keystore: %w", err)
	}
	if walletExists {
		// wallet.Open decrypts the wallet keystore; ErrWrongPassword if mismatched.
		s, _, err := wallet.Open(walletPath, oldPw)
		if err != nil {
			if errors.Is(err, wallet.ErrWrongPassword) {
				return errors.New("current password does not match the wallet keystore. " +
					"This is the silent failure mode `daimon doctor` would have flagged — " +
					"the identity and wallet keystores were encrypted under different " +
					"passwords. Rotating now would only make it worse. Restore one keystore " +
					"from backup, or use `daimon init --force` to start over (DESTROYS all state)")
			}
			return fmt.Errorf("verify current password against wallet keystore: %w", err)
		}
		_ = s.Close()
	}
	fmt.Fprintln(os.Stderr, "Current password verified against both keystores.")

	// --- Rotate identity first, then wallet -----------------------------
	if err := identity.RotatePassword(identityPath, oldPw, newPw); err != nil {
		return fmt.Errorf("rotate identity keystore: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Identity keystore rotated.")

	if walletExists {
		if err := wallet.RotatePassword(walletPath, oldPw, newPw); err != nil {
			// Half-rotated state: identity is on the new password, wallet
			// is on the old. Surface the actionable recovery explicitly.
			return fmt.Errorf("rotate wallet keystore: %w\n\n"+
				"PARTIAL ROTATION: identity keystore is now on the NEW password, but "+
				"wallet keystore is still on the OLD password. To recover:\n"+
				"  (a) re-run `daimon rotate-password` — pre-flight will succeed against "+
				"identity (new) but fail against wallet (old) and refuse, telling you to "+
				"restore one or `daimon init --force`. The rotate-password command does "+
				"NOT try to recover automatically because we can't tell which password "+
				"the user considers canonical right now.\n"+
				"  (b) re-encrypt wallet.keystore manually using internal/wallet's "+
				"RotatePassword (write a tiny Go program) if you know both passwords.\n"+
				"  (c) restore wallet.keystore from a backup encrypted under the new "+
				"password.\n"+
				"  (d) `daimon init --force` to start over (DESTROYS all state — last resort).",
				err)
		}
		fmt.Fprintln(os.Stderr, "Wallet keystore rotated.")
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Password rotated.")
	fmt.Fprintln(os.Stderr, "Next: `daimon unlock` with the new password to start the daemon.")
	return nil
}
