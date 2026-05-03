// Package server implements the Daimon Protocol RPC surface from SPEC §6.
//
// Transport is JSON-RPC 2.0 over a Unix socket at $DAIMON_HOME/daimon.sock,
// owner-only (mode 0600). The server wires the three local primitives —
// identity, memory, activity — to the method names defined in SPEC §6.1, plus
// daimon.context.get (a thin composition of memory.Search with the
// recency-weighted retrieval policy locked in SPEC §11).
//
// v0.1 scope of this package:
//
//   - JSON-RPC 2.0 single requests (batches deferred — SPEC §6.1 doesn't
//     require them and no v0.1 client needs them yet)
//   - Notifications (request without "id") are accepted; the server processes
//     them and produces no response, per the JSON-RPC 2.0 spec
//   - Per-connection serial dispatch; many connections concurrent
//
// Deliberately deferred (in priority order):
//
//   - HTTPS + mutual TLS transport (SPEC §6 alternative). Unix socket is
//     sufficient for the v0.1 single-machine target.
//   - Batch requests
//   - Server-Sent Events streaming (SPEC §11; HTTPS-only)
//   - daimon.provider.* methods — these land with the provider-adapter
//     primitive and are intentionally absent here. Calling them returns
//     CodeMethodNotFound, which is the honest signal.
package server

import (
	"encoding/json"
	"fmt"
)

// JSONRPCVersion is the only protocol version the server speaks. Requests with
// a different "jsonrpc" field are rejected with CodeInvalidRequest.
const JSONRPCVersion = "2.0"

// Request is a JSON-RPC 2.0 request object.
//
// Params is left as RawMessage so each handler can decode into its own typed
// shape without an intermediate map round-trip. ID is RawMessage so we can
// distinguish absent ("notification") from present-but-null.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

// IsNotification reports whether this request is a JSON-RPC 2.0 notification —
// a request with no "id" member. The server MUST NOT reply to notifications.
//
// Note that "id": null is NOT a notification per the spec; it is a request
// whose response id will also be null. Distinguishing the two requires looking
// at whether the field was present in the JSON, which is why ID is RawMessage.
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0
}

// Response is a JSON-RPC 2.0 response object. Exactly one of Result and Error
// is set. ID echoes the request ID (and is JSON null for parse errors and
// invalid requests where no id could be recovered).
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// RPCError is the JSON-RPC 2.0 error object. Code and Message are required;
// Data is an arbitrary payload for additional context.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// JSON-RPC 2.0 reserved error codes (-32768 .. -32000).
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Daimon-specific application error codes. The JSON-RPC 2.0 spec reserves
// -32000 .. -32099 for "Server error" — implementation-defined. Below that
// range is reserved for the protocol itself.
//
// Codes are stable across v0.1 releases; clients can switch on them.
const (
	// CodeIdentityLocked — the daimon is locked and cannot sign. The client
	// should prompt the user to unlock.
	CodeIdentityLocked = -32001

	// CodeNotFound — a referenced object (memory id, activity id) does not
	// exist. Distinct from CodeInvalidParams: the params were well-formed.
	CodeNotFound = -32002

	// CodeSignatureFailed — a stored signature did not verify, or an imported
	// document had a bad signature. Strong tamper signal; clients should not
	// silently retry.
	CodeSignatureFailed = -32003

	// CodeInvalidKind — the supplied memory or activity kind is not one of
	// the v0.1 enumerated kinds.
	CodeInvalidKind = -32004

	// CodeNotImplemented — the method exists in the SPEC §6.1 surface but is
	// not yet wired in this build. Surfaces specifically for forthcoming
	// provider.* methods.
	CodeNotImplemented = -32005
)

// nullID is the JSON-RPC null id, used in responses where no client id could
// be recovered (parse errors, malformed requests).
var nullID = json.RawMessage(`null`)

// newError constructs an RPCError with optional structured data.
func newError(code int, message string, data ...any) *RPCError {
	e := &RPCError{Code: code, Message: message}
	if len(data) > 0 {
		e.Data = data[0]
	}
	return e
}

// parseError returns the standard parse-error response body.
func parseErrorResponse(detail string) *Response {
	return &Response{
		JSONRPC: JSONRPCVersion,
		Error:   newError(CodeParseError, "Parse error", detail),
		ID:      nullID,
	}
}

// invalidRequestResponse returns the standard invalid-request body, echoing
// id when one was recoverable from the malformed object.
func invalidRequestResponse(detail string, id json.RawMessage) *Response {
	if len(id) == 0 {
		id = nullID
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		Error:   newError(CodeInvalidRequest, "Invalid Request", detail),
		ID:      id,
	}
}

// successResponse builds a result-bearing response.
func successResponse(id json.RawMessage, result any) *Response {
	if len(id) == 0 {
		id = nullID
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		Result:  result,
		ID:      id,
	}
}

// errorResponse builds an error-bearing response.
func errorResponse(id json.RawMessage, e *RPCError) *Response {
	if len(id) == 0 {
		id = nullID
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		Error:   e,
		ID:      id,
	}
}
