package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/identity"
)

// initHomeDir returns a short-prefix tempdir suitable as $DAIMON_HOME. The
// MkdirTemp("","dmn") trick keeps the path well under the AF_UNIX 104-byte
// sun_path cap on darwin (irrelevant to init itself, but keeps these tests
// uniform with the rest of cmd/daimon).
func initHomeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestRunInit_FreshHome_WritesGenesisRow asserts the SPEC §8.2 invariant: a
// freshly-init'd daimon has exactly one activity entry, and that entry's
// signature + chain Verify under the just-generated identity. This is the
// fix's core property — the chain root is the daimon's birth, not whatever
// the user happened to do first.
func TestRunInit_FreshHome_WritesGenesisRow(t *testing.T) {
	home := initHomeDir(t)

	id, err := runInit(home, []byte("test-password"), false)
	if err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if id == nil {
		t.Fatalf("runInit returned nil identity")
	}

	keystorePath := daimonhome.KeystorePath(home)
	if _, err := os.Stat(keystorePath); err != nil {
		t.Errorf("keystore should exist at %s: %v", keystorePath, err)
	}

	logPath := filepath.Join(home, "activity.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("activity.log should exist at %s: %v", logPath, err)
	}

	// Reopen under the same identity (keystore + password roundtrip).
	loaded, err := identity.LoadFromKeystore(keystorePath, []byte("test-password"))
	if err != nil {
		t.Fatalf("LoadFromKeystore: %v", err)
	}
	if loaded.DID() != id.DID() {
		t.Errorf("loaded DID %s != generated DID %s", loaded.DID(), id.DID())
	}

	alog, err := activity.Open(logPath, loaded)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	defer alog.Close()

	verified, err := alog.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified != 1 {
		t.Errorf("verified %d entries; want 1 (genesis only)", verified)
	}

	entries, err := alog.Query(context.Background(), activity.QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d; want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != activity.KindDaimonCreated {
		t.Errorf("entry kind = %q; want %q", e.Kind, activity.KindDaimonCreated)
	}
	if e.PrevHash != activity.ZeroHash() {
		t.Errorf("genesis prev_hash = %q; want ZeroHash %q", e.PrevHash, activity.ZeroHash())
	}
}

// TestRunInit_GenesisPayloadCarriesDIDAndVersion pins the payload shape from
// SPEC §8.2 so external tooling can rely on it (the demo asciicast scene 4
// renders the genesis row by name + version).
func TestRunInit_GenesisPayloadCarriesDIDAndVersion(t *testing.T) {
	home := initHomeDir(t)

	id, err := runInit(home, []byte("pw"), false)
	if err != nil {
		t.Fatalf("runInit: %v", err)
	}

	logPath := filepath.Join(home, "activity.log")
	alog, err := activity.Open(logPath, id)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	defer alog.Close()

	entries, err := alog.Query(context.Background(), activity.QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d; want 1", len(entries))
	}

	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := payload["did"]; got != id.DID() {
		t.Errorf("payload did = %v; want %s", got, id.DID())
	}
	if got := payload["version"]; got != version {
		t.Errorf("payload version = %v; want %s", got, version)
	}
}

// TestRunInit_RefusesOverwriteWithoutForce guards the existing safety net:
// reinit without --force errors and leaves the existing keystore + log alone.
func TestRunInit_RefusesOverwriteWithoutForce(t *testing.T) {
	home := initHomeDir(t)

	if _, err := runInit(home, []byte("pw"), false); err != nil {
		t.Fatalf("first runInit: %v", err)
	}

	// Capture the genesis row's hash so we can prove it survived the rejected
	// second init.
	logPath := filepath.Join(home, "activity.log")
	beforeBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read activity.log: %v", err)
	}

	_, err = runInit(home, []byte("different-pw"), false)
	if err == nil {
		t.Fatal("second runInit without --force should error")
	}
	if !strings.Contains(err.Error(), "keystore already exists") {
		t.Errorf("error message = %q; want mention of existing keystore", err.Error())
	}

	afterBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read activity.log: %v", err)
	}
	if !equalBytes(beforeBytes, afterBytes) {
		t.Error("rejected init should not mutate activity.log")
	}
}

