package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
)

// jsonrpcRequest is the JSON-RPC 2.0 request envelope. We rebuild the wire
// shape by hand instead of importing internal/server because cmd/daimon is a
// pure client — it should not depend on the server's internal types.
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

// jsonrpcResponse mirrors server.Response (kept compatible).
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type jsonrpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("rpc error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Daimon-specific error codes — kept in lockstep with internal/server/jsonrpc.go.
// We hardcode the small set the CLI cares about so callers can switch on them
// without importing the server package.
const (
	codeIdentityLocked = -32001
	codeWrongPassword  = -32008
	codeNotFound       = -32002
	codeInvalidParams  = -32602
	codeInvalidRequest = -32600
)

// rpcCall opens a connection, sends a single request, decodes the response.
// Each call uses a fresh connection — v0.1 has no pipelining requirement.
func rpcCall(socket, method string, params any, out any) error {
	c, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socket, err)
	}
	defer c.Close()

	if err := json.NewEncoder(c).Encode(jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}); err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	var resp jsonrpcResponse
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("unmarshal %s result: %w", method, err)
		}
	}
	return nil
}

// asRPCError unwraps an *jsonrpcError from a returned error if present.
// Useful for callers that want to switch on the code (e.g. CodeIdentityLocked
// → "run daimon unlock first").
func asRPCError(err error) (*jsonrpcError, bool) {
	var rpc *jsonrpcError
	if errors.As(err, &rpc) {
		return rpc, true
	}
	return nil, false
}

// streamFrame is a tolerant decode envelope for the daimon.provider.stream
// wire shape: notifications carry method+params and no id; the terminal
// response carries id + result/error.
type streamFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// rpcStream opens a connection, sends a single request, and reads frames
// until the terminal response with the matching id arrives. onDelta is
// called for each daimon.provider.stream.delta notification; the final
// response result is unmarshalled into out (when non-nil).
//
// onDelta runs synchronously in this function's goroutine — the CLI's stdout
// write per delta is what makes "token-by-token rendering" actually visible.
func rpcStream(socket, method string, params any, onDelta func(string), out any) error {
	c, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socket, err)
	}
	defer c.Close()

	if err := json.NewEncoder(c).Encode(jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}); err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}

	dec := json.NewDecoder(c)
	for {
		var fr streamFrame
		if err := dec.Decode(&fr); err != nil {
			return fmt.Errorf("decode %s frame: %w", method, err)
		}
		// Notification: no id, but a method name.
		if len(fr.ID) == 0 && fr.Method != "" {
			if fr.Method == "daimon.provider.stream.delta" {
				var p struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal(fr.Params, &p); err != nil {
					return fmt.Errorf("decode delta params: %w", err)
				}
				if onDelta != nil && p.Content != "" {
					onDelta(p.Content)
				}
			}
			// Unknown notification methods are ignored — forward-compat for
			// future delta kinds (tool calls, role markers).
			continue
		}
		// Terminal frame.
		if fr.Error != nil {
			return fr.Error
		}
		if out != nil && len(fr.Result) > 0 {
			if err := json.Unmarshal(fr.Result, out); err != nil {
				return fmt.Errorf("unmarshal %s result: %w", method, err)
			}
		}
		return nil
	}
}
