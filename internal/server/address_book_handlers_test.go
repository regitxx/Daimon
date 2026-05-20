package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
)

// newAddressBookFixture mirrors newFixture but ALSO opens an
// addressbook.Book and threads it through Options. Same posture as
// newWalletFixture — most server tests don't need an address book,
// so it's a separate constructor rather than baked into newFixture.
func newAddressBookFixture(t *testing.T) (*fixture, *addressbook.Book) {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	dir := t.TempDir()
	store, err := memory.Open(filepath.Join(dir, "memory.db"), id, memory.NullEmbedder{})
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	alog, err := activity.Open(filepath.Join(dir, "activity.log"), id)
	if err != nil {
		t.Fatalf("activity.Open: %v", err)
	}
	t.Cleanup(func() { _ = alog.Close() })

	// Address book: random key for handler tests (the wire surface
	// doesn't depend on the at-rest encryption posture; we just need
	// a working Book to thread through).
	abKey := make([]byte, 32)
	rand.Read(abKey)
	abPath := filepath.Join(dir, "address_book.enc")
	ab, err := addressbook.Open(abPath, abKey)
	if err != nil {
		t.Fatalf("addressbook.Open: %v", err)
	}
	t.Cleanup(func() { _ = ab.Close() })

	srv, err := New(Options{Identity: id, Store: store, Log: alog, AddressBook: ab})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	sockDir, err := os.MkdirTemp("", "dmn")
	if err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "s.sock")
	if err := srv.Listen(sockPath); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	go func() { _ = srv.Serve(ctx) }()
	waitForSocket(t, sockPath)

	return &fixture{t: t, id: id, mem: store, alog: alog, srv: srv, addr: sockPath}, ab
}

// --- list ------------------------------------------------------------------

func TestAddressBook_List_Empty(t *testing.T) {
	f, _ := newAddressBookFixture(t)
	resp := f.call(t, "daimon.peer.address_book.list", nil)
	var out addressBookListResult
	resultAs(t, resp, &out)
	if len(out.Entries) != 0 {
		t.Errorf("empty book list: got %d entries, want 0", len(out.Entries))
	}
}

func TestAddressBook_List_ReturnsAddedEntries(t *testing.T) {
	f, ab := newAddressBookFixture(t)
	_ = ab.Add("did:web:alice.example.com", "Alice", "z6MkAlice")
	_ = ab.Add("did:web:bob.example.com", "", "z6MkBob")
	_ = ab.Pin("did:web:alice.example.com", []string{"peer.echo"})

	resp := f.call(t, "daimon.peer.address_book.list", nil)
	var out addressBookListResult
	resultAs(t, resp, &out)

	if len(out.Entries) != 2 {
		t.Fatalf("list len = %d, want 2", len(out.Entries))
	}
	if out.Entries[0].DID != "did:web:alice.example.com" {
		t.Errorf("Entries[0].DID = %q, want alice", out.Entries[0].DID)
	}
	if out.Entries[0].Status != "pinned" {
		t.Errorf("Entries[0].Status = %q, want pinned", out.Entries[0].Status)
	}
	if out.Entries[1].DID != "did:web:bob.example.com" {
		t.Errorf("Entries[1].DID = %q, want bob", out.Entries[1].DID)
	}
}

// --- add -------------------------------------------------------------------

func TestAddressBook_Add_RoundTrip(t *testing.T) {
	f, ab := newAddressBookFixture(t)
	resp := f.call(t, "daimon.peer.address_book.add", map[string]any{
		"did":      "did:web:friend.example.com",
		"pet_name": "Friend",
	})
	var out addressBookEntryWire
	resultAs(t, resp, &out)
	if out.DID != "did:web:friend.example.com" {
		t.Errorf("DID = %q", out.DID)
	}
	if out.Status != "first_seen" {
		t.Errorf("Status = %q, want first_seen", out.Status)
	}
	if ab.Len() != 1 {
		t.Errorf("Book.Len() = %d, want 1", ab.Len())
	}
}

func TestAddressBook_Add_EmptyDIDRejected(t *testing.T) {
	f, _ := newAddressBookFixture(t)
	resp := f.call(t, "daimon.peer.address_book.add", map[string]any{"did": ""})
	if resp.Error == nil {
		t.Fatal("expected error for empty DID")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want CodeInvalidParams", resp.Error.Code)
	}
}

func TestAddressBook_Add_Duplicate(t *testing.T) {
	f, _ := newAddressBookFixture(t)
	_ = f.call(t, "daimon.peer.address_book.add", map[string]any{"did": "did:web:x.example.com"})
	resp := f.call(t, "daimon.peer.address_book.add", map[string]any{"did": "did:web:x.example.com"})
	if resp.Error == nil {
		t.Fatal("expected error for duplicate DID")
	}
}

func TestAddressBook_Add_WritesAuditRow(t *testing.T) {
	f, _ := newAddressBookFixture(t)
	_ = f.call(t, "daimon.peer.address_book.add", map[string]any{"did": "did:web:x.example.com"})

	entries, err := f.alog.Query(context.Background(), activity.QueryOptions{Kind: activity.KindPeerAddressBookAdded})
	if err != nil {
		t.Fatalf("alog.Query: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no peer.address_book.added audit row")
	}
	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := payload["did"]; got != "did:web:x.example.com" {
		t.Errorf("audit DID = %v, want did:web:x.example.com", got)
	}
}

