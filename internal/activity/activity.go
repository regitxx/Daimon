// Package activity implements the Daimon activity log primitive.
//
// The activity log is an append-only, hash-chained, signed record of every
// meaningful action the daimon has taken. It is the seed of reputation: every
// entry is independently verifiable by anyone holding the principal's public
// key, and the chain is tamper-evident as a whole.
//
// See SPEC.md §8 for the entry format and the canonical list of logged kinds.
//
// v0.1 scope of this package:
//
//   - JSON Lines storage at SPEC §10's `activity.log` path
//   - BLAKE3 hash chain (prev_hash → hash linkage)
//   - Per-entry Ed25519 signature over the raw hash bytes
//   - Append, Query, and full-chain Verify
//
// Deliberately deferred:
//
//   - Anchoring chain hashes to an external system (Bitcoin/Filecoin/etc.) —
//     SPEC §8.1 marks this as v0.4+
//   - In-memory or on-disk indexes for Query at scale — linear scan is fine
//     for the v0.1 single-user scale
package activity

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lukechampine.com/blake3"
)

// HashPrefix tags every hash with its algorithm. Future-proofs the format
// against a hash agility migration.
const HashPrefix = "blake3:"

// HashSize is the BLAKE3-256 output size in bytes.
const HashSize = 32

// ZeroHash is the prev_hash of the genesis (first) entry. It is the literal
// 32 zero bytes hex-encoded with the algorithm prefix — a sentinel that no
// real entry can ever produce.
func ZeroHash() string {
	return HashPrefix + strings.Repeat("00", HashSize)
}

// Kind enumerates the activity kinds Daimon emits in v0.1 (SPEC §8.2).
// New kinds may be added in later versions; verifiers must accept unknown
// kinds without rejecting the entry.
type Kind string

const (
	KindDaimonCreated    Kind = "daimon.created"
	KindMemoryWrite      Kind = "memory.write"
	KindMemoryExport     Kind = "memory.export"
	KindMemoryImport     Kind = "memory.import"
	KindProviderInvoke   Kind = "provider.invoke"
	KindActivityQueried  Kind = "activity.queried"
	KindActivityVerified Kind = "activity.verified"
	KindContextPreviewed Kind = "context.previewed"
	KindKeyRotated       Kind = "key.rotated"

	// v0.2 — wallet + x402 payment kinds. KindWalletCreated is logged on
	// daimon.wallet.create; the payment.* kinds will land in phase 40.3
	// when the x402 client ships. Verifiers must continue to accept unknown
	// kinds (see package doc).
	KindWalletCreated  Kind = "wallet.created"
	KindPaymentSigned  Kind = "payment.signed"
	KindPaymentSettled Kind = "payment.settled"
	KindPaymentFailed  Kind = "payment.failed"

	// v0.3 — federation address-book kinds. Logged on every peer
	// trust-state change so the audit chain carries the full pin/unpin
	// history alongside memory + payment activity. Verifiers must
	// continue to accept unknown kinds (see package doc).
	KindPeerAddressBookAdded     Kind = "peer.address_book.added"
	KindPeerAddressBookPinned    Kind = "peer.address_book.pinned"
	KindPeerAddressBookBlocked   Kind = "peer.address_book.blocked"
	KindPeerAddressBookUnblocked Kind = "peer.address_book.unblocked"
	KindPeerAddressBookRemoved   Kind = "peer.address_book.removed"

	// v0.3 — federation channel + invocation kinds. Logged on peer dial,
	// close, and every cross-daimon RPC in both directions. The sending
	// daimon writes KindPeerInvokeSent; the receiving daimon independently
	// writes KindPeerInvokeReceived with its own identity context — two
	// separate audit chains, both capturing the same cross-daimon event.
	KindPeerChannelOpened   Kind = "peer.channel.opened"
	KindPeerChannelClosed   Kind = "peer.channel.closed"
	KindPeerInvokeSent      Kind = "peer.invoke.sent"
	KindPeerInvokeReceived  Kind = "peer.invoke.received"
	// KindPeerInvokeServed is written by the SERVING daimon when it
	// successfully handles an inbound peer.ask call — the "I consumed
	// MY provider's credits on behalf of a peer" audit row. Distinct
	// from KindPeerInvokeReceived (which covers ALL inbound peer.*
	// verbs including peer.echo) because only peer.ask has economic
	// implications worth a dedicated row.
	KindPeerInvokeServed Kind = "peer.invoke.served"

	// v0.3 phase 35 — federation payment discovery kinds.

	// KindPeerPaymentInvoiced is written by the SERVING daimon when it
	// responds to a peer.pay.required call — the "I told a peer what they
	// must pay to use peer.ask" audit row. Tracks price-discovery traffic
	// separately from actual payment settlement so operators can see how
	// many peers are checking rates without yet paying.
	KindPeerPaymentInvoiced Kind = "peer.payment.invoiced"

	// KindPeerPaymentReceived is reserved for phase 40.4 when the serving
	// daimon verifies an incoming x402 payment header before honouring a
	// peer.ask call. Not written in v0.3 because on-chain settlement
	// verification is deferred until the x402 facilitator client lands.
	// Verifiers must accept this kind without rejecting the chain.
	KindPeerPaymentReceived Kind = "peer.payment.received"

	// KindPeerListenStarted is written when the daemon successfully starts
	// its Noise IK TCP listener via daimon.peer.listen. Records the bound
	// endpoint so operators can audit when federation listening was activated
	// and on which address/port.
	KindPeerListenStarted Kind = "peer.listen.started"

	// v0.4 capability delegation kinds (SPEC §17 forthcoming).

	// KindCapabilityIssued is written by the LOCAL daimon when it issues a
	// root Biscuit token via daimon.capability.issue.
	KindCapabilityIssued Kind = "capability.issued"

	// KindCapabilityRevoked is written by the LOCAL daimon when it revokes
	// a previously issued token via daimon.capability.revoke.
	KindCapabilityRevoked Kind = "capability.revoked"

	// KindCapabilityVerified is written by the SERVING daimon when an
	// inbound peer verb carries a valid Biscuit token (phase 43+).
	KindCapabilityVerified Kind = "capability.verified"

	// KindCapabilityDenied is written by the SERVING daimon when an
	// inbound capability token fails verification (phase 43+).
	KindCapabilityDenied Kind = "capability.denied"
)

