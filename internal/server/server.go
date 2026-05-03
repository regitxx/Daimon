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

	"github.com/regitxx/Daimon/internal/activity"
	"github.com/regitxx/Daimon/internal/identity"
	"github.com/regitxx/Daimon/internal/memory"
)

// Options bundles the dependencies a Server needs. All three primitives are
// required; the server is the orchestrator, not their owner.
type Options struct {
	Identity *identity.Identity
	Store    *memory.Store
	Log      *activity.Log

	// Logger is the destination for operational messages (accept errors,
	// activity-log append failures, etc.). Nil disables logging.
	Logger *log.Logger
}

// Server is a JSON-RPC 2.0 endpoint that exposes the Daimon Protocol surface
// from SPEC §6.1.
//
// Lifecycle: New → Listen(socketPath) → Serve(ctx) (blocks) → Close. Close is
// idempotent and safe to call from any goroutine; it cancels in-flight handler
// contexts and drops the socket file.
type Server struct {
	id    *identity.Identity
	store *memory.Store
	alog  *activity.Log

	logger *log.Logger

	methods map[string]methodHandler

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

// New constructs a Server bound to the given primitives. Methods are
// registered eagerly so a typo in the registration table fails at construction
// time rather than at first request.
func New(opts Options) (*Server, error) {
	if opts.Identity == nil {
		return nil, errors.New("server: Identity is required")
	}
	if opts.Store == nil {
		return nil, errors.New("server: Store is required")
	}
	if opts.Log == nil {
		return nil, errors.New("server: Log is required")
	}
	s := &Server{
		id:     opts.Identity,
		store:  opts.Store,
		alog:   opts.Log,
		logger: opts.Logger,
	}
	s.registerMethods()
	return s, nil
}

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