// --- pin -------------------------------------------------------------------

func TestAddressBook_Pin_RoundTrip(t *testing.T) {
	f, ab := newAddressBookFixture(t)
	_ = ab.Add("did:web:x.example.com", "", "")
	resp := f.call(t, "daimon.peer.address_book.pin", map[string]any{
		"did":   "did:web:x.example.com",
		"verbs": []string{"peer.echo", "peer.ask"},
	})
	var out addressBookEntryWire
	resultAs(t, resp, &out)
	if out.Status != "pinned" {
		t.Errorf("Status = %q, want pinned", out.Status)
	}
	if len(out.ApprovedVerbs) != 2 {
		t.Errorf("ApprovedVerbs = %v", out.ApprovedVerbs)
	}
}

func TestAddressBook_Pin_NotFound(t *testing.T) {
	f, _ := newAddressBookFixture(t)
	resp := f.call(t, "daimon.peer.address_book.pin", map[string]any{
		"did":   "did:web:nonexistent.example.com",
		"verbs": []string{"peer.echo"},
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing DID")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("error code = %d, want CodeNotFound", resp.Error.Code)
	}
}

func TestAddressBook_Pin_BlockedRejected(t *testing.T) {
	f, ab := newAddressBookFixture(t)
	_ = ab.Add("did:web:x.example.com", "", "")
	_ = ab.Block("did:web:x.example.com")

	resp := f.call(t, "daimon.peer.address_book.pin", map[string]any{
		"did":   "did:web:x.example.com",
		"verbs": []string{"peer.echo"},
	})
	if resp.Error == nil {
		t.Fatal("expected error for pinning blocked entry")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want CodeInvalidParams", resp.Error.Code)
	}
}

// --- block / unblock / remove ----------------------------------------------

func TestAddressBook_BlockUnblockRemove(t *testing.T) {
	f, ab := newAddressBookFixture(t)
	_ = ab.Add("did:web:x.example.com", "", "")
	_ = ab.Pin("did:web:x.example.com", []string{"peer.echo"})

	resp := f.call(t, "daimon.peer.address_book.block", map[string]any{"did": "did:web:x.example.com"})
	var blocked addressBookEntryWire
	resultAs(t, resp, &blocked)
	if blocked.Status != "blocked" {
		t.Errorf("after block: Status = %q, want blocked", blocked.Status)
	}
	if len(blocked.ApprovedVerbs) != 0 {
		t.Errorf("after block: ApprovedVerbs = %v, want empty", blocked.ApprovedVerbs)
	}

	resp = f.call(t, "daimon.peer.address_book.unblock", map[string]any{"did": "did:web:x.example.com"})
	var unblocked addressBookEntryWire
	resultAs(t, resp, &unblocked)
	if unblocked.Status != "first_seen" {
		t.Errorf("after unblock: Status = %q, want first_seen", unblocked.Status)
	}

	resp = f.call(t, "daimon.peer.address_book.remove", map[string]any{"did": "did:web:x.example.com"})
	if resp.Error != nil {
		t.Errorf("remove: got error %v, want nil", resp.Error)
	}
	if ab.Len() != 0 {
		t.Errorf("after remove: Book.Len() = %d, want 0", ab.Len())
	}
}

func TestAddressBook_Remove_NotFound(t *testing.T) {
	f, _ := newAddressBookFixture(t)
	resp := f.call(t, "daimon.peer.address_book.remove", map[string]any{"did": "did:web:nonexistent.example.com"})
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeNotFound {
		t.Errorf("error code = %d, want CodeNotFound", resp.Error.Code)
	}
}

// --- not-loaded behaviour --------------------------------------------------

func TestAddressBook_AllVerbsRejectWhenNotLoaded(t *testing.T) {
	f := newFixture(t) // no AddressBook in Options → abook is nil
	for _, c := range []struct {
		method string
		params any
	}{
		{"daimon.peer.address_book.list", nil},
		{"daimon.peer.address_book.add", map[string]any{"did": "did:web:x.example.com"}},
		{"daimon.peer.address_book.pin", map[string]any{"did": "did:web:x.example.com", "verbs": []string{"peer.echo"}}},
		{"daimon.peer.address_book.block", map[string]any{"did": "did:web:x.example.com"}},
		{"daimon.peer.address_book.unblock", map[string]any{"did": "did:web:x.example.com"}},
		{"daimon.peer.address_book.remove", map[string]any{"did": "did:web:x.example.com"}},
	} {
		t.Run(c.method, func(t *testing.T) {
			resp := f.call(t, c.method, c.params)
			if resp.Error == nil {
				t.Fatalf("%s: expected error when abook is nil", c.method)
			}
			if resp.Error.Code != CodeInvalidRequest {
				t.Errorf("%s: error code = %d, want CodeInvalidRequest", c.method, resp.Error.Code)
			}
		})
	}
}
