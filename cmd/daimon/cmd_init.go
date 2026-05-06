package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/identity"
)

// cmdInit provisions a fresh keystore at $DAIMON_HOME/identity.keystore and
// writes the SPEC §8.2 daimon.created genesis row to the activity log. The
// genesis row makes the chain root a meaningful action — the daimon's birth —
// rather than whatever the user happened to do first.
//
// Refuses to overwrite an existing keystore unless --force is passed. With
// --force, the prior activity.log and memory.db are also removed: both are
// signed/encrypted under the discarded identity and unreadable by the new one,
// so leaving them on disk would only produce a chain Verify failure on the
// first audit. init does NOT spawn daimond; the daemon comes up on the next
// `daimon unlock`. Keeping init purely about provisioning means a user can
// rsync the daimon home dir between machines without accidentally starting
// two daemons.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("daimon init", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing keystore (DANGEROUS — discards the current identity, activity log, and memory store)")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
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

	id, err := runInit(home, pw1, *force)
	zero(pw1)
	if err != nil {
		return err
	}

	keystorePath := daimonhome.KeystorePath(home)
	logPath := filepath.Join(home, "activity.log")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Identity provisioned.\n")
	fmt.Fprintf(os.Stderr, "  DID:      %s\n", id.DID())
	fmt.Fprintf(os.Stderr, "  Keystore: %s (mode 0600)\n", keystorePath)
	fmt.Fprintf(os.Stderr, "  Genesis:  %s (1 entry, kind=daimon.created)\n", logPath)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next: run `daimon unlock` to start the daemon and load the identity.")
	return nil
}

// runInit is the post-password core of `daimon init`: keystore overwrite check,
// optional --force cleanup of identity-bound state, key generation, keystore
// save, and genesis activity-row write. Split from cmdInit so tests can drive
// it without TTY mocking.
//
// Genesis fires here (not on first unlock) because SPEC §8.2's "first boot"
// reads cleanly as "key generation" — init is the daimon's birth. Post-init,
// the activity log has exactly one entry. Unlock never mutates log shape; it
// just opens the log.
func runInit(home string, password []byte, force bool) (*identity.Identity, error) {
	keystorePath := daimonhome.KeystorePath(home)
	logPath := filepath.Join(home, "activity.log")
	memDBPath := filepath.Join(home, "memory.db")

	if _, err := os.Stat(keystorePath); err == nil {
		if !force {
			return nil, fmt.Errorf("keystore already exists at %s — pass --force to overwrite (DESTROYS the current identity)", keystorePath)
		}
		// --force: prior activity.log and memory.db are signed/encrypted under
		// the old identity; the new identity cannot read either. Remove them so
		// post-init the chain has exactly one entry (the new genesis).
		if err := os.Remove(logPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale activity log: %w", err)
		}
		if err := os.Remove(memDBPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale memory store: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat keystore: %w", err)
	}

	id, err := identity.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate identity: %w", err)
	}
	if err := id.SaveToKeystore(keystorePath, password); err != nil {
		return nil, fmt.Errorf("save keystore: %w", err)
	}

	alog, err := activity.Open(logPath, id)
	if err != nil {
		return id, fmt.Errorf("open activity log for genesis: %w", err)
	}
	defer alog.Close()
	if _, err := alog.Append(context.Background(), activity.KindDaimonCreated, map[string]any{
		"version": version,
		"did":     id.DID(),
	}); err != nil {
		return id, fmt.Errorf("append genesis activity row: %w", err)
	}
	return id, nil
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
