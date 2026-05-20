package addressbook

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/secretbox"
)

// --- helpers ---------------------------------------------------------------

// freshBook constructs an empty Book on a tempdir path with a
// random 32-byte encryption key. Returns the book + the path so a
// test that needs to reopen with the same key can do so.
func freshBook(t *testing.T) (*Book, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "address_book.enc")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	b, err := Open(path, key)
	if err != nil {
		t.Fatalf("Open(fresh): %v", err)
	}
	return b, path, key
}

// --- Open / Save round-trip -------------------------------------------------

func TestOpen_NonexistentPathReturnsEmptyBook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "address_book.enc")
	key := make([]byte, 32)
	rand.Read(key)
	b, err := Open(path, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if b.Len() != 0 {
		t.Errorf("fresh book Len() = %d, want 0", b.Len())
	}
}

func TestSave_RoundTrip(t *testing.T) {
	b, path, key := freshBook(t)
	_ = b.Add("did:web:alice.example.com", "Alice", "z6MkAlice")
	_ = b.Add("did:web:bob.example.com", "Bob", "z6MkBob")
	_ = b.Pin("did:web:alice.example.com", []string{"peer.echo", "peer.ask"})
	if err := b.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Re-open with the same key. Should see both entries +
	// Alice's pin state.
	b2, err := Open(path, key)
	if err != nil {
		t.Fatalf("Open(re): %v", err)
	}
	if b2.Len() != 2 {
		t.Errorf("after reopen: Len() = %d, want 2", b2.Len())
	}
	alice, ok := b2.Lookup("did:web:alice.example.com")
	if !ok {
		t.Fatal("alice not found after reopen")
	}
	if alice.Status != Pinned {
		t.Errorf("alice.Status = %v, want Pinned", alice.Status)
	}
	if len(alice.ApprovedVerbs) != 2 {
		t.Errorf("alice.ApprovedVerbs = %v, want 2 entries", alice.ApprovedVerbs)
	}
	if alice.PetName != "Alice" {
		t.Errorf("alice.PetName = %q, want Alice", alice.PetName)
	}

	bob, ok := b2.Lookup("did:web:bob.example.com")
	if !ok {
		t.Fatal("bob not found after reopen")
	}
	if bob.Status != FirstSeen {
		t.Errorf("bob.Status = %v, want FirstSeen (never pinned)", bob.Status)
	}
}

func TestOpen_WrongKeyFails(t *testing.T) {
	b, path, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	_ = b.Save()

	// Different key MUST fail to decrypt — that's the whole point
	// of at-rest encryption.
	wrong := make([]byte, 32)
	rand.Read(wrong)
	if _, err := Open(path, wrong); !errors.Is(err, ErrCorruptedFile) {
		t.Errorf("Open with wrong key: got %v, want ErrCorruptedFile", err)
	}
}

func TestOpen_RejectsWrongKeySize(t *testing.T) {
	for _, sz := range []int{0, 16, 31, 33, 64} {
		t.Run("size_"+string(rune('0'+sz%10)), func(t *testing.T) {
			_, err := Open("/tmp/whatever.enc", make([]byte, sz))
			if !errors.Is(err, ErrInvalidKey) {
				t.Errorf("size %d: got %v, want ErrInvalidKey", sz, err)
			}
		})
	}
}

// --- CRUD ------------------------------------------------------------------

func TestAdd_RejectsEmptyDID(t *testing.T) {
	b, _, _ := freshBook(t)
	if err := b.Add("", "pet", ""); !errors.Is(err, ErrEmptyDID) {
		t.Errorf("Add empty DID: got %v, want ErrEmptyDID", err)
	}
}

func TestAdd_RejectsDuplicate(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "x", "z6MkX")
	if err := b.Add("did:web:x.example.com", "y", "z6MkY"); !errors.Is(err, ErrEntryExists) {
		t.Errorf("Add duplicate: got %v, want ErrEntryExists", err)
	}
}

func TestPin_NotFoundCase(t *testing.T) {
	b, _, _ := freshBook(t)
	if err := b.Pin("did:web:nonexistent.example.com", []string{"peer.echo"}); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("Pin missing DID: got %v, want ErrEntryNotFound", err)
	}
}

func TestPin_RefusesBlocked(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:bad.example.com", "", "")
	_ = b.Block("did:web:bad.example.com")
	if err := b.Pin("did:web:bad.example.com", []string{"peer.echo"}); !errors.Is(err, ErrBlockedEntry) {
		t.Errorf("Pin blocked entry: got %v, want ErrBlockedEntry", err)
	}
}

