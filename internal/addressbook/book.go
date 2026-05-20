package addressbook

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/regitxx/Daimon/internal/secretbox"
)

// EncryptionKeyLabel is the HKDF info string for deriving the
// address book's at-rest encryption key from the identity's
// Ed25519 seed. Pinned at v1 — changing this label invalidates
// every existing address book file. Domain-separated from
// memory-row encryption ("daimon-memory-encryption-v1") and
// activity-payload encryption — different files, different keys.
const EncryptionKeyLabel = "daimon-address-book-key-v1"

// AEADBlobAAD is the AEAD additional-data binding for address book
// ciphertext. Prevents a stolen blob from being silently re-headered
// onto another file (e.g. memory.db's encrypted column) — the AAD
// mismatch makes the AEAD verification fail.
const AEADBlobAAD = "daimon-address-book-v1-blob"

// Errors returned by the Book API. Callers can errors.Is against
// these sentinels to branch cleanly.
var (
	ErrEntryNotFound  = errors.New("addressbook: entry not found")
	ErrEntryExists    = errors.New("addressbook: entry already exists")
	ErrInvalidKey     = errors.New("addressbook: encryption key must be 32 bytes")
	ErrCorruptedFile  = errors.New("addressbook: file is corrupt or under the wrong key")
	ErrEmptyDID       = errors.New("addressbook: DID must not be empty")
	ErrBlockedEntry   = errors.New("addressbook: cannot pin/grant verbs on a blocked entry")
)

// Book is the in-memory + persistent address book. Constructed via
// Open; persisted via Save (or Close, which is Save + zero key).
// Thread-safe — all methods take an internal mutex.
type Book struct {
	mu      sync.Mutex
	path    string
	key     []byte // identity-derived at-rest encryption key
	entries map[string]*Entry
}

// onDiskShape is the JSON layout actually serialised inside the
// encrypted file. Keeping it separate from the in-memory Book lets
// us evolve the on-disk format independently — e.g. v2 could add a
// "format_version" field without changing what callers see.
type onDiskShape struct {
	FormatVersion int      `json:"format_version"`
	Entries       []*Entry `json:"entries"`
}

const currentFormatVersion = 1

// Open loads the address book from the supplied path, decrypting
// under key. If the file doesn't exist, returns an empty Book
// that Save will create on first write. If the file exists but
// decryption fails, returns ErrCorruptedFile (wraps the underlying
// secretbox error).
//
// key MUST be 32 bytes — the AES-256 key for the AEAD seal. Get
// this via identity.DeriveSubkey(EncryptionKeyLabel, 32).
func Open(path string, key []byte) (*Book, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w (got %d)", ErrInvalidKey, len(key))
	}
	b := &Book{
		path:    path,
		key:     append([]byte(nil), key...), // copy: caller's key may be reused / zeroed
		entries: make(map[string]*Entry),
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil // empty book — Save will create on first write
	}
	if err != nil {
		return nil, fmt.Errorf("addressbook open: read %s: %w", path, err)
	}

	plaintext, err := secretbox.OpenAAD(b.key, data, []byte(AEADBlobAAD))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptedFile, err)
	}
	var disk onDiskShape
	if err := json.Unmarshal(plaintext, &disk); err != nil {
		return nil, fmt.Errorf("addressbook open: parse decrypted plaintext: %w", err)
	}
	// Forward-compat: refuse to open a future-version file. v1 readers
	// have no way to safely round-trip a v2 file (might drop fields
	// they don't understand).
	if disk.FormatVersion > currentFormatVersion {
		return nil, fmt.Errorf("addressbook open: format version %d not supported (this binary speaks v%d)",
			disk.FormatVersion, currentFormatVersion)
	}
	for _, e := range disk.Entries {
		if e.DID == "" {
			continue // defensive: skip junk entries rather than fail-hard
		}
		b.entries[e.DID] = e
	}
	return b, nil
}

