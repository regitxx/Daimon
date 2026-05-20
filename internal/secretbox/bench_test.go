package secretbox

import (
	"crypto/rand"
	"testing"
)

// AES-256-GCM seal + open are the inner loop of memory-row encryption
// (every row writes one seal + every row read does one open) and
// activity-log payload encryption (same pattern, smaller plaintexts).
// They're also the inner loop of identity keystore + wallet keystore
// load — but those Argon2id-dominated paths are benchmarked
// separately in internal/identity/bench_test.go.
//
// At v0.2 scale, AEAD throughput is not a bottleneck — these
// benchmarks exist primarily to catch a regression (e.g. accidentally
// recreating the AEAD cipher per call instead of caching it).

func BenchmarkSealAAD_Memory(b *testing.B) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	// 256-byte plaintext models a typical memory-row content field.
	plaintext := make([]byte, 256)
	_, _ = rand.Read(plaintext)
	aad := []byte("daimon-memory-row-v1\x00row-id\x00content")

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := SealAAD(key, plaintext, aad); err != nil {
			b.Fatalf("SealAAD: %v", err)
		}
	}
}

func BenchmarkOpenAAD_Memory(b *testing.B) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	plaintext := make([]byte, 256)
	_, _ = rand.Read(plaintext)
	aad := []byte("daimon-memory-row-v1\x00row-id\x00content")
	blob, err := SealAAD(key, plaintext, aad)
	if err != nil {
		b.Fatalf("SealAAD: %v", err)
	}

	b.SetBytes(int64(len(plaintext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := OpenAAD(key, blob, aad); err != nil {
			b.Fatalf("OpenAAD: %v", err)
		}
	}
}