// Common errors.
var (
	ErrEmptyKind       = errors.New("activity: empty kind")
	ErrLogClosed       = errors.New("activity: log closed")
	ErrCorruptLog      = errors.New("activity: corrupt log line")
	ErrChainBroken     = errors.New("activity: chain broken")
	ErrHashMismatch    = errors.New("activity: hash mismatch")
	ErrSignatureFailed = errors.New("activity: signature verification failed")
)

// Entry is a single line in the activity log.
//
// The on-the-wire (and on-disk) form is JSON; one entry per line in the
// activity.log file. Hash and Signature are populated by Append; tools that
// construct an Entry for verification should leave them empty when computing
// canonical bytes.
type Entry struct {
	ID        string          `json:"id"`   // ULID
	Timestamp int64           `json:"ts"`   // unix milliseconds
	Kind      Kind            `json:"kind"` // SPEC §8.2
	Payload   json.RawMessage `json:"payload,omitempty"`
	PrevHash  string          `json:"prev_hash"`           // "blake3:..." of previous entry, or ZeroHash()
	Hash      string          `json:"hash,omitempty"`      // "blake3:..." of canonicalBytes()
	Signature []byte          `json:"signature,omitempty"` // Ed25519 over the raw hash bytes
}

// canonicalBytes returns the JSON encoding used as input to BLAKE3. It is the
// same struct marshaled with Hash and Signature cleared so the hash output
// commits to id, ts, kind, payload, and prev_hash but not to itself.
//
// Note (v0.1 limitation): this is "Go's encoding/json applied to this struct
// shape". Stable enough for Go-to-Go interop; cross-language SDK interop will
// require RFC 8785 JCS or a pre-hashed canonical form. Tracked.
func (e *Entry) canonicalBytes() ([]byte, error) {
	cp := *e
	cp.Hash = ""
	cp.Signature = nil
	return json.Marshal(&cp)
}

// computeHash returns the entry's BLAKE3 hash both as the prefixed string used
// for storage and as the raw 32-byte digest used for signing.
func (e *Entry) computeHash() (string, []byte, error) {
	b, err := e.canonicalBytes()
	if err != nil {
		return "", nil, fmt.Errorf("canonical bytes: %w", err)
	}
	h := blake3.Sum256(b)
	out := make([]byte, HashSize)
	copy(out, h[:])
	return HashPrefix + hex.EncodeToString(out), out, nil
}
