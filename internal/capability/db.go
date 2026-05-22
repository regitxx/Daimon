package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const schema = `
-- Tokens this daimon has issued to other agents.
CREATE TABLE IF NOT EXISTS issued_tokens (
    token_id          TEXT    PRIMARY KEY,   -- UUID assigned at issue time
    verbs             TEXT    NOT NULL,      -- JSON array, e.g. ["peer.ask"]
    grantee_did       TEXT,                  -- optional: specific grantee DID
    target_did        TEXT,                  -- "did:key:z6Mk..." or "any"
    valid_until       INTEGER,               -- Unix seconds UTC; NULL = no expiry
    max_calls         INTEGER NOT NULL DEFAULT 0,
    model_constraint  TEXT    NOT NULL DEFAULT '',
    token_bytes       BLOB    NOT NULL,      -- serialized Biscuit token
    issued_at         INTEGER NOT NULL,      -- Unix seconds UTC
    revoked           INTEGER NOT NULL DEFAULT 0,
    revoked_at        INTEGER               -- Unix seconds UTC; NULL = not revoked
);

CREATE INDEX IF NOT EXISTS idx_issued_revoked  ON issued_tokens(revoked);
CREATE INDEX IF NOT EXISTS idx_issued_at       ON issued_tokens(issued_at);

-- Tokens this daimon has received from other agents (caller side).
CREATE TABLE IF NOT EXISTS received_tokens (
    token_id          TEXT    PRIMARY KEY,   -- UUID or hex digest of first 8 bytes
    issuer_did        TEXT    NOT NULL,
    verbs             TEXT    NOT NULL,      -- JSON array
    token_bytes       BLOB    NOT NULL,
    received_at       INTEGER NOT NULL,
    valid_until       INTEGER               -- from embedded expiry check; NULL = unknown
);

CREATE INDEX IF NOT EXISTS idx_received_at ON received_tokens(received_at);

-- Signed receipts (direction = "issued" when we served the call,
-- direction = "received" when a remote served us and returned a receipt).
CREATE TABLE IF NOT EXISTS receipts (
    receipt_id        TEXT    PRIMARY KEY,
    direction         TEXT    NOT NULL CHECK (direction IN ('issued','received')),
    served_at         INTEGER NOT NULL,
    served_verb       TEXT    NOT NULL,
    caller_did        TEXT    NOT NULL,
    server_did        TEXT    NOT NULL,
    provider          TEXT    NOT NULL DEFAULT '',
    model             TEXT    NOT NULL DEFAULT '',
    input_tokens      INTEGER NOT NULL DEFAULT 0,
    output_tokens     INTEGER NOT NULL DEFAULT 0,
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    signature         BLOB                  -- Ed25519 sig over canonical JSON; NULL = unverified
);

CREATE INDEX IF NOT EXISTS idx_receipts_direction ON receipts(direction);
CREATE INDEX IF NOT EXISTS idx_receipts_served_at ON receipts(served_at);

-- Per-token call count.  The serving daimon increments this on every
-- successful verification; the count is then injected as calls_used(n)
-- into the Biscuit Authorizer so the max_calls Datalog check is enforced.
CREATE TABLE IF NOT EXISTS token_calls (
    token_id          TEXT    PRIMARY KEY,
    calls_used        INTEGER NOT NULL DEFAULT 0,
    last_called_at    INTEGER              -- Unix seconds UTC
);
`

// ---------------------------------------------------------------------------
// DB type + constructor
// ---------------------------------------------------------------------------

// DB is the capability store.  It holds four tables in a single SQLite file
// at $DAIMON_HOME/capability.db (path supplied by the caller).
//
// Capability.db does NOT encrypt its rows: no private key material is stored
// here — token_bytes are opaque Biscuit tokens (public cryptographic objects),
// and receipt fields are informational metadata.  If field-level privacy ever
// becomes load-bearing this seam is Open().
//
// DB is safe for concurrent use from multiple goroutines via the single
// pooled connection (MaxOpenConns = 1).
type DB struct {
	db *sql.DB
}

// OpenDB opens (or creates) capability.db at the given path.
// Use ":memory:" for tests.
func OpenDB(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("capabilitydb open: %w", err)
	}
	d.SetMaxOpenConns(1)
	if _, err := d.ExecContext(context.Background(), schema); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("capabilitydb init schema: %w", err)
	}
	return &DB{db: d}, nil
}

// Close releases the underlying database handle.
func (d *DB) Close() error { return d.db.Close() }

// ---------------------------------------------------------------------------
// Issued token types + CRUD
// ---------------------------------------------------------------------------

