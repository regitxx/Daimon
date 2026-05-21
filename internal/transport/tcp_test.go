package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"testing"
)

// newTCPPair creates a paired Listener+Dial using an ephemeral loopback port.
// Returns the Listener (caller must close) and the address string.
func newTCPListener(t *testing.T, priv ed25519.PrivateKey) *Listener {
	t.Helper()
	ln, err := ListenTCP("127.0.0.1:0", priv)
	if err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// runEchoServer runs a goroutine that accepts one connection and echoes
// every received frame back. Send errors are silently ignored (tests check
// the client side). Returns a channel that closes when the server exits.
func runEchoServer(ln *Listener) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			frame, err := conn.RecvFrame()
			if err != nil {
				return
			}
			_ = conn.SendFrame(frame)
		}
	}()
	return done
}

// --- Basic handshake + echo ---------------------------------------------------

func TestTCP_DialAcceptHandshake(t *testing.T) {
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)

	ln := newTCPListener(t, serverPriv)
	addr := ln.Addr().String()

	// Derive the server's X25519 pubkey from its Ed25519 public key.
	_, serverXPub, err := Ed25519ToX25519Keypair(serverPriv)
	if err != nil {
		t.Fatalf("derive serverXPub: %v", err)
	}

	acceptErrCh := make(chan error, 1)
	var serverConn *Conn
	go func() {
		c, err := ln.Accept()
		serverConn = c
		acceptErrCh <- err
	}()

	clientConn, err := Dial("tcp", addr, clientPriv, serverXPub)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	if err := <-acceptErrCh; err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer serverConn.Close()
}

func TestTCP_SendRecvEcho(t *testing.T) {
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, serverXPub, _ := Ed25519ToX25519Keypair(serverPriv)

	ln := newTCPListener(t, serverPriv)
	serverDone := runEchoServer(ln)

	clientConn, err := Dial("tcp", ln.Addr().String(), clientPriv, serverXPub)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	want := []byte("hello from client")
	if err := clientConn.SendFrame(want); err != nil {
		t.Fatalf("SendFrame: %v", err)
	}
	got, err := clientConn.RecvFrame()
	if err != nil {
		t.Fatalf("RecvFrame: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("echo: got %q, want %q", got, want)
	}

	clientConn.Close()
	<-serverDone
}

func TestTCP_MultipleFrames(t *testing.T) {
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, serverXPub, _ := Ed25519ToX25519Keypair(serverPriv)

	ln := newTCPListener(t, serverPriv)
	runEchoServer(ln)

	clientConn, err := Dial("tcp", ln.Addr().String(), clientPriv, serverXPub)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	const N = 20
	for i := 0; i < N; i++ {
		want := []byte(fmt.Sprintf("message %d", i))
		if err := clientConn.SendFrame(want); err != nil {
			t.Fatalf("iter %d SendFrame: %v", i, err)
		}
		got, err := clientConn.RecvFrame()
		if err != nil {
			t.Fatalf("iter %d RecvFrame: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("iter %d: got %q, want %q", i, got, want)
		}
	}
}

// --- PeerX25519 identity assertion -------------------------------------------

func TestTCP_PeerX25519_ServerSeesClient(t *testing.T) {
	// After the handshake both sides see the other's X25519 static key.
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, serverXPub, _ := Ed25519ToX25519Keypair(serverPriv)
	_, clientXPub, _ := Ed25519ToX25519Keypair(clientPriv)

	ln := newTCPListener(t, serverPriv)
	acceptDone := make(chan *Conn, 1)
	go func() {
		c, _ := ln.Accept()
		acceptDone <- c
	}()

	clientConn, err := Dial("tcp", ln.Addr().String(), clientPriv, serverXPub)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	serverConn := <-acceptDone
	if serverConn == nil {
		t.Fatal("Accept returned nil conn")
	}
	defer serverConn.Close()

	// Server should see client's X25519 pubkey.
	if serverConn.PeerX25519 != clientXPub {
		t.Errorf("server.PeerX25519 = %x, want clientXPub %x", serverConn.PeerX25519, clientXPub)
	}
	// Client should see server's X25519 pubkey.
	if clientConn.PeerX25519 != serverXPub {
		t.Errorf("client.PeerX25519 = %x, want serverXPub %x", clientConn.PeerX25519, serverXPub)
	}
}

// --- Ed25519PublicToX25519 yields same PeerX25519 as Ed25519ToX25519Keypair -

func TestTCP_PeerX25519_MatchesBirationaMap(t *testing.T) {
	// Verify the PeerX25519 seen in the handshake equals what
	// Ed25519PublicToX25519 computes from the peer's Ed25519 public key.
	// This is the key invariant that lets a daemon authenticate a peer DID
	// from only the Noise handshake result.
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, serverXPub, _ := Ed25519ToX25519Keypair(serverPriv)

	ln := newTCPListener(t, serverPriv)
	acceptDone := make(chan *Conn, 1)
	go func() {
		c, _ := ln.Accept()
		acceptDone <- c
	}()

	clientConn, err := Dial("tcp", ln.Addr().String(), clientPriv, serverXPub)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()
	serverConn := <-acceptDone
	defer serverConn.Close()

	// Compute expected client X25519 pubkey from Ed25519 public key.
	clientEdPub := clientPriv.Public().(ed25519.PublicKey)
	expectedXPub, err := Ed25519PublicToX25519(clientEdPub)
	if err != nil {
		t.Fatalf("Ed25519PublicToX25519: %v", err)
	}
	if serverConn.PeerX25519 != expectedXPub {
		t.Errorf("server.PeerX25519 doesn't match Ed25519PublicToX25519 of client pub key:\n  got:  %x\n  want: %x",
			serverConn.PeerX25519, expectedXPub)
	}
}

// --- Wrong-key rejection -----------------------------------------------------

func TestTCP_DialWrongPubkeyFails(t *testing.T) {
	// If the dialing side supplies the wrong remotePub, the Noise
	// handshake fails — the initiator encrypts to the wrong key and
	// the responder cannot decrypt message 1.
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, wrongXPub, _ := Ed25519ToX25519Keypair(wrongPriv)

	ln := newTCPListener(t, serverPriv)
	// Accept errors are silently discarded by the server side; we only
	// care that Dial returns an error.
	go func() {
		c, _ := ln.Accept()
		if c != nil {
			c.Close()
		}
	}()

	_, err := Dial("tcp", ln.Addr().String(), clientPriv, wrongXPub)
	if err == nil {
		t.Error("expected error when dialing with wrong pubkey, got nil")
	}
}

// --- Closed-connection error --------------------------------------------------

func TestTCP_RecvAfterClose(t *testing.T) {
	_, serverPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, serverXPub, _ := Ed25519ToX25519Keypair(serverPriv)

	ln := newTCPListener(t, serverPriv)
	runEchoServer(ln)

	clientConn, err := Dial("tcp", ln.Addr().String(), clientPriv, serverXPub)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	clientConn.Close()
	if _, err := clientConn.RecvFrame(); err == nil {
		t.Error("RecvFrame after Close: expected error, got nil")
	}
}

// --- Addr returns a usable address -------------------------------------------

func TestTCPListener_Addr(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	ln := newTCPListener(t, priv)
	addr := ln.Addr()
	if addr == nil {
		t.Fatal("Addr() returned nil")
	}
	// The address must be dialable (loopback + real port).
	tc, err := net.Dial(addr.Network(), addr.String())
	if err != nil {
		t.Fatalf("raw Dial to listener addr %s: %v", addr, err)
	}
	tc.Close()
}
