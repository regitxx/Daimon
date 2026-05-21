package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// cmdPeer routes the `peer` subcommand surface (v0.3 federation).
//
// Subcommand tree:
//
//	daimon peer listen [--addr tcp://0.0.0.0:9999]    Start inbound TCP listener
//	daimon peer dial --did <did> --endpoint <ep>       Open a Noise IK channel
//	daimon peer close <channel_id>                     Close an open channel
//	daimon peer list                                   List open channels
//	daimon peer echo <channel_id> <message>            Send a peer.echo
//	daimon peer invoke <channel_id> <method>           Raw peer.invoke
//	daimon peer pay-required <channel_id> <service>    x402 price discovery
//	daimon peer address-book <sub>                     Manage the address book
func cmdPeer(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon peer <dial|close|list|echo|invoke|pay-required|address-book> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "dial":
		return cmdPeerDial(rest)
	case "close":
		return cmdPeerClose(rest)
	case "list":
		return cmdPeerList(rest)
	case "echo":
		return cmdPeerEcho(rest)
	case "listen":
		return cmdPeerListen(rest)
	case "invoke":
		return cmdPeerInvoke(rest)
	case "pay-required":
		return cmdPeerPayRequired(rest)
	case "address-book":
		return cmdPeerAddressBook(rest)
	default:
		return fmt.Errorf("daimon peer: unknown subcommand %q", sub)
	}
}

// cmdFederation routes the `federation` subcommand surface (v0.3).
//
//	daimon federation config    Show DID, transport key, protocols, endpoint
func cmdFederation(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon federation config [--json]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "config":
		return cmdFederationConfig(rest)
	default:
		return fmt.Errorf("daimon federation: unknown subcommand %q", sub)
	}
}

// ---------------------------------------------------------------------------
// daimon federation config
// ---------------------------------------------------------------------------

// federationConfigWire mirrors the JSON-RPC result of daimon.federation.config
// (see internal/server/federation_handlers.go federationConfigResult).
type federationConfigWire struct {
	DID                      string   `json:"did"`
	TransportPubkeyMultibase string   `json:"transport_pubkey_multibase"`
	DIDMethods               []string `json:"did_methods"`
	Protocols                []string `json:"protocols"`
	PublicEndpoint           string   `json:"public_endpoint,omitempty"`
	FederationVersion        string   `json:"federation_version"`
}

func cmdFederationConfig(args []string) error {
	fs := flag.NewFlagSet("daimon federation config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the full result envelope as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon federation config [--json]")
	}

	var cfg federationConfigWire
	if err := daemonCall("daimon.federation.config", nil, &cfg); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(cfg)
	}

	fmt.Printf("DID:              %s\n", cfg.DID)
	fmt.Printf("Transport pubkey: %s\n", cfg.TransportPubkeyMultibase)
	fmt.Printf("Protocols:        %s\n", strings.Join(cfg.Protocols, ", "))
	fmt.Printf("DID methods:      %s\n", strings.Join(cfg.DIDMethods, ", "))
	fmt.Printf("Version:          %s\n", cfg.FederationVersion)
	if cfg.PublicEndpoint != "" {
		fmt.Printf("Public endpoint:  %s\n", cfg.PublicEndpoint)
	} else {
		fmt.Printf("Public endpoint:  (not listening)\n")
	}
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer listen
// ---------------------------------------------------------------------------

