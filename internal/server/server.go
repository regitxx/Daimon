package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
	"github.com/regitxx/Daimon/internal/provider"
)

// Options bundles the dependencies a Server needs.
//
// The server runs in one of two modes:
//
//   - **Unlocked-from-construction (demo mode).** Identity, Store, and Log
//     are all supplied; the server starts ready and dispatches every method.
//     Used by `daimond demo` and by every existing test fixture.
//
//   - **Locked-until-unlock (serve mode).** Unlock is non-nil; Identity,
//     Store, and Log MAY be omitted. The server starts in a locked state
//     where every method except daimon.identity.unlock returns
//     CodeIdentityLocked. The unlock RPC calls Unlock(password); on success
//     the returned trio populates the server and dispatch is permitted on
//     all methods thereafter. Lock is one-way for v0.1 — once unlocked, the
//     daemon stays unlocked until process exit.
//
// Providers and Credentials are optional in both modes. When Providers is
// nil, daimon.provider.* return CodeNotFound (no provider registry attached).
// When Credentials is nil, daimon.provider.list reports adapters as
// unconfigured.
type Options struct {
	Identity    *identity.Identity
	Store       *memory.Store
	Log         *activity.Log
	Providers   *provider.Registry
	Credentials *provider.CredentialStore

	// Unlock, if non-nil, switches the server into locked-mode. The unlock
	// RPC calls this with the user-supplied password and expects either the
	// loaded trio (identity + store + log) or a wrong-password / IO error.
	// Returning a non-nil error from Unlock leaves the server locked and the
	// RPC reports CodeIdentityLocked with the error message in Data.
	Unlock UnlockFunc

	// Logger is the destination for operational messages (accept errors,
	// activity-log append failures, etc.). Nil disables logging.
	Logger *log.Logger
}

// UnlockFunc is the keystore-loading callback supplied by the daemon main.
// It is invoked exactly once per server lifetime: the first successful call
// transitions the server from locked to unlocked. Subsequent unlock RPCs are
// rejected (the daemon is already running, no need to re-derive the key).
type UnlockFunc func(ctx context.Context, password string) (*identity.Identity, *memory.Store, *activity.Log, error)

// Server is a JSON-RPC 2.0 endpoint that exposes the Daimon Protocol surface
// from SPEC §6.1.
//
// Lifecycle: New → Listen(socketPath) → Serve(ctx) (blocks) → Close. Close is
// idempotent and safe to call from any goroutine; it cancels in-flight handler
// contexts and drops the socket file.
//
// Lock state: tracked via the unlocked atomic.Bool. Construction in unlock-
// mode (Options.Unlock != nil) sets unlocked=false; the unlock RPC populates
// the principal trio and then transitions unlocked→true via atomic.Store,
// which carries release semantics so the field writes happen-before any
// subsequent Load returning true. Demo-mode construction sets the trio first
// and then unlocked=true via the same atomic write, so dispatch sees a
// consistent snapshot in either path.
type Server struct {
	id        *identity.Identity
	store     *memory.Store
	alog      *activity.Log
	providers *provider.Registry
	creds     *provider.CredentialStore

	logger *log.Logger

	methods map[string]methodHandler

	// unlocked is the locked→unlocked one-shot. Loaded by dispatch, stored by
	// New (demo mode) or by handleIdentityUnlock (serve mode).
	unlocked atomic.Bool

	// unlockFn is the keystore-loading callback; nil in demo mode.
	// unlockOnce serializes unlock attempts so two concurrent unlock RPCs
	// can't both load the keystore (cheap deduplication on the slow path).
	unlockFn   UnlockFunc
	unlockOnce sync.Mutex

	mu         sync.Mutex
	listener   net.Listener
	socketPath string
	closed     bool
	closeOnce  sync.Once

	// serveCtx is cancelled on Close; per-connection goroutines derive from
	// it so a Close cancels their handler contexts.
	serveCtx    context.Context
	serveCancel context.CancelFunc

	// conns tracks live connections so Close can drain them.
	conns sync.WaitGroup
}

