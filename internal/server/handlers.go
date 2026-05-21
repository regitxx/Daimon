package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/provider"
)

// Method names for the SPEC §6.1 surface. Constants exist for the names the
// dispatcher special-cases (the locked-state gate, the streaming branch in
// handleConn); the rest live as string literals in registerMethods because
// they have no out-of-table consumers.
const (
	methodIdentityUnlock = "daimon.identity.unlock"
	methodProviderStream = "daimon.provider.stream"

	// streamDeltaNotification is the JSON-RPC method on the server-pushed
	// notifications emitted between request and final response. Per JSON-RPC
	// 2.0 a notification has no id field; the params carry one Delta.
	streamDeltaNotification = "daimon.provider.stream.delta"
)

// registerMethods wires the SPEC §6.1 surface to the bound primitives.
//
// daimon.identity.unlock is registered unconditionally and is the ONLY method
// the dispatcher allows pre-unlock. Demo-mode servers (constructed without an
// Unlock callback) start unlocked, so the gate never engages and unlock is a
// no-op — calling it on an unlocked server returns CodeInvalidRequest rather
// than re-deriving the key.
//
// daimon.provider.{list,invoke} are registered unconditionally. When the
// server has no Provider Registry attached, the handlers return a structured
// error explaining that no providers are configured — distinct from the
// JSON-RPC CodeMethodNotFound we'd return if the method itself were missing.
func (s *Server) registerMethods() {
	s.methods = map[string]methodHandler{
		methodIdentityUnlock:     s.handleIdentityUnlock,
		"daimon.identity.get":    s.handleIdentityGet,
		"daimon.memory.write":    s.handleMemoryWrite,
		"daimon.memory.read":     s.handleMemoryRead,
		"daimon.memory.search":   s.handleMemorySearch,
		"daimon.memory.delete":   s.handleMemoryDelete,
		"daimon.memory.export":   s.handleMemoryExport,
		"daimon.memory.import":   s.handleMemoryImport,
		"daimon.context.get":     s.handleContextGet,
		"daimon.activity.append": s.handleActivityAppend,
		"daimon.activity.query":  s.handleActivityQuery,
		"daimon.activity.verify": s.handleActivityVerify,
		"daimon.provider.list":   s.handleProviderList,
		"daimon.provider.invoke": s.handleProviderInvoke,
		"daimon.wallet.list":          s.handleWalletList,
		"daimon.wallet.create":        s.handleWalletCreate,
		"daimon.wallet.address":       s.handleWalletAddress,
		"daimon.wallet.sign":          s.handleWalletSign,
		"daimon.wallet.derive":        s.handleWalletDerive,
		"daimon.wallet.show_mnemonic": s.handleWalletShowMnemonic,
		"daimon.payment.pay":          s.handlePaymentPay,
		"daimon.federation.config": s.handleFederationConfig,
		"daimon.peer.listen":       s.handlePeerListenStart,
		"daimon.peer.address_book.list":    s.handleAddressBookList,
		"daimon.peer.address_book.add":     s.handleAddressBookAdd,
		"daimon.peer.address_book.pin":     s.handleAddressBookPin,
		"daimon.peer.address_book.block":   s.handleAddressBookBlock,
		"daimon.peer.address_book.unblock": s.handleAddressBookUnblock,
		"daimon.peer.address_book.remove":  s.handleAddressBookRemove,
		// v0.3 phase 33 — peer channel management
		"daimon.peer.dial":   s.handlePeerDial,
		"daimon.peer.close":  s.handlePeerClose,
		"daimon.peer.list":   s.handlePeerList,
		"daimon.peer.invoke": s.handlePeerInvoke,
	}
}

// --- daimon.identity.unlock --------------------------------------------------

type identityUnlockParams struct {
	Password string `json:"password"`
}

type identityUnlockResult struct {
	DID string `json:"did"`
	// Mnemonic is the freshly-generated BIP-39 24-word wallet recovery
	// phrase, returned EXACTLY ONCE on the first unlock that auto-creates
	// the wallet keystore (v0.2 §3). Subsequent unlocks omit this field.
	// The mnemonic is the only way to recover the wallet's private keys —
	// the keystore is encrypted at rest, so losing both the keystore file
	// AND the mnemonic is unrecoverable. Clients MUST surface this to the
	// principal with safe-backup framing.
	Mnemonic []string `json:"mnemonic,omitempty"`
}