// cmdPeerListen starts the daemon's inbound Noise IK TCP listener. The
// daemon must be unlocked (daimon unlock) before calling this — the
// Noise IK static key is derived from the unlocked identity.
//
// The listener stays running until the daemon exits. Call
// `daimon federation config` at any time to read the bound endpoint back.
func cmdPeerListen(args []string) error {
	fs := flag.NewFlagSet("daimon peer listen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", "", `TCP bind address, e.g. "0.0.0.0:9999" or "tcp://0.0.0.0:9999" (default: 0.0.0.0:0, OS-assigned port)`)
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon peer listen [--addr <addr>] [--json]")
	}

	var params map[string]any
	if *addr != "" {
		params = map[string]any{"addr": *addr}
	}
	var result struct {
		Endpoint string `json:"endpoint"`
	}
	if err := daemonCall("daimon.peer.listen", params, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	fmt.Printf("Listening on: %s\n", result.Endpoint)
	fmt.Fprintln(os.Stderr, "Remote peers can dial you at this address. Share it alongside your DID:")
	fmt.Fprintf(os.Stderr, "  daimon federation config\n")
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer dial
// ---------------------------------------------------------------------------

// peerChannelMeta mirrors the channel metadata returned by peer.dial and
// peer.list (channel_id, peer_did, opened_at).
type peerChannelMeta struct {
	ChannelID string `json:"channel_id"`
	PeerDID   string `json:"peer_did"`
	OpenedAt  string `json:"opened_at"`
}

func cmdPeerDial(args []string) error {
	fs := flag.NewFlagSet("daimon peer dial", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	peerDID := fs.String("did", "", "remote daimon's DID (required)")
	endpoint := fs.String("endpoint", "", `remote TCP endpoint, e.g. "tcp://host:9999" (required)`)
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *peerDID == "" || *endpoint == "" {
		return fmt.Errorf("usage: daimon peer dial --did <did> --endpoint <ep> [--json]")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon peer dial --did <did> --endpoint <ep> [--json]")
	}

	params := map[string]any{"did": *peerDID, "endpoint": *endpoint}
	var result peerChannelMeta
	if err := daemonCall("daimon.peer.dial", params, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	fmt.Printf("Channel opened.\n")
	fmt.Printf("  Channel ID: %s\n", result.ChannelID)
	fmt.Printf("  Peer DID:   %s\n", result.PeerDID)
	fmt.Printf("  Opened at:  %s\n", result.OpenedAt)
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer close
// ---------------------------------------------------------------------------

func cmdPeerClose(args []string) error {
	fs := flag.NewFlagSet("daimon peer close", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon peer close [--json] <channel_id>")
	}
	channelID := fs.Arg(0)

	params := map[string]any{"channel_id": channelID}
	if err := daemonCall("daimon.peer.close", params, nil); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(map[string]string{"channel_id": channelID, "status": "closed"})
	}
	fmt.Printf("Channel %s closed.\n", channelID)
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer list
// ---------------------------------------------------------------------------

type peerListWire struct {
	Channels []peerChannelMeta `json:"channels"`
}

func cmdPeerList(args []string) error {
	fs := flag.NewFlagSet("daimon peer list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon peer list [--json]")
	}

	var result peerListWire
	if err := daemonCall("daimon.peer.list", nil, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	if len(result.Channels) == 0 {
		fmt.Fprintln(os.Stderr, "no open peer channels — dial one with `daimon peer dial`")
		return nil
	}
	tw := tabPrinter(os.Stdout)
	fmt.Fprintln(tw, "CHANNEL ID\tPEER DID\tOPENED AT")
	for _, ch := range result.Channels {
		did := truncate(ch.PeerDID, 40)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", ch.ChannelID, did, ch.OpenedAt)
	}
	return tw.Flush()
}

// ---------------------------------------------------------------------------
// daimon peer echo
// ---------------------------------------------------------------------------

func cmdPeerEcho(args []string) error {
	fs := flag.NewFlagSet("daimon peer echo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the raw invoke result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: daimon peer echo [--json] <channel_id> <message>")
	}
	channelID := fs.Arg(0)
	message := fs.Arg(1)

	peerParams, _ := json.Marshal(map[string]any{"message": message})
	invokeParams := map[string]any{
		"channel_id": channelID,
		"method":     "peer.echo",
		"params":     json.RawMessage(peerParams),
	}
	var invokeResult struct {
		Result json.RawMessage `json:"result,omitempty"`
	}
	if err := daemonCall("daimon.peer.invoke", invokeParams, &invokeResult); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(invokeResult)
	}
	// Decode the echo response: {message, from_did}.
	var echo struct {
		Message string `json:"message"`
		FromDID string `json:"from_did"`
	}
	if err := json.Unmarshal(invokeResult.Result, &echo); err != nil {
		return fmt.Errorf("decode echo result: %w", err)
	}
	fmt.Printf("%s\n", echo.Message)
	fmt.Fprintf(os.Stderr, "from: %s\n", echo.FromDID)
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer invoke
// ---------------------------------------------------------------------------

func cmdPeerInvoke(args []string) error {
	fs := flag.NewFlagSet("daimon peer invoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rawParams := fs.String("params", "", "JSON params to forward to the peer method (optional)")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: daimon peer invoke [--params <json>] [--json] <channel_id> <method>")
	}
	channelID := fs.Arg(0)
	method := fs.Arg(1)

	invokeParams := map[string]any{
		"channel_id": channelID,
		"method":     method,
	}
	if *rawParams != "" {
		// Validate it's well-formed JSON before forwarding.
		if !json.Valid([]byte(*rawParams)) {
			return fmt.Errorf("--params: invalid JSON: %q", *rawParams)
		}
		invokeParams["params"] = json.RawMessage(*rawParams)
	}

	var invokeResult struct {
		Result json.RawMessage `json:"result,omitempty"`
	}
	if err := daemonCall("daimon.peer.invoke", invokeParams, &invokeResult); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(invokeResult)
	}
	// Pretty-print the peer's result JSON to stdout.
	var pretty any
	if len(invokeResult.Result) > 0 {
		if err := json.Unmarshal(invokeResult.Result, &pretty); err != nil {
			// Fallback: print raw.
			fmt.Println(string(invokeResult.Result))
			return nil
		}
		return printJSON(pretty)
	}
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer pay-required
// ---------------------------------------------------------------------------

// paymentRequirementsWire mirrors payment.PaymentRequirements on the wire.
// Declared here (not imported) so cmd/daimon stays a pure client — see
// the "no server imports" constraint in client.go.
type paymentRequirementsWire struct {
	Scheme            string `json:"scheme"`
	Network           string `json:"network"`
	MaxAmountRequired string `json:"maxAmountRequired"`
	Resource          string `json:"resource"`
	Description       string `json:"description"`
	PayTo             string `json:"payTo"`
	MaxTimeoutSeconds int    `json:"maxTimeoutSeconds"`
	Asset             string `json:"asset"`
}

func cmdPeerPayRequired(args []string) error {
	fs := flag.NewFlagSet("daimon peer pay-required", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the raw invoke result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: daimon peer pay-required [--json] <channel_id> <service>")
	}
	channelID := fs.Arg(0)
	service := fs.Arg(1)

	// peer.pay.required is a peer-served verb — invoke it over the channel.
	peerParams, _ := json.Marshal(map[string]any{"service": service})
	invokeParams := map[string]any{
		"channel_id": channelID,
		"method":     "peer.pay.required",
		"params":     json.RawMessage(peerParams),
	}
	var invokeResult struct {
		Result json.RawMessage `json:"result,omitempty"`
	}
	if err := daemonCall("daimon.peer.invoke", invokeParams, &invokeResult); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(invokeResult)
	}

	// Decode: {requirements: [...]}.
	var wrapper struct {
		Requirements []paymentRequirementsWire `json:"requirements"`
	}
	if err := json.Unmarshal(invokeResult.Result, &wrapper); err != nil {
		return fmt.Errorf("decode pay-required result: %w", err)
	}
	if len(wrapper.Requirements) == 0 {
		fmt.Fprintln(os.Stderr, "no payment requirements — service may be free or peer has no wallet configured")
		return nil
	}
	for i, req := range wrapper.Requirements {
		if i > 0 {
			fmt.Println()
		}
		if len(wrapper.Requirements) > 1 {
			fmt.Printf("Requirement %d:\n", i+1)
		}
		fmt.Printf("  Scheme:   %s\n", req.Scheme)
		fmt.Printf("  Network:  %s\n", req.Network)
		fmt.Printf("  Amount:   %s (%s)\n", req.MaxAmountRequired, req.Description)
		fmt.Printf("  Pay to:   %s\n", req.PayTo)
		fmt.Printf("  Asset:    %s\n", req.Asset)
		fmt.Printf("  Resource: %s\n", req.Resource)
		fmt.Printf("  Timeout:  %ds\n", req.MaxTimeoutSeconds)
	}
	return nil
}

// ---------------------------------------------------------------------------
// daimon peer address-book
// ---------------------------------------------------------------------------

func cmdPeerAddressBook(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: daimon peer address-book <list|add|pin|block|unblock|remove> [args]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return cmdPeerAddressBookList(rest)
	case "add":
		return cmdPeerAddressBookAdd(rest)
	case "pin":
		return cmdPeerAddressBookPin(rest)
	case "block":
		return cmdPeerAddressBookBlock(rest)
	case "unblock":
		return cmdPeerAddressBookUnblock(rest)
	case "remove":
		return cmdPeerAddressBookRemove(rest)
	default:
		return fmt.Errorf("daimon peer address-book: unknown subcommand %q", sub)
	}
}

