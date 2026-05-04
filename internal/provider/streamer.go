package provider

import "context"

// Delta is one increment of streamed output. v0.1.x is text-only; tool-call
// fragments and role markers extend this shape later.
//
// Adapters MUST send only non-empty Content deltas — a stream of empty deltas
// would force the server and CLI to filter, and there is no information in an
// empty delta the final Response can't supply.
type Delta struct {
	Content string `json:"content"`
}

// Streamer is the optional companion to Provider for adapters that can render
// output incrementally. Adapters opt in by implementing this interface
// alongside Provider; the daimon.provider.stream RPC type-asserts before
// dispatching, so adapters that haven't implemented Stream report
// "does not support streaming" rather than silently buffering.
//
// Contract:
//
//   - The adapter MUST send each non-empty content increment to deltas in the
//     order it arrives from the upstream model.
//   - The adapter MUST close deltas before returning, regardless of whether
//     the stream completed cleanly or errored midway. The server reads until
//     close to know the stream is finished.
//   - The adapter MUST honour ctx cancellation: if ctx is cancelled mid-
//     stream, the adapter exits promptly, closes deltas, and returns
//     ctx.Err() (or wraps it). No upstream HTTP call may continue past the
//     cancellation — the user wanted to stop, and any further tokens cost the
//     user money or rate-limit budget.
//   - On success, the returned *Response carries the fully accumulated
//     content (sum of all delta Contents), the final model id, normalised
//     stop reason, usage if reported, and the raw upstream payload of the
//     terminal frame.
//
// Channel ownership: the caller (server handler) creates and consumes the
// channel; the adapter only sends and closes. This keeps the adapter's
// concurrency model simple — one writer, one closer.
type Streamer interface {
	Provider
	Stream(ctx context.Context, req Request, deltas chan<- Delta) (*Response, error)
}