// handleIdentityUnlock loads the keystore, populates the server's principal
// trio (identity, memory store, activity log), and flips the unlocked flag.
// Pre-unlock this is the only method dispatch will route; post-unlock it
// returns CodeInvalidRequest because the daemon is already unlocked and
// re-deriving the key would burn ~50ms of Argon2id for no reason.
//
// The caller (daimond serve) wires Options.Unlock to a callback that does the
// keystore.LoadFromKeystore + memory.Open + activity.Open trio. A wrong
// password surfaces as identity.ErrWrongPassword from that callback; we
// translate it to CodeIdentityLocked with the message in Data so the CLI can
// distinguish "wrong password, retry" from "daemon ate it, give up".
func (s *Server) handleIdentityUnlock(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	if s.unlockFn == nil {
		// Demo-mode server — no keystore to load. The server is already
		// unlocked from construction; calling unlock is a client error.
		return nil, newError(CodeInvalidRequest, "this daimon was constructed unlocked; identity.unlock is not applicable")
	}
	var p identityUnlockParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Password == "" {
		return nil, newError(CodeInvalidParams, "password is required")
	}

	// Serialize unlock attempts so two concurrent calls don't both load the
	// keystore. The fast path (already unlocked) takes the lock briefly to
	// observe the flag — cheap.
	s.unlockOnce.Lock()
	defer s.unlockOnce.Unlock()
	if s.unlocked.Load() {
		// Someone else won the race; return success idempotently rather than
		// erroring, since the post-condition the caller wants ("daemon is
		// unlocked") holds.
		return identityUnlockResult{DID: s.id.DID()}, nil
	}

	id, store, alog, wstore, freshMnemonic, abook, err := s.unlockFn(ctx, p.Password)
	if err != nil {
		// We do not log the password or hash thereof. The error message is
		// surfaced verbatim — typically "wrong password or corrupted
		// keystore" from internal/identity.
		return nil, newError(CodeIdentityLocked, "unlock failed", err.Error())
	}
	if id == nil || store == nil || alog == nil {
		return nil, newError(CodeInternalError, "unlock callback returned nil trio without error")
	}
	// wstore + abook MAY be nil if either keystore / address book failed
	// to load for a non-fatal reason (corrupted, future-version format,
	// etc.); the daemon stays unlocked but the affected RPC surface
	// reports "not loaded" until repaired.

	// Field writes happen-before the atomic.Store(true) below; subsequent
	// dispatch.Load() returning true is paired with these writes via
	// release/acquire semantics (Go memory model). Order matters.
	s.id = id
	s.store = store
	s.alog = alog
	s.wstore = wstore
	s.abook = abook
	s.unlocked.Store(true)

	out := identityUnlockResult{DID: id.DID()}
	if freshMnemonic != nil {
		out.Mnemonic = append([]string(nil), freshMnemonic.Words...)
	}
	return out, nil
}

// --- daimon.identity.get -----------------------------------------------------

type identityGetResult struct {
	DID        string   `json:"did"`
	PublicKey  string   `json:"public_key"` // multibase fragment, matches did:key body
	DIDMethods []string `json:"did_methods"`
}

func (s *Server) handleIdentityGet(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	did := s.id.DID()
	return identityGetResult{
		DID:        did,
		PublicKey:  identity.MultibaseFragment(did),
		DIDMethods: []string{"did:key"}, // did:ion is post-v0.1
	}, nil
}

// --- daimon.memory.write -----------------------------------------------------