// IssuedToken is the record of a capability token this daimon minted.
type IssuedToken struct {
	TokenID         string
	Verbs           []string
	GranteeDID      string    // optional
	TargetDID       string    // "did:key:..." or "any"
	ValidUntil      time.Time // zero = no expiry
	MaxCalls        int64
	ModelConstraint string
	TokenBytes      []byte
	IssuedAt        time.Time
	Revoked         bool
	RevokedAt       time.Time // zero = not revoked
}

// RecordIssued persists a newly issued token.
func (d *DB) RecordIssued(ctx context.Context, t IssuedToken) error {
	verbsJSON, err := json.Marshal(t.Verbs)
	if err != nil {
		return fmt.Errorf("capabilitydb: marshal verbs: %w", err)
	}
	var validUntil *int64
	if !t.ValidUntil.IsZero() {
		v := t.ValidUntil.UTC().Unix()
		validUntil = &v
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO issued_tokens
			(token_id, verbs, grantee_did, target_did, valid_until,
			 max_calls, model_constraint, token_bytes, issued_at, revoked)
		VALUES (?,?,?,?,?,?,?,?,?,0)`,
		t.TokenID, string(verbsJSON), t.GranteeDID, t.TargetDID, validUntil,
		t.MaxCalls, t.ModelConstraint, t.TokenBytes, t.IssuedAt.UTC().Unix(),
	)
	if err != nil {
		return fmt.Errorf("capabilitydb: insert issued token: %w", err)
	}
	return nil
}

// RevokeToken marks a token as revoked.  Idempotent.
func (d *DB) RevokeToken(ctx context.Context, tokenID string) error {
	now := time.Now().UTC().Unix()
	res, err := d.db.ExecContext(ctx,
		`UPDATE issued_tokens SET revoked=1, revoked_at=? WHERE token_id=? AND revoked=0`,
		now, tokenID,
	)
	if err != nil {
		return fmt.Errorf("capabilitydb: revoke token %s: %w", tokenID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either already revoked (idempotent) or not found.  Distinguish:
		var exists int
		if err := d.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM issued_tokens WHERE token_id=?`, tokenID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("capabilitydb: check token existence %s: %w", tokenID, err)
		}
		if exists == 0 {
			return fmt.Errorf("capabilitydb: token %s not found", tokenID)
		}
		// exists but already revoked — idempotent, no error
	}
	return nil
}

// IsRevoked reports whether a token_id is in the revocation list.
// Returns false for unknown token IDs (absence ≠ revoked).
func (d *DB) IsRevoked(ctx context.Context, tokenID string) (bool, error) {
	var revoked int
	err := d.db.QueryRowContext(ctx,
		`SELECT revoked FROM issued_tokens WHERE token_id=?`, tokenID,
	).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // not issued by us → not (locally) revoked
	}
	if err != nil {
		return false, fmt.Errorf("capabilitydb: is_revoked %s: %w", tokenID, err)
	}
	return revoked != 0, nil
}

