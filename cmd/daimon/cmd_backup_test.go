package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- buildTarball / extractTarball round-trip -------------------------------

func TestBuildTarball_IncludesPresentFiles(t *testing.T) {
	home := t.TempDir()
	mustWriteFileBackup(t, filepath.Join(home, "identity.keystore"), []byte("fake-id"))
	mustWriteFileBackup(t, filepath.Join(home, "memory.db"), []byte("fake-mem"))
	// wallet.keystore + activity.log deliberately absent — should be skipped silently.

	body, included, err := buildTarball(home)
	if err != nil {
		t.Fatalf("buildTarball: %v", err)
	}
	if got, want := len(included), 2; got != want {
		t.Errorf("included count: got %d, want %d (got=%v)", got, want, included)
	}
	if !containsString(included, "identity.keystore") || !containsString(included, "memory.db") {
		t.Errorf("included missing expected files: got %v", included)
	}
	if containsString(included, "wallet.keystore") || containsString(included, "activity.log") {
		t.Errorf("included contains absent files: got %v", included)
	}

	// Tarball must be a valid gzipped tar that we can read back.
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	tr := tar.NewReader(gz)
	got := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		buf, _ := io.ReadAll(tr)
		got[hdr.Name] = buf
	}
	if string(got["identity.keystore"]) != "fake-id" {
		t.Errorf("identity.keystore content roundtrip mismatch")
	}
	if string(got["memory.db"]) != "fake-mem" {
		t.Errorf("memory.db content roundtrip mismatch")
	}
}