func TestBlock_ClearsApprovedVerbs(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	_ = b.Pin("did:web:x.example.com", []string{"peer.echo", "peer.ask"})
	_ = b.Block("did:web:x.example.com")
	e, _ := b.Lookup("did:web:x.example.com")
	if e.Status != Blocked {
		t.Errorf("Status = %v, want Blocked", e.Status)
	}
	if len(e.ApprovedVerbs) != 0 {
		t.Errorf("ApprovedVerbs after block = %v, want empty", e.ApprovedVerbs)
	}
}

func TestUnblock_GoesToFirstSeenNotPinned(t *testing.T) {
	// Asymmetry by design: unblock returns to FirstSeen, not to
	// the prior Pinned state. Re-pinning requires an explicit Pin
	// call.
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	_ = b.Pin("did:web:x.example.com", []string{"peer.echo"})
	_ = b.Block("did:web:x.example.com")
	_ = b.Unblock("did:web:x.example.com")
	e, _ := b.Lookup("did:web:x.example.com")
	if e.Status != FirstSeen {
		t.Errorf("after unblock: Status = %v, want FirstSeen", e.Status)
	}
	if len(e.ApprovedVerbs) != 0 {
		t.Errorf("after unblock: ApprovedVerbs = %v, want empty", e.ApprovedVerbs)
	}
}

func TestUnblock_IdempotentOnNonBlocked(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	if err := b.Unblock("did:web:x.example.com"); err != nil {
		t.Errorf("Unblock non-blocked: got %v, want nil (idempotent)", err)
	}
}

func TestRemove_DeletesEntry(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	if err := b.Remove("did:web:x.example.com"); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if _, ok := b.Lookup("did:web:x.example.com"); ok {
		t.Error("entry survived Remove")
	}
	if b.Len() != 0 {
		t.Errorf("Len() after Remove = %d, want 0", b.Len())
	}
}

func TestRemove_NotFound(t *testing.T) {
	b, _, _ := freshBook(t)
	if err := b.Remove("did:web:nonexistent.example.com"); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("Remove missing: got %v, want ErrEntryNotFound", err)
	}
}

// --- Touch + TOFU ----------------------------------------------------------

func TestTouch_RecordsPubKeyOnFirstSeen(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	if err := b.Touch("did:web:x.example.com", "z6MkFirst"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	e, _ := b.Lookup("did:web:x.example.com")
	if e.TransportPubKeyMultibase != "z6MkFirst" {
		t.Errorf("TransportPubKeyMultibase = %q, want z6MkFirst", e.TransportPubKeyMultibase)
	}
}

func TestTouch_TOFUViolation(t *testing.T) {
	// The TOFU defense: if an entry already has a recorded
	// transport pubkey and a subsequent Touch produces a DIFFERENT
	// pubkey, that's a violation. Don't silently update — surface
	// it to the caller so the user can decide whether it's a
	// rotation or a compromise.
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "z6MkOriginal")
	err := b.Touch("did:web:x.example.com", "z6MkATTACKER")
	if err == nil {
		t.Error("Touch with different pubkey: expected TOFU violation error, got nil")
	}
	if !strings.Contains(err.Error(), "TOFU violation") {
		t.Errorf("Touch error: got %v, want 'TOFU violation' substring", err)
	}
	// Original pubkey unchanged.
	e, _ := b.Lookup("did:web:x.example.com")
	if e.TransportPubKeyMultibase != "z6MkOriginal" {
		t.Errorf("after rejected Touch: TransportPubKeyMultibase = %q, want z6MkOriginal", e.TransportPubKeyMultibase)
	}
}

func TestTouch_SamePubKeyOK(t *testing.T) {
	// Same pubkey on a subsequent Touch is fine — that's the
	// happy-path "we're still talking to the same peer."
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "z6MkSame")
	if err := b.Touch("did:web:x.example.com", "z6MkSame"); err != nil {
		t.Errorf("Touch same pubkey: got %v, want nil", err)
	}
}

func TestTouch_EmptyPubKeyOK(t *testing.T) {
	// Empty pubkey means "just update LastSeen, don't try to
	// record/check the pubkey." Useful for "I sent something but
	// didn't actually shake hands" paths.
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "z6MkExisting")
	if err := b.Touch("did:web:x.example.com", ""); err != nil {
		t.Errorf("Touch with empty pubkey: got %v, want nil", err)
	}
}

// --- HasVerb ---------------------------------------------------------------