// LookupIssued returns the record for a single issued token.
func (d *DB) LookupIssued(ctx context.Context, tokenID string) (*IssuedToken, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT token_id, verbs, COALESCE(grantee_did,''), COALESCE(target_did,''),
		       valid_until, max_calls, model_constraint, token_bytes,
		       issued_at, revoked, revoked_at
		FROM issued_tokens WHERE token_id=?`, tokenID,
	)
	t, err := scanIssuedToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("capabilitydb: issued token %s not found", tokenID)
	}
	return t, err
}

// ListIssued returns all issued tokens.  If includeRevoked is false, revoked
// tokens are excluded.  Results are ordered newest-first.
func (d *DB) ListIssued(ctx context.Context, includeRevoked bool) ([]IssuedToken, error) {
	q := `SELECT token_id, verbs, COALESCE(grantee_did,''), COALESCE(target_did,''),
		         valid_until, max_calls, model_constraint, token_bytes,
		         issued_at, revoked, revoked_at
		  FROM issued_tokens`
	if !includeRevoked {
		q += " WHERE revoked=0"
	}
	q += " ORDER BY issued_at DESC"

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("capabilitydb: list issued: %w", err)
	}
	defer rows.Close()
	return scanIssuedTokens(rows)
}

func scanIssuedToken(row *sql.Row) (*IssuedToken, error) {
	var (
		t          IssuedToken
		verbsJSON  string
		validUntil *int64
		issuedAt   int64
		revoked    int
		revokedAt  *int64
	)
	if err := row.Scan(
		&t.TokenID, &verbsJSON, &t.GranteeDID, &t.TargetDID,
		&validUntil, &t.MaxCalls, &t.ModelConstraint, &t.TokenBytes,
		&issuedAt, &revoked, &revokedAt,
	); err != nil {
		return nil, err
	}
	return fillIssuedToken(&t, verbsJSON, validUntil, issuedAt, revoked, revokedAt)
}

func scanIssuedTokens(rows *sql.Rows) ([]IssuedToken, error) {
	var out []IssuedToken
	for rows.Next() {
		var (
			t          IssuedToken
			verbsJSON  string
			validUntil *int64
			issuedAt   int64
			revoked    int
			revokedAt  *int64
		)
		if err := rows.Scan(
			&t.TokenID, &verbsJSON, &t.GranteeDID, &t.TargetDID,
			&validUntil, &t.MaxCalls, &t.ModelConstraint, &t.TokenBytes,
			&issuedAt, &revoked, &revokedAt,
		); err != nil {
			return nil, fmt.Errorf("capabilitydb: scan issued row: %w", err)
		}
		filled, err := fillIssuedToken(&t, verbsJSON, validUntil, issuedAt, revoked, revokedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, *filled)
	}
	return out, rows.Err()
}

func fillIssuedToken(t *IssuedToken, verbsJSON string, validUntil *int64, issuedAt int64, revoked int, revokedAt *int64) (*IssuedToken, error) {
	if err := json.Unmarshal([]byte(verbsJSON), &t.Verbs); err != nil {
		return nil, fmt.Errorf("capabilitydb: unmarshal verbs: %w", err)
	}
	if validUntil != nil {
		t.ValidUntil = time.Unix(*validUntil, 0).UTC()
	}
	t.IssuedAt = time.Unix(issuedAt, 0).UTC()
	t.Revoked = revoked != 0
	if revokedAt != nil {
		t.RevokedAt = time.Unix(*revokedAt, 0).UTC()
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// Received token types + CRUD
// ---------------------------------------------------------------------------

// ReceivedToken is a capability token received from a remote daimon.
type ReceivedToken struct {
	TokenID    string
	IssuerDID  string
	Verbs      []string
	TokenBytes []byte
	ReceivedAt time.Time
	ValidUntil time.Time // zero = not known / no expiry
}

// RecordReceived stores an inbound capability token.  Upserts on token_id.
func (d *DB) RecordReceived(ctx context.Context, t ReceivedToken) error {
	verbsJSON, err := json.Marshal(t.Verbs)
	if err != nil {
		return fmt.Errorf("capabilitydb: marshal verbs: %w", err)
	}
	var validUntil *int64
	if !t.ValidUntil.IsZero() {
		v := t.ValidUntil.UTC().Unix()
		validUntil = &v
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO received_tokens (token_id, issuer_did, verbs, token_bytes, received_at, valid_until)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(token_id) DO UPDATE SET
			issuer_did=excluded.issuer_did,
			verbs=excluded.verbs,
			token_bytes=excluded.token_bytes,
			received_at=excluded.received_at,
			valid_until=excluded.valid_until`,
		t.TokenID, t.IssuerDID, string(verbsJSON), t.TokenBytes,
		t.ReceivedAt.UTC().Unix(), validUntil,
	)
	return err
}

