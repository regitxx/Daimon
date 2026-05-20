package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/addressbook"
)

// daimon.peer.address_book.* RPC handlers. Wraps the addressbook
// package's persistent Book primitive with the JSON-RPC envelope +
// activity-log integration.
//
// All address-book operations go through the live daemon's in-memory
// Book and persist via Save on every mutation (RPCs are interactive +
// rare, so the IO cost is fine). The same encryption posture as
// memory.db: file is unreadable without the unlocked identity's
// HKDF subkey.
//
// Per design Decision 5, the address book records:
//   - DIDs the daimon has talked to (first_seen)
//   - DIDs the user has explicitly trusted (pinned + per-verb auth)
//   - DIDs the user has explicitly refused (blocked)
// + the TOFU transport-pubkey pin recorded on first successful
// handshake (handled by addressbook.Touch in the future dial path,
// not exposed via RPC).

// addressBookNotReady is returned by every handler when s.abook is
// nil — same pattern as walletNotReady(). Surfaces a clean
// CodeInvalidRequest with the standard "X keystore not loaded"
// message so doctor + SDK consumers can branch on it consistently.
func addressBookNotReady() *RPCError {
	return newError(CodeInvalidRequest, "address book not loaded; check daemon logs for open failures")
}

// --- daimon.peer.address_book.list -----------------------------------------

type addressBookListResult struct {
	Entries []addressBookEntryWire `json:"entries"`
}

// addressBookEntryWire is the on-wire shape for an Entry. Distinct
// from addressbook.Entry because Status is rendered as its string
// form ("first_seen" / "pinned" / "blocked") for human-friendly
// SDK consumption rather than the Go enum int.
type addressBookEntryWire struct {
	DID                      string   `json:"did"`
	PetName                  string   `json:"pet_name,omitempty"`
	Status                   string   `json:"status"`
	ApprovedVerbs            []string `json:"approved_verbs,omitempty"`
	TransportPubKeyMultibase string   `json:"transport_pubkey_multibase,omitempty"`
	FirstSeen                string   `json:"first_seen"`
	LastSeen                 string   `json:"last_seen"`
}

func entryToWire(e *addressbook.Entry) addressBookEntryWire {
	return addressBookEntryWire{
		DID:                      e.DID,
		PetName:                  e.PetName,
		Status:                   e.Status.String(),
		ApprovedVerbs:            e.ApprovedVerbs,
		TransportPubKeyMultibase: e.TransportPubKeyMultibase,
		FirstSeen:                e.FirstSeen.UTC().Format("2006-01-02T15:04:05Z"),
		LastSeen:                 e.LastSeen.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) handleAddressBookList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	if s.abook == nil {
		return nil, addressBookNotReady()
	}
	out := addressBookListResult{Entries: []addressBookEntryWire{}}
	for _, e := range s.abook.List() {
		out.Entries = append(out.Entries, entryToWire(e))
	}
	return out, nil
}

// --- daimon.peer.address_book.add ------------------------------------------

type addressBookAddParams struct {
	DID                      string `json:"did"`
	PetName                  string `json:"pet_name,omitempty"`
	TransportPubKeyMultibase string `json:"transport_pubkey_multibase,omitempty"`
}

func (s *Server) handleAddressBookAdd(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.abook == nil {
		return nil, addressBookNotReady()
	}
	var p addressBookAddParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.DID == "" {
		return nil, newError(CodeInvalidParams, "did is required")
	}

	if err := s.abook.Add(p.DID, p.PetName, p.TransportPubKeyMultibase); err != nil {
		if errors.Is(err, addressbook.ErrEntryExists) {
			return nil, newError(CodeInvalidParams, "address book entry already exists", p.DID)
		}
		if errors.Is(err, addressbook.ErrEmptyDID) {
			return nil, newError(CodeInvalidParams, err.Error())
		}
		return nil, newError(CodeInternalError, "address book add", err.Error())
	}
	if err := s.abook.Save(); err != nil {
		return nil, newError(CodeInternalError, "address book save", err.Error())
	}
	s.auditAddressBook(ctx, "peer.address_book.added", p.DID, nil)

	entry, _ := s.abook.Lookup(p.DID)
	return entryToWire(entry), nil
}

// --- daimon.peer.address_book.pin ------------------------------------------

type addressBookPinParams struct {
	DID    string   `json:"did"`
	Verbs  []string `json:"verbs"`
}