// methodHandler is the per-method dispatch signature. params is the raw JSON
// of the request's "params" field (may be empty); the returned value is
// JSON-marshaled into the response's "result" field.
type methodHandler func(ctx context.Context, params json.RawMessage) (any, *RPCError)

// New constructs a Server. Construction validates Options for the chosen
// mode:
//
//   - Demo mode (Options.Unlock == nil): Identity, Store, and Log are all
//     required; the server starts unlocked.
//
//   - Serve mode (Options.Unlock != nil): the trio MAY be omitted; the server
//     starts locked and only daimon.identity.unlock is callable.
//
// Methods are registered eagerly so a typo in the registration table fails at
// construction time rather than at first request.
func New(opts Options) (*Server, error) {
	s := &Server{
		providers: opts.Providers,
		creds:     opts.Credentials,
		logger:    opts.Logger,
	}
	if opts.Unlock != nil {
		// Serve mode: trio may be nil; unlock callback populates them.
		s.unlockFn = opts.Unlock
		s.id = opts.Identity
		s.store = opts.Store
		s.alog = opts.Log
		// Stays locked until handleIdentityUnlock runs successfully.
	} else {
		if opts.Identity == nil {
			return nil, errors.New("server: Identity is required (or supply Options.Unlock for serve mode)")
		}
		if opts.Store == nil {
			return nil, errors.New("server: Store is required (or supply Options.Unlock for serve mode)")
		}
		if opts.Log == nil {
			return nil, errors.New("server: Log is required (or supply Options.Unlock for serve mode)")
		}
		s.id = opts.Identity
		s.store = opts.Store
		s.alog = opts.Log
		s.unlocked.Store(true)
	}
	s.registerMethods()
	return s, nil
}

// IsUnlocked reports whether the server has loaded the principal trio.
// Useful for tests and diagnostics; not exposed via the wire surface.
func (s *Server) IsUnlocked() bool { return s.unlocked.Load() }

// Listen binds the Unix socket at socketPath. If a stale socket file exists
// at that path AND nothing is currently listening on it, the stale file is
// removed; if something IS listening, the bind fails (we never knock another
// process off its socket).
//
// The socket is set to mode 0600 so the kernel enforces the access policy
// SPEC §6 calls for ("auth by filesystem permissions, owner-only").
func (s *Server) Listen(socketPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return fmt.Errorf("server: already listening on %s", s.socketPath)
	}
	if s.closed {
		return errors.New("server: closed")
	}

	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		return fmt.Errorf("server: chmod 0600 %s: %w", socketPath, err)
	}
	s.listener = ln
	s.socketPath = socketPath
	return nil
}

// Addr returns the bound socket path, or "" if Listen has not been called.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.socketPath
}

// Serve accepts connections until the listener is closed. Each connection is
// handled in its own goroutine. Returns net.ErrClosed when the listener is
// closed normally (i.e. when Close is called); other accept errors are logged
// and Serve continues.
//
// ctx propagates to per-handler invocations. Cancelling ctx is equivalent to
// calling Close.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	if s.listener == nil {
		s.mu.Unlock()
		return errors.New("server: Listen must be called before Serve")
	}
	ln := s.listener
	s.serveCtx, s.serveCancel = context.WithCancel(ctx)
	s.mu.Unlock()

	// Honour ctx cancellation by closing the listener; Accept will then
	// return net.ErrClosed and we exit cleanly.
	go func() {
		<-s.serveCtx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				s.conns.Wait()
				return nil
			}
			s.logf("accept error: %v", err)
			continue
		}
		s.conns.Add(1)
		go func(c net.Conn) {
			defer s.conns.Done()
			s.handleConn(s.serveCtx, c)
		}(conn)
	}
}

// Close stops accepting new connections, cancels in-flight handler contexts,
// and removes the socket file. Safe to call from any goroutine; safe to call
// multiple times.
func (s *Server) Close() error {
	var rerr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		ln := s.listener
		path := s.socketPath
		cancel := s.serveCancel
		s.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		if ln != nil {
			if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				rerr = err
			}
		}
		// Best-effort socket file cleanup; not fatal if it's already gone.
		if path != "" {
			_ = os.Remove(path)
		}
	})
	return rerr
}