// ListReceived returns all stored received tokens, newest-first.
func (d *DB) ListReceived(ctx context.Context) ([]ReceivedToken, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT token_id, issuer_did, verbs, token_bytes, received_at, valid_until
		FROM received_tokens ORDER BY received_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("capabilitydb: list received: %w", err)
	}
	defer rows.Close()
	var out []ReceivedToken
	for rows.Next() {
		var (
			t          ReceivedToken
			verbsJSON  string
			receivedAt int64
			validUntil *int64
		)
		if err := rows.Scan(&t.TokenID, &t.IssuerDID, &verbsJSON, &t.TokenBytes, &receivedAt, &validUntil); err != nil {
			return nil, fmt.Errorf("capabilitydb: scan received row: %w", err)
		}
		if err := json.Unmarshal([]byte(verbsJSON), &t.Verbs); err != nil {
			return nil, fmt.Errorf("capabilitydb: unmarshal verbs: %w", err)
		}
		t.ReceivedAt = time.Unix(receivedAt, 0).UTC()
		if validUntil != nil {
			t.ValidUntil = time.Unix(*validUntil, 0).UTC()
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Receipt types + CRUD
// ---------------------------------------------------------------------------

// ReceiptDirection indicates whether this daimon issued or received the receipt.
type ReceiptDirection string

const (
	ReceiptIssued   ReceiptDirection = "issued"   // we served the call
	ReceiptReceived ReceiptDirection = "received" // remote served us
)

// Receipt is a signed proof-of-service record.  See design/v0.4-delegation.md §8.
type Receipt struct {
	ReceiptID    string
	Direction    ReceiptDirection
	ServedAt     time.Time
	ServedVerb   string
	CallerDID    string
	ServerDID    string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	DurationMS   int64
	Signature    []byte // Ed25519 sig over canonical JSON; nil = unsigned (received, unverified)
}

// RecordReceipt persists a receipt.  Upserts on receipt_id.
func (d *DB) RecordReceipt(ctx context.Context, r Receipt) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO receipts
			(receipt_id, direction, served_at, served_verb, caller_did, server_did,
			 provider, model, input_tokens, output_tokens, duration_ms, signature)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(receipt_id) DO UPDATE SET
			direction=excluded.direction,
			served_at=excluded.served_at,
			served_verb=excluded.served_verb,
			caller_did=excluded.caller_did,
			server_did=excluded.server_did,
			provider=excluded.provider,
			model=excluded.model,
			input_tokens=excluded.input_tokens,
			output_tokens=excluded.output_tokens,
			duration_ms=excluded.duration_ms,
			signature=excluded.signature`,
		r.ReceiptID, string(r.Direction), r.ServedAt.UTC().Unix(), r.ServedVerb,
		r.CallerDID, r.ServerDID, r.Provider, r.Model,
		r.InputTokens, r.OutputTokens, r.DurationMS, r.Signature,
	)
	if err != nil {
		return fmt.Errorf("capabilitydb: record receipt: %w", err)
	}
	return nil
}

// ListReceipts returns receipts, optionally filtered by direction.
// direction="" returns all. Results are newest-first.
func (d *DB) ListReceipts(ctx context.Context, direction ReceiptDirection) ([]Receipt, error) {
	q := `SELECT receipt_id, direction, served_at, served_verb, caller_did, server_did,
		         provider, model, input_tokens, output_tokens, duration_ms, signature
		  FROM receipts`
	var args []any
	if direction != "" {
		q += " WHERE direction=?"
		args = append(args, string(direction))
	}
	q += " ORDER BY served_at DESC"

	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("capabilitydb: list receipts: %w", err)
	}
	defer rows.Close()
	var out []Receipt
	for rows.Next() {
		var (
			r        Receipt
			dirStr   string
			servedAt int64
		)
		if err := rows.Scan(
			&r.ReceiptID, &dirStr, &servedAt, &r.ServedVerb, &r.CallerDID, &r.ServerDID,
			&r.Provider, &r.Model, &r.InputTokens, &r.OutputTokens, &r.DurationMS, &r.Signature,
		); err != nil {
			return nil, fmt.Errorf("capabilitydb: scan receipt row: %w", err)
		}
		r.Direction = ReceiptDirection(dirStr)
		r.ServedAt = time.Unix(servedAt, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Call-count tracking (for calls_used Datalog fact)
// ---------------------------------------------------------------------------

// IncrementCalls atomically increments the call counter for tokenID and
// returns the new total.  Creates the row with calls_used=1 if it doesn't
// exist yet.  The returned value is what should be passed to VerifyContext.CallsUsed.
func (d *DB) IncrementCalls(ctx context.Context, tokenID string) (int64, error) {
	now := time.Now().UTC().Unix()
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO token_calls (token_id, calls_used, last_called_at)
		VALUES (?, 1, ?)
		ON CONFLICT(token_id) DO UPDATE SET
			calls_used = calls_used + 1,
			last_called_at = excluded.last_called_at`,
		tokenID, now,
	)
	if err != nil {
		return 0, fmt.Errorf("capabilitydb: increment calls %s: %w", tokenID, err)
	}
	var n int64
	if err := d.db.QueryRowContext(ctx,
		`SELECT calls_used FROM token_calls WHERE token_id=?`, tokenID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("capabilitydb: read calls_used %s: %w", tokenID, err)
	}
	return n, nil
}

// CallsUsed returns the current call count for tokenID.
// Returns 0 if the token has never been verified here.
func (d *DB) CallsUsed(ctx context.Context, tokenID string) (int64, error) {
	var n int64
	err := d.db.QueryRowContext(ctx,
		`SELECT calls_used FROM token_calls WHERE token_id=?`, tokenID,
	).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("capabilitydb: calls_used %s: %w", tokenID, err)
	}
	return n, nil
}