// Save atomically writes the encrypted book to disk. Uses a sibling
// `.save-tmp` file + atomic rename so a crash mid-write leaves the
// original file intact.
//
// Note: Save grabs the mutex internally, so concurrent Save calls
// are serialized. Callers don't need to lock around Save themselves.
func (b *Book) Save() error {
	b.mu.Lock()
	entriesSnapshot := make([]*Entry, 0, len(b.entries))
	for _, e := range b.entries {
		entriesSnapshot = append(entriesSnapshot, e)
	}
	// Sort by DID for deterministic on-disk output — useful for
	// rsync-friendly backups + makes diffs against snapshots
	// meaningful.
	sort.Slice(entriesSnapshot, func(i, j int) bool {
		return entriesSnapshot[i].DID < entriesSnapshot[j].DID
	})
	path := b.path
	key := append([]byte(nil), b.key...) // copy under lock
	b.mu.Unlock()

	disk := onDiskShape{
		FormatVersion: currentFormatVersion,
		Entries:       entriesSnapshot,
	}
	plaintext, err := json.Marshal(disk)
	if err != nil {
		return fmt.Errorf("addressbook save: marshal: %w", err)
	}
	ciphertext, err := secretbox.SealAAD(key, plaintext, []byte(AEADBlobAAD))
	if err != nil {
		return fmt.Errorf("addressbook save: seal: %w", err)
	}

	tmp := path + ".save-tmp"
	if err := os.WriteFile(tmp, ciphertext, 0600); err != nil {
		return fmt.Errorf("addressbook save: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("addressbook save: commit %s → %s: %w", tmp, path, err)
	}
	return nil
}

// Close saves the book + zeros the in-memory encryption key. After
// Close, all subsequent method calls return errors. Idempotent: a
// second Close is a no-op.
func (b *Book) Close() error {
	if err := b.Save(); err != nil {
		return err
	}
	b.mu.Lock()
	for i := range b.key {
		b.key[i] = 0
	}
	b.key = nil
	b.mu.Unlock()
	return nil
}

// --- CRUD methods -----------------------------------------------------------

// Add records a first-seen entry for did. Returns ErrEntryExists if
// the DID is already in the book (use Update / Pin / Block to
// modify an existing entry).
//
// petName is optional. transportPubKey is the X25519 multibase key
// observed during the first handshake; empty string is acceptable
// for a name-only entry that hasn't been dialed yet.
func (b *Book) Add(did, petName, transportPubKey string) error {
	if did == "" {
		return ErrEmptyDID
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.entries[did]; ok {
		return fmt.Errorf("%w: %s", ErrEntryExists, did)
	}
	now := time.Now().UTC()
	b.entries[did] = &Entry{
		DID:                      did,
		PetName:                  petName,
		Status:                   FirstSeen,
		TransportPubKeyMultibase: transportPubKey,
		FirstSeen:                now,
		LastSeen:                 now,
	}
	return nil
}

// Pin upgrades an entry to Pinned and grants the specified verbs.
// Refuses to pin a blocked entry (ErrBlockedEntry) — the user has
// to Unblock first, an explicit two-step to avoid foot-shooting
// (you wouldn't want a casual `Pin` to silently undo a Block).
//
// verbs replaces ApprovedVerbs entirely — to add a verb to an
// existing list, the caller reads + appends + calls Pin with the
// merged slice. This is intentional: explicit listing makes the
// audit-log row that records the pin operation carry the complete
// set of authorized verbs, not a diff.
func (b *Book) Pin(did string, verbs []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[did]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEntryNotFound, did)
	}
	if e.Status == Blocked {
		return ErrBlockedEntry
	}
	e.Status = Pinned
	e.ApprovedVerbs = append([]string(nil), verbs...) // defensive copy
	e.LastSeen = time.Now().UTC()
	return nil
}

// Block marks an entry as Blocked. The DID's verbs are cleared
// (blocked entries have no approved verbs by definition).
// Idempotent — blocking an already-blocked entry is a no-op.
func (b *Book) Block(did string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[did]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEntryNotFound, did)
	}
	e.Status = Blocked
	e.ApprovedVerbs = nil
	return nil
}

// Unblock transitions a blocked entry back to FirstSeen. Doesn't
// restore previous approved verbs — re-pinning requires an
// explicit Pin call. The asymmetry is deliberate: undoing a block
// shouldn't silently re-grant the previous trust level.
func (b *Book) Unblock(did string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[did]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEntryNotFound, did)
	}
	if e.Status != Blocked {
		return nil // not blocked, nothing to do — idempotent
	}
	e.Status = FirstSeen
	return nil
}

// Remove deletes an entry entirely. Use for "I don't want any
// record of having talked to this DID" cases. The audit log will
// still carry the pin / first-seen rows from when the entry
// existed — Remove doesn't rewrite history, only the live
// address book.
func (b *Book) Remove(did string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.entries[did]; !ok {
		return fmt.Errorf("%w: %s", ErrEntryNotFound, did)
	}
	delete(b.entries, did)
	return nil
}

// Touch updates LastSeen + optionally records the transport pubkey
// (if currently empty). Use after a successful handshake. Returns
// ErrEntryNotFound if the DID isn't in the book.
//
// If transportPubKey is non-empty and DIFFERS from a previously
// recorded pubkey, returns an error — that's the TOFU violation
// case (the peer's static key changed unexpectedly, suggesting
// compromise or rotation). Callers MUST surface this to the user
// rather than silently updating.
func (b *Book) Touch(did, transportPubKey string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[did]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEntryNotFound, did)
	}
	if transportPubKey != "" && e.TransportPubKeyMultibase != "" && e.TransportPubKeyMultibase != transportPubKey {
		return fmt.Errorf("addressbook touch %s: TOFU violation — pubkey changed from %q to %q",
			did, e.TransportPubKeyMultibase, transportPubKey)
	}
	if transportPubKey != "" && e.TransportPubKeyMultibase == "" {
		e.TransportPubKeyMultibase = transportPubKey
	}
	e.LastSeen = time.Now().UTC()
	return nil
}

// Lookup returns the entry for did. Returns nil + ok=false if not
// in the book. The returned pointer is a snapshot copy — modifying
// it doesn't change the book's state. Use the Pin/Block/Remove
// mutators for that.
func (b *Book) Lookup(did string) (*Entry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[did]
	if !ok {
		return nil, false
	}
	// Return a defensive copy: callers can read fields but not
	// mutate the book by writing to the returned pointer's fields.
	cp := *e
	cp.ApprovedVerbs = append([]string(nil), e.ApprovedVerbs...)
	return &cp, true
}

// List returns all entries, sorted by DID. Defensive-copies each
// entry, same as Lookup.
func (b *Book) List() []*Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Entry, 0, len(b.entries))
	for _, e := range b.entries {
		cp := *e
		cp.ApprovedVerbs = append([]string(nil), e.ApprovedVerbs...)
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DID < out[j].DID })
	return out
}

// Len returns the number of entries.
func (b *Book) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// generateNonce is a small helper for tests / future random-bytes
// needs in this package. Public-facing API doesn't expose it.
func generateNonce(n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := rand.Read(out); err != nil {
		return nil, err
	}
	return out, nil
}
