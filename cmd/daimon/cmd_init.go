package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/identity"
)

// cmdInit provisions a fresh keystore at $DAIMON_HOME/identity.keystore.
// Generates a new Ed25519 identity, prompts for a password (twice for
// confirmation), encrypts the private key under the password-derived AES-GCM
// key (Argon2id, SPEC §4.2), and writes the keystore at mode 0600.
//
// Refuses to overwrite an existing keystore unless --force is passed.
// init does NOT spawn daimond; the daemon comes up on the next `daimon
// unlock`. Keeping init purely about provisioning means a user can rsync
// the daimon home dir between machines without accidentally starting two
// daemons.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("daimon init", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing keystore (DANGEROUS — discards the current identity)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}
	keystorePath := daimonhome.KeystorePath(home)

	if _, err := os.Stat(keystorePath); err == nil {
		if !*force {
			return fmt.Errorf("keystore already exists at %s — pass --force to overwrite (DESTROYS the current identity)", keystorePath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat keystore: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Provisioning new daimon identity in %s\n", home)
	fmt.Fprintln(os.Stderr, "The keystore will be encrypted under a password you supply.")
	fmt.Fprintln(os.Stderr, "There is no recovery — losing the password loses the identity.")
	fmt.Fprintln(os.Stderr)

	pw1, err := readPassword("Choose a password: ")
	if err != nil {
		return err
	}
	if len(pw1) == 0 {
		return errors.New("password must not be empty")
	}
	pw2, err := readPassword("Confirm password:  ")
	if err != nil {
		return err
	}
	if string(pw1) != string(pw2) {
		zero(pw1)
		zero(pw2)
		return errors.New("passwords did not match")
	}
	zero(pw2)

	id, err := identity.Generate()
	if err != nil {
		zero(pw1)
		return fmt.Errorf("generate identity: %w", err)
	}
	if err := id.SaveToKeystore(keystorePath, pw1); err != nil {
		zero(pw1)
		return fmt.Errorf("save keystore: %w", err)
	}
	zero(pw1)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Identity provisioned.\n")
	fmt.Fprintf(os.Stderr, "  DID:      %s\n", id.DID())
	fmt.Fprintf(os.Stderr, "  Keystore: %s (mode 0600)\n", keystorePath)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next: run `daimon unlock` to start the daemon and load the identity.")
	return nil
}

// zero best-effort-clears a password byte slice. Go's GC may have copied the
// underlying memory in any number of places (the runtime, the stack, the
// terminal driver) — this is hygiene, not security. The real defense is the
// SPEC §9.2 trust boundary: if the daemon process is compromised the unlocked
// key is already exposed, and that's by design.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