// addressBookEntryWire mirrors the on-wire shape of addressbook.Entry as
// serialised by the server (field names follow the server's JSON tags, e.g.
// pet_name not label, transport_pubkey_multibase not pubkey_multibase).
type addressBookEntryWire struct {
	DID                      string   `json:"did"`
	PetName                  string   `json:"pet_name,omitempty"`
	Status                   string   `json:"status"`
	ApprovedVerbs            []string `json:"approved_verbs,omitempty"`
	TransportPubKeyMultibase string   `json:"transport_pubkey_multibase,omitempty"`
	FirstSeen                string   `json:"first_seen"`
	LastSeen                 string   `json:"last_seen"`
}

type addressBookListWire struct {
	Entries []addressBookEntryWire `json:"entries"`
}

func cmdPeerAddressBookList(args []string) error {
	fs := flag.NewFlagSet("daimon peer address-book list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon peer address-book list [--json]")
	}

	var result addressBookListWire
	if err := daemonCall("daimon.peer.address_book.list", nil, &result); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	if len(result.Entries) == 0 {
		fmt.Fprintln(os.Stderr, "address book is empty")
		return nil
	}
	tw := tabPrinter(os.Stdout)
	fmt.Fprintln(tw, "DID\tSTATUS\tLABEL\tAPPROVED VERBS\tLAST SEEN")
	for _, e := range result.Entries {
		verbs := strings.Join(e.ApprovedVerbs, ",")
		if verbs == "" {
			verbs = "-"
		}
		did := truncate(e.DID, 36)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", did, e.Status, e.PetName, verbs, e.LastSeen)
	}
	return tw.Flush()
}