type memoryWriteParams struct {
	Kind     string         `json:"kind"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Source   string         `json:"source,omitempty"`
}

type memoryWriteResult struct {
	ID string `json:"id"`
}

func (s *Server) handleMemoryWrite(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p memoryWriteParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}

	mem, err := s.store.Write(ctx, memory.WriteRequest{
		Kind:     memory.Kind(p.Kind),
		Content:  p.Content,
		Metadata: p.Metadata,
		Source:   p.Source,
	})
	if err != nil {
		return nil, mapMemoryError(err, "write")
	}

	// Audit-trail append. Per SPEC §8 the daimon — not the client — decides
	// what is "meaningful"; in mediated mode we always log writes. A failure
	// here is logged but does not fail the write itself: the memory has
	// already been committed and a client retry would create a duplicate
	// memory with no audit win. The audit gap is the lesser harm in v0.1.
	if _, err := s.alog.Append(ctx, activity.KindMemoryWrite, map[string]any{
		"id":     mem.ID,
		"kind":   string(mem.Kind),
		"source": mem.Source,
	}); err != nil {
		s.logf("activity append (memory.write id=%s): %v", mem.ID, err)
	}

	return memoryWriteResult{ID: mem.ID}, nil
}

// --- daimon.memory.read ------------------------------------------------------

type memoryReadParams struct {
	ID string `json:"id"`
}

func (s *Server) handleMemoryRead(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p memoryReadParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.ID == "" {
		return nil, newError(CodeInvalidParams, "id is required")
	}
	mem, err := s.store.Read(ctx, p.ID)
	if err != nil {
		return nil, mapMemoryError(err, "read")
	}
	return mem, nil
}

// --- daimon.memory.search ----------------------------------------------------

type memorySearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// scoredMemory is the per-row search result shape: the memory plus its score.
// Embedding stays in the response so a client can re-verify or re-rank
// locally; future versions may add an opt-out flag.
type scoredMemory struct {
	*memory.Memory
	Score float64 `json:"score"`
}

func (s *Server) handleMemorySearch(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p memorySearchParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	results, err := s.store.Search(ctx, p.Query, memory.SearchOptions{
		Kind:  memory.Kind(p.Kind),
		Limit: p.Limit,
	})
	if err != nil {
		return nil, mapMemoryError(err, "search")
	}
	out := make([]scoredMemory, len(results))
	for i, r := range results {
		out[i] = scoredMemory{Memory: r.Memory, Score: r.Score}
	}
	return out, nil
}

// --- daimon.memory.delete ----------------------------------------------------

type memoryDeleteParams struct {
	ID string `json:"id"`
}

type memoryDeleteResult struct {
	Deleted bool `json:"deleted"`
}

func (s *Server) handleMemoryDelete(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p memoryDeleteParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.ID == "" {
		return nil, newError(CodeInvalidParams, "id is required")
	}
	deleted, err := s.store.Delete(ctx, p.ID)
	if err != nil {
		return nil, mapMemoryError(err, "delete")
	}
	// memory.delete is not in SPEC §8.2's enumerated kinds in v0.1; we
	// deliberately do NOT auto-log here. Adding kinds is a spec change.
	return memoryDeleteResult{Deleted: deleted}, nil
}

// --- daimon.memory.export ----------------------------------------------------

func (s *Server) handleMemoryExport(ctx context.Context, _ json.RawMessage) (any, *RPCError) {
	doc, err := s.store.Export(ctx)
	if err != nil {
		return nil, mapMemoryError(err, "export")
	}
	if _, err := s.alog.Append(ctx, activity.KindMemoryExport, map[string]any{
		"memories": len(doc.Memories),
	}); err != nil {
		s.logf("activity append (memory.export): %v", err)
	}
	return doc, nil
}

// --- daimon.memory.import ----------------------------------------------------

type memoryImportParams struct {
	Document        *memory.ExportDocument `json:"document"`
	VerifySignature *bool                  `json:"verify_signature,omitempty"`
}

type memoryImportResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

func (s *Server) handleMemoryImport(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p memoryImportParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Document == nil {
		return nil, newError(CodeInvalidParams, "document is required")
	}
	// SPEC §6.1 default: verify_signature = true. Only skip on explicit false.
	skipVerify := false
	if p.VerifySignature != nil && !*p.VerifySignature {
		skipVerify = true
	}
	res, err := s.store.Import(ctx, p.Document, memory.ImportOptions{SkipVerification: skipVerify})
	if err != nil {
		return nil, mapMemoryError(err, "import")
	}
	if _, err := s.alog.Append(ctx, activity.KindMemoryImport, map[string]any{
		"imported": res.Imported,
		"skipped":  res.Skipped,
	}); err != nil {
		s.logf("activity append (memory.import): %v", err)
	}
	return memoryImportResult{Imported: res.Imported, Skipped: res.Skipped}, nil
}

// --- daimon.context.get ------------------------------------------------------

type contextGetParams struct {
	Query     string   `json:"query"`
	MaxTokens int      `json:"max_tokens,omitempty"`
	Kinds     []string `json:"kinds,omitempty"`
}

type contextGetResult struct {
	Context       string   `json:"context"`
	MemoryIDs     []string `json:"memory_ids"`
	TokenEstimate int      `json:"token_estimate"`
}

// handleContextGet implements the SPEC §11 retrieval policy:
//
//	score = 0.7 × cosine + 0.3 × exp(−age_days/30)
//
// The actual retrieval and formatting lives in runContextRetrieval so the
// provider.invoke handler can reuse it for the inject_context flow.
//
// Per SPEC §8.2 a successful standalone context.get appends a
// context.previewed entry to the activity log, mirroring the audit symmetry
// of activity.queried/activity.verified. The inject_context-on-invoke path
// deliberately does NOT write this row — the provider.invoke entry already
// records injected_memory_ids for the same retrieval, and a context.previewed
// alongside it would double-log a single principal action.
func (s *Server) handleContextGet(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p contextGetParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	res, rpcErr := s.runContextRetrieval(ctx, p.Query, p.MaxTokens, p.Kinds)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if _, err := s.alog.Append(ctx, activity.KindContextPreviewed, map[string]any{
		"query":   p.Query,
		"matched": len(res.MemoryIDs),
	}); err != nil {
		s.logf("activity append (context.previewed): %v", err)
	}
	return res, nil
}

// runContextRetrieval pulls candidates from memory.Search, re-ranks with the
// SPEC §11 recency boost, applies the optional kinds[] allowlist, and formats
// the top-K under the token budget into a numbered "[i] (kind) content" block.
func (s *Server) runContextRetrieval(ctx context.Context, query string, maxTokens int, kinds []string) (contextGetResult, *RPCError) {
	if maxTokens <= 0 {
		maxTokens = 2000 // SPEC §11 default
	}
	// Pull more candidates than we'll keep so the recency re-rank has room.
	hits, err := s.store.Search(ctx, query, memory.SearchOptions{Limit: 50})
	if err != nil {
		return contextGetResult{}, mapMemoryError(err, "context.get")
	}

	allow := kindAllowlist(kinds)
	now := time.Now().UnixMilli()

	type scored struct {
		m     *memory.Memory
		final float64
	}
	rescored := make([]scored, 0, len(hits))
	for _, h := range hits {
		if allow != nil && !allow[string(h.Memory.Kind)] {
			continue
		}
		rescored = append(rescored, scored{
			m:     h.Memory,
			final: 0.7*h.Score + 0.3*recencyBoost(now, h.Memory.CreatedAt),
		})
	}
	sort.SliceStable(rescored, func(i, j int) bool {
		return rescored[i].final > rescored[j].final
	})

	var (
		buf       strings.Builder
		ids       []string
		tokens    int
		formatted = make([]string, 0, len(rescored))
	)
	for i, r := range rescored {
		line := fmt.Sprintf("[%d] (%s) %s", i+1, r.m.Kind, r.m.Content)
		t := estimateTokens(line)
		if tokens+t > maxTokens && len(ids) > 0 {
			break
		}
		formatted = append(formatted, line)
		ids = append(ids, r.m.ID)
		tokens += t
	}
	for i, line := range formatted {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}

	return contextGetResult{
		Context:       buf.String(),
		MemoryIDs:     ids,
		TokenEstimate: tokens,
	}, nil
}

// --- daimon.provider.list ----------------------------------------------------

type providerListEntry struct {
	Name       string           `json:"name"`
	Models     []provider.Model `json:"models"`
	Configured bool             `json:"configured"`
}

func (s *Server) handleProviderList(_ context.Context, _ json.RawMessage) (any, *RPCError) {
	if s.providers == nil {
		return []providerListEntry{}, nil
	}
	list := s.providers.List()
	out := make([]providerListEntry, 0, len(list))
	for _, p := range list {
		configured := false
		if s.creds != nil {
			configured = s.creds.Has(p.Name())
		}
		out = append(out, providerListEntry{
			Name:       p.Name(),
			Models:     p.Models(),
			Configured: configured,
		})
	}
	return out, nil
}

// --- daimon.provider.invoke --------------------------------------------------

type providerInjectContext struct {
	Query     string   `json:"query"`
	MaxTokens int      `json:"max_tokens,omitempty"`
	Kinds     []string `json:"kinds,omitempty"`
}

type providerInvokeParams struct {
	Provider      string                 `json:"provider"`
	Request       provider.Request       `json:"request"`
	InjectContext *providerInjectContext `json:"inject_context,omitempty"`
}

// providerInvokeResult is the wire shape returned by daimon.provider.invoke
// AND daimon.provider.stream's terminal response. The provider.Response is
// nested under "response" (one extra wrapping level vs. the bare-response wire
// shape Daimon used through session 23) so the daimon can surface mediation
// metadata — the IDs of memories that were folded into the prompt via SPEC §11
// inject_context — alongside the raw provider reply. omitempty on
// InjectedMemoryIDs means a no-inject-context call serialises as
// {"response": {...}} with no metadata key, preserving the principle that the
// envelope only carries fields the daimon actually has something to say about.
type providerInvokeResult struct {
	Response          *provider.Response `json:"response"`
	InjectedMemoryIDs []string           `json:"injected_memory_ids,omitempty"`
}

func (s *Server) handleProviderInvoke(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p providerInvokeParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Provider == "" {
		return nil, newError(CodeInvalidParams, "provider is required")
	}
	if s.providers == nil {
		return nil, newError(CodeNotFound, "no provider registry attached to this daimon")
	}
	prov, err := s.providers.Get(p.Provider)
	if err != nil {
		return nil, newError(CodeNotFound, err.Error())
	}

	req := p.Request
	var injectedIDs []string
	if p.InjectContext != nil {
		// Run the SPEC §11 retrieval, then prepend the formatted block to
		// the system prompt — daimon's job is to enrich the prompt before
		// the provider sees it. Empty retrieval is silent, not an error.
		ctxResult, rpcErr := s.runContextRetrieval(ctx, p.InjectContext.Query, p.InjectContext.MaxTokens, p.InjectContext.Kinds)
		if rpcErr != nil {
			return nil, rpcErr
		}
		if ctxResult.Context != "" {
			if req.System != "" {
				req.System = ctxResult.Context + "\n\n" + req.System
			} else {
				req.System = ctxResult.Context
			}
			injectedIDs = ctxResult.MemoryIDs
		}
	}

	start := time.Now()
	resp, err := prov.Invoke(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		// Provider-level failures map to internal-error; the upstream message
		// carries the diagnostic detail. We do not classify HTTP 4xx as
		// CodeInvalidParams because the validity is the provider's call, not
		// ours.
		return nil, newError(CodeInternalError, fmt.Sprintf("provider.%s.invoke: %v", p.Provider, err))
	}

	// SPEC §8.2: every provider call is logged in mediated mode. We do not
	// log the prompt itself — that would defeat the purpose of keeping
	// memory local. We log who, what model, what counted, why it stopped.
	logPayload := map[string]any{
		"provider":      p.Provider,
		"model":         resp.Model,
		"input_tokens":  resp.Usage.InputTokens,
		"output_tokens": resp.Usage.OutputTokens,
		"stop_reason":   string(resp.StopReason),
		"duration_ms":   elapsed.Milliseconds(),
	}
	if len(injectedIDs) > 0 {
		logPayload["injected_memory_ids"] = injectedIDs
	}
	if _, err := s.alog.Append(ctx, activity.KindProviderInvoke, logPayload); err != nil {
		s.logf("activity append (provider.invoke %s): %v", p.Provider, err)
	}

	return providerInvokeResult{Response: resp, InjectedMemoryIDs: injectedIDs}, nil
}

// --- daimon.provider.stream --------------------------------------------------

// streamDeltaParams is the wire shape on each daimon.provider.stream.delta
// notification. One field, one purpose: token-by-token text. Tool-call and
// role-marker deltas live behind a future spec change (see SPEC §6.1).
type streamDeltaParams struct {
	Content string `json:"content"`
}

// handleProviderStream is the special-cased handler for daimon.provider.stream.
// Unlike the regular methodHandler signature it owns the connection's encoder
// directly so it can push N notifications followed by one final response on
// the original request id, all on the same conn — exactly what JSON-RPC 2.0
// permits for server-pushed notifications.
//
// Locking: handleConn calls this synchronously, before reading the next
// request from the same conn. The conn's writer (json.Encoder) therefore has
// exactly one user during the stream — the goroutine the consumer reads
// from — so no mutex is required to keep notification frames from
// interleaving with concurrent responses on the same conn (concurrency comes
// from many conns, each with its own handler).
//
// Returns the encoder error (if any) so handleConn knows to drop the conn.
func (s *Server) handleProviderStream(ctx context.Context, enc *json.Encoder, head Request) error {
	// Locked-state gate, mirrored from dispatch — handleConn intercepted us
	// before dispatch ran the gate.
	if !s.unlocked.Load() {
		if head.IsNotification() {
			return nil
		}
		return enc.Encode(errorResponse(head.ID, newError(CodeIdentityLocked, "identity is locked", head.Method)))
	}

	if head.IsNotification() {
		// A streaming notification (request without id) makes no sense — the
		// caller is asking for a response stream. Drop silently to honour the
		// notification-no-reply invariant.
		return nil
	}

	var p providerInvokeParams
	if rpcErr := decodeParams(head.Params, &p); rpcErr != nil {
		return enc.Encode(errorResponse(head.ID, rpcErr))
	}
	if p.Provider == "" {
		return enc.Encode(errorResponse(head.ID, newError(CodeInvalidParams, "provider is required")))
	}
	if s.providers == nil {
		return enc.Encode(errorResponse(head.ID, newError(CodeNotFound, "no provider registry attached to this daimon")))
	}
	prov, err := s.providers.Get(p.Provider)
	if err != nil {
		return enc.Encode(errorResponse(head.ID, newError(CodeNotFound, err.Error())))
	}
	streamer, ok := prov.(provider.Streamer)
	if !ok {
		// Per the locked decision: don't silently fall back. The CLI is the
		// layer that decides to retry against Invoke with a stderr note.
		return enc.Encode(errorResponse(head.ID, newError(CodeNotFound,
			"provider does not support streaming", p.Provider)))
	}

	req := p.Request
	var injectedIDs []string
	if p.InjectContext != nil {
		ctxResult, rpcErr := s.runContextRetrieval(ctx, p.InjectContext.Query, p.InjectContext.MaxTokens, p.InjectContext.Kinds)
		if rpcErr != nil {
			return enc.Encode(errorResponse(head.ID, rpcErr))
		}
		if ctxResult.Context != "" {
			if req.System != "" {
				req.System = ctxResult.Context + "\n\n" + req.System
			} else {
				req.System = ctxResult.Context
			}
			injectedIDs = ctxResult.MemoryIDs
		}
	}

	// streamCtx so we can stop the adapter's upstream call if the conn writer
	// errors mid-stream (client went away, etc.) — the goroutine below cancels
	// when it sees the encoder fail.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	deltas := make(chan provider.Delta, 16)
	type runResult struct {
		resp *provider.Response
		err  error
	}
	resultCh := make(chan runResult, 1)
	start := time.Now()
	go func() {
		resp, err := streamer.Stream(streamCtx, req, deltas)
		resultCh <- runResult{resp: resp, err: err}
	}()

	// Forward each delta as a notification. The adapter contract guarantees
	// the channel is closed before Stream returns, so this loop exits.
	// Notifications have no id field per JSON-RPC 2.0 — streamNotification
	// is a dedicated envelope so the wire bytes are right.
	for d := range deltas {
		frame := streamNotification{
			JSONRPC: JSONRPCVersion,
			Method:  streamDeltaNotification,
			Params:  streamDeltaParams{Content: d.Content},
		}
		if err := enc.Encode(frame); err != nil {
			cancel()
			// Drain the channel so the adapter goroutine's send doesn't
			// deadlock on a buffer-full channel.
			go func() {
				for range deltas {
				}
			}()
			<-resultCh
			return err
		}
	}

	res := <-resultCh
	elapsed := time.Since(start)

	if res.err != nil {
		return enc.Encode(errorResponse(head.ID, newError(CodeInternalError,
			fmt.Sprintf("provider.%s.stream: %v", p.Provider, res.err))))
	}
	if res.resp == nil {
		return enc.Encode(errorResponse(head.ID, newError(CodeInternalError,
			fmt.Sprintf("provider.%s.stream: nil response from adapter", p.Provider))))
	}

	logPayload := map[string]any{
		"provider":      p.Provider,
		"model":         res.resp.Model,
		"input_tokens":  res.resp.Usage.InputTokens,
		"output_tokens": res.resp.Usage.OutputTokens,
		"stop_reason":   string(res.resp.StopReason),
		"duration_ms":   elapsed.Milliseconds(),
		"streamed":      true,
	}
	if len(injectedIDs) > 0 {
		logPayload["injected_memory_ids"] = injectedIDs
	}
	if _, err := s.alog.Append(ctx, activity.KindProviderInvoke, logPayload); err != nil {
		s.logf("activity append (provider.stream %s): %v", p.Provider, err)
	}

	return enc.Encode(successResponse(head.ID, providerInvokeResult{Response: res.resp, InjectedMemoryIDs: injectedIDs}))
}

// streamNotification is the wire shape for a server-pushed JSON-RPC
// notification. Distinct from Response (which has an id field) — the absence
// of "id" is what makes it a notification per JSON-RPC 2.0.
type streamNotification struct {
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  streamDeltaParams `json:"params"`
}

// --- daimon.activity.append --------------------------------------------------

type activityAppendParams struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload,omitempty"`
}