func (s *Server) handleAddressBookPin(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.abook == nil {
		return nil, addressBookNotReady()
	}
	var p addressBookPinParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.DID == "" {
		return nil, newError(CodeInvalidParams, "did is required")
	}

	if err := s.abook.Pin(p.DID, p.Verbs); err != nil {
		if errors.Is(err, addressbook.ErrEntryNotFound) {
			return nil, newError(CodeNotFound, fmt.Sprintf("address book entry not found: %s", p.DID))
		}
		if errors.Is(err, addressbook.ErrBlockedEntry) {
			return nil, newError(CodeInvalidParams, "cannot pin a blocked entry (unblock first)", p.DID)
		}
		return nil, newError(CodeInternalError, "address book pin", err.Error())
	}
	if err := s.abook.Save(); err != nil {
		return nil, newError(CodeInternalError, "address book save", err.Error())
	}
	s.auditAddressBook(ctx, "peer.address_book.pinned", p.DID, map[string]any{"verbs": p.Verbs})

	entry, _ := s.abook.Lookup(p.DID)
	return entryToWire(entry), nil
}

// --- daimon.peer.address_book.block / unblock / remove ---------------------

type addressBookDIDOnlyParams struct {
	DID string `json:"did"`
}

func (s *Server) handleAddressBookBlock(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	return s.handleAddressBookStatusChange(ctx, params, "block", "peer.address_book.blocked", (*addressbook.Book).Block)
}

func (s *Server) handleAddressBookUnblock(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	return s.handleAddressBookStatusChange(ctx, params, "unblock", "peer.address_book.unblocked", (*addressbook.Book).Unblock)
}

func (s *Server) handleAddressBookRemove(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.abook == nil {
		return nil, addressBookNotReady()
	}
	var p addressBookDIDOnlyParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.DID == "" {
		return nil, newError(CodeInvalidParams, "did is required")
	}
	if err := s.abook.Remove(p.DID); err != nil {
		if errors.Is(err, addressbook.ErrEntryNotFound) {
			return nil, newError(CodeNotFound, fmt.Sprintf("address book entry not found: %s", p.DID))
		}
		return nil, newError(CodeInternalError, "address book remove", err.Error())
	}
	if err := s.abook.Save(); err != nil {
		return nil, newError(CodeInternalError, "address book save", err.Error())
	}
	s.auditAddressBook(ctx, "peer.address_book.removed", p.DID, nil)
	return struct{}{}, nil // {} — caller knows the DID they asked to remove
}

// handleAddressBookStatusChange consolidates block + unblock since
// they have identical wire-shape + audit-kind machinery. The
// operation parameter is a *addressbook.Book method handle so we
// don't have to write two near-identical handlers.
func (s *Server) handleAddressBookStatusChange(
	ctx context.Context,
	params json.RawMessage,
	verb string,
	auditKind string,
	op func(*addressbook.Book, string) error,
) (any, *RPCError) {
	if s.abook == nil {
		return nil, addressBookNotReady()
	}
	var p addressBookDIDOnlyParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.DID == "" {
		return nil, newError(CodeInvalidParams, "did is required")
	}
	if err := op(s.abook, p.DID); err != nil {
		if errors.Is(err, addressbook.ErrEntryNotFound) {
			return nil, newError(CodeNotFound, fmt.Sprintf("address book entry not found: %s", p.DID))
		}
		return nil, newError(CodeInternalError, fmt.Sprintf("address book %s", verb), err.Error())
	}
	if err := s.abook.Save(); err != nil {
		return nil, newError(CodeInternalError, "address book save", err.Error())
	}
	s.auditAddressBook(ctx, auditKind, p.DID, nil)

	entry, ok := s.abook.Lookup(p.DID)
	if !ok {
		// Shouldn't happen — we just succeeded the op. Defensive.
		return struct{}{}, nil
	}
	return entryToWire(entry), nil
}

// auditAddressBook appends an activity-log row for an address-book
// state change. Best-effort: a failed append logs a warning but
// doesn't fail the RPC (the address book itself is the source of
// truth; the activity log is the audit trail).
func (s *Server) auditAddressBook(ctx context.Context, kind, did string, extra map[string]any) {
	if s.alog == nil {
		return
	}
	payload := map[string]any{"did": did}
	for k, v := range extra {
		payload[k] = v
	}
	if _, err := s.alog.Append(ctx, activity.Kind(kind), payload); err != nil && s.logger != nil {
		s.logger.Printf("audit %s for %s failed: %v", kind, did, err)
	}
}