func cmdPeerAddressBookAdd(args []string) error {
	fs := flag.NewFlagSet("daimon peer address-book add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	did := fs.String("did", "", "peer's DID (required)")
	label := fs.String("label", "", "human-readable label / pet name (optional)")
	pubkeyMultibase := fs.String("pubkey-multibase", "", "transport Ed25519 pubkey in multibase (optional)")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *did == "" {
		return fmt.Errorf("usage: daimon peer address-book add --did <did> [--label <l>] [--pubkey-multibase <pk>] [--json]")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon peer address-book add --did <did> [--label <l>] [--pubkey-multibase <pk>] [--json]")
	}

	// Wire field names match the server handler's JSON tags:
	// pet_name (not label), transport_pubkey_multibase (not pubkey_multibase).
	params := map[string]any{"did": *did}
	if *label != "" {
		params["pet_name"] = *label
	}
	if *pubkeyMultibase != "" {
		params["transport_pubkey_multibase"] = *pubkeyMultibase
	}

	var entry addressBookEntryWire
	if err := daemonCall("daimon.peer.address_book.add", params, &entry); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entry)
	}
	fmt.Printf("Added.\n")
	fmt.Printf("  DID:    %s\n", entry.DID)
	fmt.Printf("  Status: %s\n", entry.Status)
	if entry.PetName != "" {
		fmt.Printf("  Label:  %s\n", entry.PetName)
	}
	return nil
}