func TestHasVerb(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	_ = b.Pin("did:web:x.example.com", []string{"peer.echo", "peer.ask"})
	e, _ := b.Lookup("did:web:x.example.com")

	if !e.HasVerb("peer.echo") {
		t.Error("HasVerb(peer.echo) = false, want true")
	}
	if !e.HasVerb("peer.ask") {
		t.Error("HasVerb(peer.ask) = false, want true")
	}
	if e.HasVerb("peer.pay") {
		t.Error("HasVerb(peer.pay) = true, want false (not in ApprovedVerbs)")
	}

	// Nil entry → false.
	var nilEntry *Entry
	if nilEntry.HasVerb("peer.echo") {
		t.Error("nil entry HasVerb = true, want false")
	}

	// Blocked entry → false regardless of approved verbs.
	_ = b.Block("did:web:x.example.com")
	e2, _ := b.Lookup("did:web:x.example.com")
	if e2.HasVerb("peer.echo") {
		t.Error("blocked entry HasVerb = true, want false")
	}
}

// --- List sorted -----------------------------------------------------------

func TestList_SortedByDID(t *testing.T) {
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:zebra.example.com", "", "")
	_ = b.Add("did:web:alpha.example.com", "", "")
	_ = b.Add("did:web:bravo.example.com", "", "")
	list := b.List()
	if len(list) != 3 {
		t.Fatalf("List len = %d, want 3", len(list))
	}
	if list[0].DID != "did:web:alpha.example.com" {
		t.Errorf("List[0] = %q, want alpha", list[0].DID)
	}
	if list[1].DID != "did:web:bravo.example.com" {
		t.Errorf("List[1] = %q, want bravo", list[1].DID)
	}
	if list[2].DID != "did:web:zebra.example.com" {
		t.Errorf("List[2] = %q, want zebra", list[2].DID)
	}
}

// --- Defensive-copy property ----------------------------------------------

func TestLookup_ReturnsDefensiveCopy(t *testing.T) {
	// Mutating the entry returned by Lookup MUST NOT mutate the
	// book's internal state. This is the property that lets
	// callers read-and-modify safely.
	b, _, _ := freshBook(t)
	_ = b.Add("did:web:x.example.com", "original", "")
	_ = b.Pin("did:web:x.example.com", []string{"peer.echo"})

	got, _ := b.Lookup("did:web:x.example.com")
	got.PetName = "tampered"
	got.ApprovedVerbs[0] = "peer.EVIL"

	// Re-lookup — the book should be unchanged.
	got2, _ := b.Lookup("did:web:x.example.com")
	if got2.PetName != "original" {
		t.Errorf("PetName mutated by external write: got %q, want original", got2.PetName)
	}
	if got2.ApprovedVerbs[0] != "peer.echo" {
		t.Errorf("ApprovedVerbs[0] mutated by external write: got %q, want peer.echo", got2.ApprovedVerbs[0])
	}
}

// --- Close -----------------------------------------------------------------

func TestClose_SavesAndZerosKey(t *testing.T) {
	b, path, key := freshBook(t)
	_ = b.Add("did:web:x.example.com", "", "")
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Re-opening with the original key still works (Close saved
	// before zeroing).
	b2, err := Open(path, key)
	if err != nil {
		t.Fatalf("Open after Close: %v", err)
	}
	if b2.Len() != 1 {
		t.Errorf("after reopen: Len() = %d, want 1", b2.Len())
	}
}

// --- Forward-compat refusal ------------------------------------------------

func TestOpen_RefusesFutureFormatVersion(t *testing.T) {
	// Manually construct a v2 file (FormatVersion = 2) and verify
	// v1 readers refuse to open it. Forward-compat refusal: a v1
	// reader can't safely round-trip a v2 file because it might
	// drop fields it doesn't understand.
	dir := t.TempDir()
	path := filepath.Join(dir, "future.enc")
	key := make([]byte, 32)
	rand.Read(key)

	// Build a v2-looking JSON payload, encrypt under our key, write
	// to disk. v1 reader should refuse to Open it.
	futurePlaintext := []byte(`{"format_version": 2, "entries": []}`)
	ct, err := secretbox.SealAAD(key, futurePlaintext, []byte(AEADBlobAAD))
	if err != nil {
		t.Fatalf("seal for test: %v", err)
	}
	if err := os.WriteFile(path, ct, 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := Open(path, key); err == nil {
		t.Error("Open v2 file with v1 reader: expected error, got nil")
	} else if !strings.Contains(err.Error(), "format version") {
		t.Errorf("Open future-format file: got %v, want 'format version' substring", err)
	}
}
