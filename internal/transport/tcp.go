package transport

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

// maxFrameBody is the maximum plaintext size per application frame.
// The Noise spec limits messages to 65535 bytes; we subtract the 16-byte
// ChaCha20-Poly1305 AEAD tag to get the max usable plaintext length.
// JSON-RPC frames over the peer protocol are far below this limit in
// normal use (typical echo / invoke payloads are ≪ 4 KiB).
const maxFrameBody = 65519 // 65535 - 16

// Listener accepts inbound Noise IK connections from peer daimons.
// Created via ListenTCP; close via Listener.Close.
type Listener struct {
	ln      net.Listener
	privKey ed25519.PrivateKey
}

// ListenTCP starts a TCP listener on addr. localPriv is the daemon's
// Ed25519 identity private key; it is used as the Noise IK static key
// (after internal X25519 conversion) for all inbound handshakes.
//
// Pass ":0" for addr to get an OS-assigned ephemeral port — use
// Listener.Addr() to discover the actual address.
func ListenTCP(addr string, localPriv ed25519.PrivateKey) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: listen %s: %w", addr, err)
	}
	return &Listener{ln: ln, privKey: localPriv}, nil
}

// Addr returns the listener's bound TCP address.
func (l *Listener) Addr() net.Addr { return l.ln.Addr() }

// Accept waits for an inbound TCP connection and drives the Noise IK
// responder handshake to completion. Returns an authenticated *Conn on
// success. The peer's X25519 static public key (which was revealed during
// the handshake) is available via Conn.PeerX25519.
//
// The caller is responsible for closing the returned Conn when done.
func (l *Listener) Accept() (*Conn, error) {
	tc, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	hs, err := NewResponder(l.privKey)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport accept: responder init: %w", err)
	}

	// Handshake message 1: initiator → responder.
	msg1, err := recvHandshakeMsg(tc)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport accept: recv msg1: %w", err)
	}
	if _, err := hs.ReadMessage(msg1); err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport accept: process msg1: %w", err)
	}

	// Handshake message 2: responder → initiator.
	// This message completes the handshake on both sides.
	msg2, err := hs.WriteMessage(nil)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport accept: write msg2: %w", err)
	}
	if err := sendHandshakeMsg(tc, msg2); err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport accept: send msg2: %w", err)
	}

	return &Conn{tc: tc, hs: hs, PeerX25519: hs.PeerStatic()}, nil
}

// Close stops accepting new connections.
func (l *Listener) Close() error { return l.ln.Close() }

// Conn is a bidirectional, Noise IK authenticated, encrypted channel
// over TCP. After construction via Dial or Listener.Accept, use
// SendFrame / RecvFrame for application data.
//
// Conn is NOT safe for concurrent use from multiple goroutines; callers
// requiring concurrent send+recv must serialize with a mutex.
type Conn struct {
	tc  net.Conn
	hs  *HandshakeState // holds the Noise cipher states post-handshake

	// PeerX25519 is the peer's X25519 static public key as authenticated
	// by the Noise IK handshake. Populated after construction; zero value
	// means unset (should not happen for a properly-constructed Conn).
	//
	// To recover the peer's DID: this value IS the X25519 public key.
	// The peer's Ed25519 → X25519 is one-way, so the DID cannot be
	// re-derived from this value alone; the caller must either track the
	// DID separately (stored at dial time) or use the address book lookup.
	PeerX25519 [X25519KeySize]byte
}

// SendFrame encrypts data with Noise AEAD and writes a 4-byte-length-prefixed
// ciphertext frame to the connection. Frames are strictly ordered; callers
// MUST NOT reorder or skip send calls — each call advances the Noise send
// nonce by one.
func (c *Conn) SendFrame(data []byte) error {
	if len(data) > maxFrameBody {
		return fmt.Errorf("transport: frame body too large: %d > %d", len(data), maxFrameBody)
	}
	ct, err := c.hs.Encrypt(nil, nil, data)
	if err != nil {
		return fmt.Errorf("transport: send encrypt: %w", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(ct)))
	if _, err := c.tc.Write(hdr[:]); err != nil {
		return fmt.Errorf("transport: send header: %w", err)
	}
	if _, err := c.tc.Write(ct); err != nil {
		return fmt.Errorf("transport: send body: %w", err)
	}
	return nil
}

// RecvFrame reads one length-prefixed frame from the connection, decrypts it,
// and returns the plaintext. Blocks until a complete frame is available.
// Returns an error on any network failure or AEAD authentication failure
// (which indicates wire tampering or a stale connection).
func (c *Conn) RecvFrame() ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.tc, hdr[:]); err != nil {
		return nil, fmt.Errorf("transport: recv header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	// maxFrameBody + 16 = max Noise ciphertext length (plaintext + AEAD tag)
	if n > maxFrameBody+16 {
		return nil, fmt.Errorf("transport: oversized frame: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.tc, buf); err != nil {
		return nil, fmt.Errorf("transport: recv body: %w", err)
	}
	pt, err := c.hs.Decrypt(nil, nil, buf)
	if err != nil {
		return nil, fmt.Errorf("transport: recv decrypt: %w", err)
	}
	return pt, nil
}

// Close closes the underlying TCP connection.
func (c *Conn) Close() error { return c.tc.Close() }

// Dial dials network+addr, performs the Noise IK initiator handshake using
// localPriv as the local static key and remotePub as the expected remote
// static key. Returns an authenticated *Conn on success.
//
// remotePub should be derived from the peer's DID via Ed25519PublicToX25519
// so the handshake simultaneously establishes encryption AND verifies the
// peer's identity matches the expected DID. A mismatch causes the handshake
// to fail (the Noise layer detects the key discrepancy).
//
// Typical call:
//
//	remotePub, _ := transport.Ed25519PublicToX25519(peerEdPub)
//	conn, err   := transport.Dial("tcp", "peer.example.com:4242", myPrivKey, remotePub)
func Dial(network, addr string, localPriv ed25519.PrivateKey, remotePub [X25519KeySize]byte) (*Conn, error) {
	tc, err := net.Dial(network, addr)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s: %w", addr, err)
	}
	hs, err := NewInitiator(localPriv, remotePub)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport: dial initiator init: %w", err)
	}

	// Handshake message 1: initiator → responder.
	msg1, err := hs.WriteMessage(nil)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport: dial write msg1: %w", err)
	}
	if err := sendHandshakeMsg(tc, msg1); err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport: dial send msg1: %w", err)
	}

	// Handshake message 2: responder → initiator.
	msg2, err := recvHandshakeMsg(tc)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport: dial recv msg2: %w", err)
	}
	if _, err := hs.ReadMessage(msg2); err != nil {
		tc.Close()
		return nil, fmt.Errorf("transport: dial process msg2: %w", err)
	}

	return &Conn{tc: tc, hs: hs, PeerX25519: hs.PeerStatic()}, nil
}

// ErrConnClosed is returned by SendFrame / RecvFrame when the underlying
// TCP connection has been closed.
var ErrConnClosed = errors.New("transport: connection closed")

// sendHandshakeMsg writes a 2-byte-length-prefixed handshake message.
// Noise IK handshake messages are ≤ 132 bytes so 2-byte length is sufficient.
func sendHandshakeMsg(w io.Writer, msg []byte) error {
	if len(msg) > 65535 {
		return errors.New("transport: handshake message too large")
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(msg)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(msg)
	return err
}

// recvHandshakeMsg reads a 2-byte-length-prefixed handshake message.
func recvHandshakeMsg(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