// handleConn reads JSON-RPC requests from c serially and writes responses
// back. A single connection processes one request at a time; concurrency
// across clients comes from many connections.
//
// The streaming method (daimon.provider.stream) is special-cased: it is the
// only handler that needs to push multiple frames (one notification per
// delta, then the final response) before the conn can read the next request.
// We dispatch it inline so the writer here remains the sole writer to the
// conn, which preserves the single-writer-per-connection invariant without
// needing a mutex on the encoder.
func (s *Server) handleConn(ctx context.Context, c net.Conn) {
	defer c.Close()

	dec := json.NewDecoder(c)
	enc := json.NewEncoder(c)

	for {
		// Bail out promptly on shutdown.
		if err := ctx.Err(); err != nil {
			return
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			// Surface a parse error with no recoverable id and close the
			// connection — we have no way to resync the JSON stream.
			_ = enc.Encode(parseErrorResponse(err.Error()))
			return
		}

		// Quick first parse: we need the method name to decide between the
		// streaming and non-streaming paths. A second pass inside dispatch /
		// handleProviderStream pulls out params and id; this is cheap because
		// json.Unmarshal stops at the first level.
		var head Request
		if err := json.Unmarshal(raw, &head); err == nil && head.Method == methodProviderStream {
			if err := s.handleProviderStream(ctx, enc, head); err != nil {
				s.logf("write stream response: %v", err)
				return
			}
			continue
		}

		resp := s.dispatch(ctx, raw)
		if resp == nil {
			// Notification: no response. Continue reading.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			s.logf("write response: %v", err)
			return
		}
	}
}

// dispatch parses a single raw JSON request and routes it through the
// registered method handler. Returns nil for notifications (no reply).
func (s *Server) dispatch(ctx context.Context, raw json.RawMessage) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidRequestResponse(err.Error(), nil)
	}
	if req.JSONRPC != JSONRPCVersion {
		if req.IsNotification() {
			return nil
		}
		return invalidRequestResponse(
			fmt.Sprintf("unsupported jsonrpc version %q (require %q)", req.JSONRPC, JSONRPCVersion),
			req.ID,
		)
	}
	if req.Method == "" {
		if req.IsNotification() {
			return nil
		}
		return invalidRequestResponse("method is required", req.ID)
	}

	handler, ok := s.methods[req.Method]
	if !ok {
		if req.IsNotification() {
			return nil
		}
		return errorResponse(req.ID, newError(CodeMethodNotFound, "Method not found", req.Method))
	}

	// Locked-state gate. Pre-unlock, only daimon.identity.unlock is callable.
	// All other methods return CodeIdentityLocked so the client knows to
	// prompt the user instead of retrying. Notifications drop silently —
	// the wire-level invariant (no response to a notification) outranks the
	// "tell the client to unlock" hint, and a locked daimon ignoring a
	// signed notification is the same as a missing daimon ignoring it.
	if !s.unlocked.Load() && req.Method != methodIdentityUnlock {
		if req.IsNotification() {
			return nil
		}
		return errorResponse(req.ID, newError(CodeIdentityLocked, "identity is locked", req.Method))
	}

	result, rpcErr := handler(ctx, req.Params)
	if req.IsNotification() {
		return nil
	}
	if rpcErr != nil {
		return errorResponse(req.ID, rpcErr)
	}
	return successResponse(req.ID, result)
}

func (s *Server) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

// removeStaleSocket clears a leftover socket file if no process is listening.
// We probe by attempting a transient Dial: if it succeeds, another process
// owns the socket and we refuse to take it; if it fails with ECONNREFUSED, the
// file is stale and can be removed.
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("server: stat socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		// Path exists and is not a socket — refuse to delete arbitrary files.
		return fmt.Errorf("server: %s exists and is not a socket", path)
	}
	c, err := net.Dial("unix", path)
	if err == nil {
		_ = c.Close()
		return fmt.Errorf("server: socket %s is in use by another process", path)
	}
	return os.Remove(path)
}