// TestRunInit_ForceCleansActivityLogAndMemoryDB pins the --force semantic:
// since activity.log + memory.db are signed/encrypted under the discarded
// identity, --force removes them so the new chain has exactly one entry and
// the new memory store starts empty. Without this cleanup, the new identity
// would inherit an unreadable chain (verify fails at entry 0) and an
// unreadable memory store.
func TestRunInit_ForceCleansActivityLogAndMemoryDB(t *testing.T) {
	home := initHomeDir(t)

	// Round one: provision an identity, write a non-genesis activity entry,
	// touch a memory.db file (don't need real content — runInit's cleanup just
	// asserts the path is gone).
	idA, err := runInit(home, []byte("pw-a"), false)
	if err != nil {
		t.Fatalf("first runInit: %v", err)
	}
	logPath := filepath.Join(home, "activity.log")
	memDBPath := filepath.Join(home, "memory.db")
	{
		alog, err := activity.Open(logPath, idA)
		if err != nil {
			t.Fatalf("activity.Open (round A): %v", err)
		}
		if _, err := alog.Append(context.Background(), activity.KindMemoryWrite, map[string]any{
			"id":   "m-fake",
			"kind": "fact",
		}); err != nil {
			t.Fatalf("activity.Append (round A): %v", err)
		}
		_ = alog.Close()
	}
	if err := os.WriteFile(memDBPath, []byte("stale-memory-db-bytes"), 0o600); err != nil {
		t.Fatalf("write stale memory.db: %v", err)
	}

	// Round two: --force re-init under a different password.
	idB, err := runInit(home, []byte("pw-b"), true)
	if err != nil {
		t.Fatalf("second runInit (force): %v", err)
	}
	if idB.DID() == idA.DID() {
		t.Fatal("--force should produce a fresh identity; DIDs collided")
	}

	// New memory.db must be absent (the stale bytes are gone, runInit doesn't
	// recreate the memory store — that's unlock's job).
	if _, err := os.Stat(memDBPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--force should remove stale memory.db; stat err = %v", err)
	}

	// New activity.log must verify under the new identity with exactly 1 entry
	// (the new genesis), proving the stale entries were wiped.
	alogB, err := activity.Open(logPath, idB)
	if err != nil {
		t.Fatalf("activity.Open (round B): %v", err)
	}
	defer alogB.Close()
	verified, err := alogB.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify (round B): %v", err)
	}
	if verified != 1 {
		t.Errorf("verified %d entries after --force; want 1 (new genesis)", verified)
	}
}

// TestRunInit_ForceWipesWalletKeystore pins the wallet-keystore cleanup
// added alongside the recover password cross-check. Without this, a
// post-recover --force would leave wallet.keystore on disk encrypted
// under the OLD identity's password, and the next `daimon unlock` would
// silently disable wallet RPCs (wallet.Open fails non-fatally with a
// stderr log line that nothing else surfaces). Removing it on --force
// lets the next unlock re-auto-create a fresh wallet keystore under the
// new password, restoring symmetry with activity.log + memory.db.
func TestRunInit_ForceWipesWalletKeystore(t *testing.T) {
	home := initHomeDir(t)

	// Provision identity round one + plant a wallet.keystore so we can
	// assert the cleanup removed it. Real bytes aren't needed — runInit
	// just removes the path.
	if _, err := runInit(home, []byte("pw-a"), false); err != nil {
		t.Fatalf("first runInit: %v", err)
	}
	walletPath := filepath.Join(home, "wallet.keystore")
	if err := os.WriteFile(walletPath, []byte("stale-wallet-keystore-bytes"), 0o600); err != nil {
		t.Fatalf("write stale wallet.keystore: %v", err)
	}

	if _, err := runInit(home, []byte("pw-b"), true); err != nil {
		t.Fatalf("second runInit (force): %v", err)
	}

	if _, err := os.Stat(walletPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--force should remove stale wallet.keystore; stat err = %v", err)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
