// Package addressbook implements the v0.3 federation address book —
// the persistent record of peer daimons this daimon has talked to
// (or pinned for trust). Per design/v0.3-federation.md Decision 5,
// the address book holds the TOFU + pinning state that gates
// peer.* RPC verbs.
//
// Lives on disk as $DAIMON_HOME/address_book.enc, encrypted under
// an identity-derived HKDF subkey (label "daimon-address-book-key-v1")
// — same pattern memory-row encryption uses. Unreadable without
// the unlocked identity.
package addressbook

import "time"

// Status is the trust posture for a peer DID.
type Status int

const (
	// FirstSeen: the daimon has dialed this peer but the user
	// hasn't explicitly pinned it. peer.* verbs from this DID
	// are NOT authorized unless they've been explicitly approved
	// in Entry.ApprovedVerbs (per-verb authorization on top of
	// the pin status).
	FirstSeen Status = iota

	// Pinned: the user has explicitly trusted this peer DID. The
	// (DID, transport pubkey) tuple is pinned — subsequent
	// connections that produce a different pubkey trip a TOFU
	// violation (which calling code surfaces as an error).
	Pinned

	// Blocked: the user has explicitly refused to talk to this
	// peer. Dial attempts fail immediately, no handshake.
	Blocked
)

// String returns the human-friendly name for the status, used in
// table-rendered CLI output + the JSON serialisation. Stable
// across versions — changing these strings would break wire-format
// compatibility for clients consuming the JSON shape.
func (s Status) String() string {
	switch s {
	case FirstSeen:
		return "first_seen"
	case Pinned:
		return "pinned"
	case Blocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// Entry is one peer's record in the address book. Persisted on
// disk inside the encrypted JSON envelope; surfaced to RPC callers
// as-is (no transformation between on-disk and on-wire shapes —
// the JSON tags below ARE the wire contract).
type Entry struct {
	// DID is the peer's DID. Address book lookups are keyed on
	// this — one entry per DID. The DID format is opaque to the
	// address book (could be did:key, did:web, did:ion, …); the
	// resolver picks the right method at dial time.
	DID string `json:"did"`

	// PetName is an optional user-supplied label for this peer.
	// Pure UX — the daimon never sends it on the wire. Useful
	// for the user-facing "I added alice@example.com last
	// Tuesday" recall.
	PetName string `json:"pet_name,omitempty"`

	// Status is the trust posture (first_seen / pinned / blocked).
	Status Status `json:"status"`

	// ApprovedVerbs is the per-verb authorization list. Even a
	// pinned peer can only invoke verbs in this set. Per design
	// Decision 5 ("explicit per-verb authorization"); blocking
	// peer.echo while allowing peer.ask is a meaningful posture.
	// Empty for FirstSeen entries.
	ApprovedVerbs []string `json:"approved_verbs,omitempty"`

	// TransportPubKeyMultibase is the X25519 transport pubkey
	// recorded at TOFU time. Subsequent handshakes that produce
	// a different pubkey trip a TOFU violation. Empty until the
	// first successful handshake with this peer.
	TransportPubKeyMultibase string `json:"transport_pubkey_multibase,omitempty"`

	// FirstSeen is when the address book first recorded this DID.
	// Doesn't get updated; "first seen" is a permanent fact about
	// the relationship.
	FirstSeen time.Time `json:"first_seen"`

	// LastSeen is the most recent timestamp the daimon dialed or
	// was dialed by this peer. Updated on every successful
	// handshake. Useful for staleness signals ("this peer hasn't
	// been seen in 6 months — maybe their endpoint moved").
	LastSeen time.Time `json:"last_seen"`
}

// HasVerb reports whether the peer is authorized for the given
// peer.* verb. Returns false for blocked peers, true for any
// non-blocked peer whose ApprovedVerbs slice contains the verb.
//
// First-seen peers with no explicit approvals get false — the
// trust ceremony requires explicit per-verb pinning, not
// silent first-use-grants-everything.
func (e *Entry) HasVerb(verb string) bool {
	if e == nil || e.Status == Blocked {
		return false
	}
	for _, v := range e.ApprovedVerbs {
		if v == verb {
			return true
		}
	}
	return false
}