func TestBuildTarball_RejectsNonRegularFile(t *testing.T) {
	home := t.TempDir()
	// Make identity.keystore a directory instead of a file.
	if err := os.Mkdir(filepath.Join(home, "identity.keystore"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, err := buildTarball(home); err == nil {
		t.Error("buildTarball: expected error for directory in place of regular file")
	}
}

func TestExtractTarball_RoundTrip(t *testing.T) {
	home := t.TempDir()
	mustWriteFileBackup(t, filepath.Join(home, "identity.keystore"), []byte("alpha"))
	mustWriteFileBackup(t, filepath.Join(home, "activity.log"), []byte("beta"))
	tarball, _, err := buildTarball(home)
	if err != nil {
		t.Fatalf("buildTarball: %v", err)
	}

	dst := t.TempDir()
	included, err := extractTarball(tarball, dst)
	if err != nil {
		t.Fatalf("extractTarball: %v", err)
	}
	if got, want := len(included), 2; got != want {
		t.Errorf("extracted count: got %d, want %d", got, want)
	}
	if body, err := os.ReadFile(filepath.Join(dst, "identity.keystore")); err != nil || string(body) != "alpha" {
		t.Errorf("identity.keystore: got body=%q err=%v, want body=alpha", body, err)
	}
	if body, err := os.ReadFile(filepath.Join(dst, "activity.log")); err != nil || string(body) != "beta" {
		t.Errorf("activity.log: got body=%q err=%v, want body=beta", body, err)
	}
}

func TestExtractTarball_RejectsPathTraversal(t *testing.T) {
	// Construct a malicious tarball by hand — bare name in buildTarball
	// can't produce a traversal, but a hostile actor could craft one.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("evil")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escape.txt",
		Mode: 0600,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	tw.Close()
	gz.Close()

	dst := t.TempDir()
	if _, err := extractTarball(buf.Bytes(), dst); err == nil {
		t.Error("extractTarball: expected error for path-traversal entry, got nil")
	}
}

func TestExtractTarball_RejectsUnexpectedFile(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("rogue")
	_ = tw.WriteHeader(&tar.Header{Name: "not-in-allowlist.txt", Mode: 0600, Size: int64(len(body))})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()

	dst := t.TempDir()
	if _, err := extractTarball(buf.Bytes(), dst); err == nil {
		t.Error("extractTarball: expected error for file outside backupFiles allowlist")
	}
}

// --- Encrypted backup round-trip --------------------------------------------

func TestEncryptedBackup_RoundTrip(t *testing.T) {
	tarball := []byte("hello daimon")
	passphrase := []byte("super-secret")

	enc, err := encodeEncryptedBackup(tarball, passphrase)
	if err != nil {
		t.Fatalf("encodeEncryptedBackup: %v", err)
	}
	if !bytes.HasPrefix(enc, []byte(backupMagicLine)) {
		t.Error("encoded backup missing magic header")
	}
	if !bytes.HasPrefix(enc[len(backupMagicLine):], []byte(backupModeEnc)) {
		t.Error("encoded backup missing encrypted mode marker")
	}

	got, err := decryptBackup(enc, passphrase)
	if err != nil {
		t.Fatalf("decryptBackup: %v", err)
	}
	if !bytes.Equal(got, tarball) {
		t.Errorf("roundtrip mismatch: got %q, want %q", got, tarball)
	}
}

func TestEncryptedBackup_WrongPassphrase(t *testing.T) {
	enc, err := encodeEncryptedBackup([]byte("ciphertext"), []byte("right"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := decryptBackup(enc, []byte("WRONG")); err == nil {
		t.Error("decryptBackup with wrong passphrase: expected error, got nil")
	} else if !strings.Contains(err.Error(), "wrong passphrase or corrupted backup") {
		t.Errorf("decryptBackup wrong passphrase: got %q, want %q substring",
			err.Error(), "wrong passphrase or corrupted backup")
	}
}

func TestEncryptedBackup_TruncatedFile(t *testing.T) {
	enc, _ := encodeEncryptedBackup([]byte("x"), []byte("p"))
	// Truncate to just past the magic + mode markers, lose the salt/nonce/ct.
	short := enc[:len(backupMagicLine)+len(backupModeEnc)+5]
	if _, err := decryptBackup(short, []byte("p")); err == nil {
		t.Error("decryptBackup on truncated file: expected error")
	}
}

// --- decodeBackup mode-detection --------------------------------------------

func TestDecodeBackup_RejectsBadMagic(t *testing.T) {
	if _, _, err := decodeBackup([]byte("PKZIPv1\n...")); err == nil {
		t.Error("decodeBackup on non-daimon-magic file: expected error")
	}
}

func TestDecodeBackup_RejectsUnknownMode(t *testing.T) {
	bad := []byte(backupMagicLine + "what-is-this\n...")
	if _, _, err := decodeBackup(bad); err == nil {
		t.Error("decodeBackup with unknown mode: expected error")
	}
}

func TestDecodeBackup_PlainMode(t *testing.T) {
	plain := encodePlainBackup([]byte("inner"))
	tb, mode, err := decodeBackup(plain)
	if err != nil {
		t.Fatalf("decodeBackup: %v", err)
	}
	if mode != "plain" {
		t.Errorf("mode = %q, want plain", mode)
	}
	if !bytes.Equal(tb, []byte("inner")) {
		t.Errorf("tarball roundtrip: got %q, want %q", tb, "inner")
	}
}

func TestDecodeBackup_EncryptedModeReportsCorrectly(t *testing.T) {
	enc, _ := encodeEncryptedBackup([]byte("x"), []byte("p"))
	_, mode, err := decodeBackup(enc)
	if err != nil {
		t.Fatalf("decodeBackup: %v", err)
	}
	if mode != "encrypted" {
		t.Errorf("mode = %q, want encrypted", mode)
	}
}

// --- walkTarball (dry-run mode under restore --dry-run) ---------------------

func TestWalkTarball_HappyPath(t *testing.T) {
	// Build a valid tarball via buildTarball, then walk it. Should
	// report all included files with correct sizes.
	home := t.TempDir()
	mustWriteFileBackup(t, filepath.Join(home, "identity.keystore"), []byte("alpha"))
	mustWriteFileBackup(t, filepath.Join(home, "memory.db"), bytes.Repeat([]byte("x"), 100))

	tarball, _, err := buildTarball(home)
	if err != nil {
		t.Fatalf("buildTarball: %v", err)
	}

	entries, err := walkTarball(tarball)
	if err != nil {
		t.Fatalf("walkTarball: %v", err)
	}
	if got, want := len(entries), 2; got != want {
		t.Fatalf("entry count: got %d, want %d", got, want)
	}
	// Order isn't guaranteed by tarball (matches backupFiles order in
	// buildTarball, but the test shouldn't depend on that). Build a map.
	byName := map[string]int64{}
	for _, e := range entries {
		byName[e.name] = e.size
	}
	if byName["identity.keystore"] != 5 {
		t.Errorf("identity.keystore size: got %d, want 5", byName["identity.keystore"])
	}
	if byName["memory.db"] != 100 {
		t.Errorf("memory.db size: got %d, want 100", byName["memory.db"])
	}
}

func TestWalkTarball_RejectsPathTraversal(t *testing.T) {
	// Same defense-in-depth check extractTarball does. A tarball that
	// would be rejected at extract time MUST also be rejected at
	// dry-run, so users can't be tricked into believing a malicious
	// backup is safe by running dry-run first.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("evil")
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0600, Size: int64(len(body))})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()

	if _, err := walkTarball(buf.Bytes()); err == nil {
		t.Error("walkTarball: expected error for path-traversal entry, got nil")
	}
}

func TestWalkTarball_RejectsUnexpectedFile(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("rogue")
	_ = tw.WriteHeader(&tar.Header{Name: "not-in-allowlist.txt", Mode: 0600, Size: int64(len(body))})
	_, _ = tw.Write(body)
	tw.Close()
	gz.Close()

	if _, err := walkTarball(buf.Bytes()); err == nil {
		t.Error("walkTarball: expected error for file outside backupFiles allowlist")
	}
}

func TestWalkTarball_RejectsCorruptGzip(t *testing.T) {
	// Random bytes that don't form a valid gzip stream. walkTarball
	// should error at gzip.NewReader (which validates the magic + flags
	// up front) or at the first read attempt.
	if _, err := walkTarball([]byte("not a gzip stream")); err == nil {
		t.Error("walkTarball: expected error for non-gzip input, got nil")
	}
}

// --- Helpers ----------------------------------------------------------------

func mustWriteFileBackup(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func containsString(s []string, want string) bool {
	for _, x := range s {
		if x == want {
			return true
		}
	}
	return false
}