func cmdPeerAddressBookPin(args []string) error {
	fs := flag.NewFlagSet("daimon peer address-book pin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	did := fs.String("did", "", "peer's DID (required)")
	verbsStr := fs.String("verbs", "", "comma-separated list of verbs to approve, e.g. peer.ask (required)")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *did == "" || *verbsStr == "" {
		return fmt.Errorf("usage: daimon peer address-book pin --did <did> --verbs <v1,v2,...> [--json]")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: daimon peer address-book pin --did <did> --verbs <v1,v2,...> [--json]")
	}

	verbs := strings.Split(*verbsStr, ",")
	for i, v := range verbs {
		verbs[i] = strings.TrimSpace(v)
	}

	params := map[string]any{"did": *did, "verbs": verbs}
	var entry addressBookEntryWire
	if err := daemonCall("daimon.peer.address_book.pin", params, &entry); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entry)
	}
	fmt.Printf("Pinned.\n")
	fmt.Printf("  DID:            %s\n", entry.DID)
	fmt.Printf("  Status:         %s\n", entry.Status)
	fmt.Printf("  Approved verbs: %s\n", strings.Join(entry.ApprovedVerbs, ", "))
	return nil
}

func cmdPeerAddressBookBlock(args []string) error {
	fs := flag.NewFlagSet("daimon peer address-book block", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	did := fs.String("did", "", "peer's DID (required)")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *did == "" {
		return fmt.Errorf("usage: daimon peer address-book block --did <did> [--json]")
	}

	params := map[string]any{"did": *did}
	var entry addressBookEntryWire
	if err := daemonCall("daimon.peer.address_book.block", params, &entry); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entry)
	}
	fmt.Printf("Blocked.\n")
	fmt.Printf("  DID:    %s\n", entry.DID)
	fmt.Printf("  Status: %s\n", entry.Status)
	return nil
}

func cmdPeerAddressBookUnblock(args []string) error {
	fs := flag.NewFlagSet("daimon peer address-book unblock", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	did := fs.String("did", "", "peer's DID (required)")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *did == "" {
		return fmt.Errorf("usage: daimon peer address-book unblock --did <did> [--json]")
	}

	params := map[string]any{"did": *did}
	var entry addressBookEntryWire
	if err := daemonCall("daimon.peer.address_book.unblock", params, &entry); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(entry)
	}
	fmt.Printf("Unblocked.\n")
	fmt.Printf("  DID:    %s\n", entry.DID)
	fmt.Printf("  Status: %s\n", entry.Status)
	return nil
}

func cmdPeerAddressBookRemove(args []string) error {
	fs := flag.NewFlagSet("daimon peer address-book remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	did := fs.String("did", "", "peer's DID (required)")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *did == "" {
		return fmt.Errorf("usage: daimon peer address-book remove --did <did> [--json]")
	}

	params := map[string]any{"did": *did}
	// handleAddressBookRemove returns {}, not the entry — pass nil for out.
	if err := daemonCall("daimon.peer.address_book.remove", params, nil); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(map[string]string{"did": *did, "status": "removed"})
	}
	fmt.Printf("Removed: %s\n", *did)
	return nil
}