type activityAppendResult struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

func (s *Server) handleActivityAppend(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p activityAppendParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.Kind == "" {
		return nil, newError(CodeInvalidParams, "kind is required")
	}
	entry, err := s.alog.Append(ctx, activity.Kind(p.Kind), p.Payload)
	if err != nil {
		return nil, mapActivityError(err, "append")
	}
	return activityAppendResult{ID: entry.ID, Hash: entry.Hash}, nil
}

// --- daimon.activity.query ---------------------------------------------------

type activityQueryParams struct {
	Since int64  `json:"since,omitempty"`
	Until int64  `json:"until,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (s *Server) handleActivityQuery(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p activityQueryParams
	if rpcErr := decodeParams(params, &p); rpcErr != nil {
		return nil, rpcErr
	}
	entries, err := s.alog.Query(ctx, activity.QueryOptions{
		Since: p.Since,
		Until: p.Until,
		Kind:  activity.Kind(p.Kind),
		Limit: p.Limit,
	})
	if err != nil {
		return nil, mapActivityError(err, "query")
	}
	// Per SPEC §8.2, every query against the log is itself a logged event.
	// We append AFTER reading so the queried entry isn't visible in this
	// response; future queries see it.
	if _, err := s.alog.Append(ctx, activity.KindActivityQueried, map[string]any{
		"matched": len(entries),
	}); err != nil {
		s.logf("activity append (activity.queried): %v", err)
	}
	return entries, nil
}

// --- daimon.activity.verify --------------------------------------------------

type activityVerifyResult struct {
	Verified int  `json:"verified"`
	OK       bool `json:"ok"`
}

// handleActivityVerify walks the activity-log chain end-to-end, asserting
// prev_hash continuity, BLAKE3 hash recomputation over the canonical plaintext,
// and Ed25519 signature validity for every entry. On success appends a new
// activity.verified entry to the log so the audit trail records the
// verification itself; on failure returns a typed CodeInternalError carrying
// the underlying activity error and does NOT append (extending a corrupt chain
// would compound the problem — see SPEC §6.1).
//
// Like activity.query, this method takes no params; the verifier walks the
// whole chain or nothing.
func (s *Server) handleActivityVerify(ctx context.Context, _ json.RawMessage) (any, *RPCError) {
	n, err := s.alog.Verify(ctx)
	if err != nil {
		return nil, mapActivityError(err, "verify")
	}
	// Append AFTER verification so the verify-of-N entry isn't itself in scope
	// of this verify. The next verify call walks N+1 entries (including this
	// one) — same self-incrementing property as activity.queried.
	if _, aerr := s.alog.Append(ctx, activity.KindActivityVerified, map[string]any{
		"verified": n,
	}); aerr != nil {
		s.logf("activity append (activity.verified): %v", aerr)
	}
	return activityVerifyResult{Verified: n, OK: true}, nil
}

// --- helpers -----------------------------------------------------------------

// decodeParams unmarshals params into v, returning a typed RPC error on
// failure. Empty params is permitted (handlers decide whether their fields
// are optional).
func decodeParams(params json.RawMessage, v any) *RPCError {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return newError(CodeInvalidParams, "Invalid params", err.Error())
	}
	return nil
}

// mapMemoryError translates errors from internal/memory into RPC error codes.
// Unrecognised errors fall through to CodeInternalError.
func mapMemoryError(err error, op string) *RPCError {
	switch {
	case errors.Is(err, memory.ErrNotFound):
		return newError(CodeNotFound, "memory not found")
	case errors.Is(err, memory.ErrSignatureFailed):
		return newError(CodeSignatureFailed, "memory signature verification failed")
	case errors.Is(err, memory.ErrInvalidKind):
		return newError(CodeInvalidKind, "invalid memory kind")
	case errors.Is(err, memory.ErrEmptyContent):
		return newError(CodeInvalidParams, "content is required")
	case errors.Is(err, memory.ErrUnknownFormat):
		return newError(CodeInvalidParams, err.Error())
	case errors.Is(err, memory.ErrDIDMismatch):
		return newError(CodeSignatureFailed, err.Error())
	case errors.Is(err, identity.ErrIdentityLocked):
		return newError(CodeIdentityLocked, "identity is locked")
	}
	return newError(CodeInternalError, fmt.Sprintf("memory.%s: %v", op, err))
}

// mapActivityError translates errors from internal/activity into RPC errors.
func mapActivityError(err error, op string) *RPCError {
	switch {
	case errors.Is(err, activity.ErrEmptyKind):
		return newError(CodeInvalidParams, "kind is required")
	case errors.Is(err, activity.ErrLogClosed):
		return newError(CodeInternalError, "activity log is closed")
	case errors.Is(err, activity.ErrSignatureFailed):
		return newError(CodeSignatureFailed, "activity signature verification failed", err.Error())
	case errors.Is(err, activity.ErrChainBroken):
		return newError(CodeInternalError, "activity chain broken", err.Error())
	case errors.Is(err, activity.ErrHashMismatch):
		return newError(CodeInternalError, "activity hash mismatch", err.Error())
	case errors.Is(err, activity.ErrInvalidCiphertext):
		return newError(CodeInternalError, "activity payload AEAD authentication failed", err.Error())
	case errors.Is(err, identity.ErrIdentityLocked):
		return newError(CodeIdentityLocked, "identity is locked")
	}
	return newError(CodeInternalError, fmt.Sprintf("activity.%s: %v", op, err))
}

// recencyBoost computes exp(-age_days / 30) per SPEC §11.
func recencyBoost(nowMs, createdMs int64) float64 {
	const dayMs = 86_400_000
	if createdMs <= 0 || nowMs <= createdMs {
		return 1.0
	}
	ageDays := float64(nowMs-createdMs) / float64(dayMs)
	return math.Exp(-ageDays / 30.0)
}

// estimateTokens is a rough chars/4 heuristic suitable for the v0.1 token
// budget. Real tokenization arrives with the provider adapters that own the
// model-specific tokenizer.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := (len(s) + 3) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// kindAllowlist returns nil when the caller did not supply a filter (= match
// any kind), else a set of kind strings to keep.
func kindAllowlist(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	m := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		m[k] = true
	}
	return m
}
