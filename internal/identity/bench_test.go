package identity

import (
	"path/filepath"
	"testing"
)

// Argon2id (64 MiB, 3 iterations, 4 parallel lanes) is the dominant
// cost of `daimon unlock`. Every keystore read pays it. The numbers
// here are the floor for how fast unlock CAN be on any given machine
// — actual unlock latency adds JSON parse + AES-GCM decrypt
// (microseconds, negligible) + socket round-trip + daemon spawn
// (variable). See docs/perf.md for the measured baseline.

func BenchmarkLoadKeystore(b *testing.B) {
	// Provision a keystore once; benchmark the load+decrypt path
	// which is what unlock actually exercises. Each iteration runs
	// the full Argon2id KDF (the expensive part) + AES-GCM open.
	id, err := Generate()
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	dir := b.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	password := []byte("benchmark-password")
	if err := id.SaveToKeystore(path, password); err != nil {
		b.Fatalf("SaveToKeystore: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadFromKeystore(path, password); err != nil {
			b.Fatalf("LoadFromKeystore: %v", err)
		}
	}
}

// BenchmarkRotatePassword exercises the full rotate path: decrypt
// under old (Argon2id + AES-GCM open) + re-encrypt under new
// (Argon2id + AES-GCM seal) + paranoia re-decrypt (Argon2id +
// AES-GCM open) + atomic rename. Three Argon2id calls per rotate.
//
// This is the cost a user pays for `daimon rotate-password` on the
// identity keystore. The wallet keystore adds another rotate of
// the same shape on top.
func BenchmarkRotatePassword(b *testing.B) {
	id, err := Generate()
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	dir := b.TempDir()
	path := filepath.Join(dir, "keys.encrypted")
	if err := id.SaveToKeystore(path, []byte("pw0")); err != nil {
		b.Fatalf("SaveToKeystore: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		old := []byte{'p', 'w', byte('0' + i%9)}
		new_ := []byte{'p', 'w', byte('0' + (i+1)%9)}
		if err := RotatePassword(path, old, new_); err != nil {
			b.Fatalf("RotatePassword iter %d: %v", i, err)
		}
	}
}
