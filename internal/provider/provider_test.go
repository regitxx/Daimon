package provider_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/provider"
)

// stubProvider is a minimal Provider implementation for registry tests. It
// records the last Invoke request so tests can assert routing without going
// through HTTP.
type stubProvider struct {
	name    string
	models  []provider.Model
	lastReq provider.Request
	resp    *provider.Response
	err     error
}

func (s *stubProvider) Name() string             { return s.name }
func (s *stubProvider) Models() []provider.Model { return s.models }
func (s *stubProvider) Invoke(_ context.Context, req provider.Request) (*provider.Response, error) {
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

// --- Registry --------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := provider.NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("new registry should be empty, got len=%d", r.Len())
	}

	a := &stubProvider{name: "alpha"}
	b := &stubProvider{name: "beta"}
	r.Register(a)
	r.Register(b)

	if r.Len() != 2 {
		t.Fatalf("expected 2 providers, got %d", r.Len())
	}
	got, err := r.Get("alpha")
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	if got.Name() != "alpha" {
		t.Fatalf("got %q, want alpha", got.Name())
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := provider.NewRegistry()
	_, err := r.Get("nope")
	if !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestRegistry_RegisterReplaces(t *testing.T) {
	r := provider.NewRegistry()
	a1 := &stubProvider{name: "alpha", models: []provider.Model{{ID: "v1"}}}
	a2 := &stubProvider{name: "alpha", models: []provider.Model{{ID: "v2"}}}
	r.Register(a1)
	r.Register(a2)
	got, _ := r.Get("alpha")
	if got.Models()[0].ID != "v2" {
		t.Fatalf("expected replacement, got models=%v", got.Models())
	}
	if r.Len() != 1 {
		t.Fatalf("replacement should not duplicate; got len=%d", r.Len())
	}
}

func TestRegistry_ListSortedByName(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&stubProvider{name: "gamma"})
	r.Register(&stubProvider{name: "alpha"})
	r.Register(&stubProvider{name: "beta"})
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("expected 3, got %d", len(list))
	}
	want := []string{"alpha", "beta", "gamma"}
	for i, p := range list {
		if p.Name() != want[i] {
			t.Fatalf("position %d: got %q, want %q", i, p.Name(), want[i])
		}
	}
}

// --- CredentialStore -------------------------------------------------------

func TestCredentialStore_SetGetHasNamesDelete(t *testing.T) {
	c := provider.NewCredentialStore()
	if c.Has("claude") {
		t.Fatal("empty store should not have any provider")
	}
	c.Set("claude", "sk-ant-xxx")
	c.Set("openai", "sk-yyy")

	if !c.Has("claude") {
		t.Fatal("Has after Set should be true")
	}
	got, err := c.Get("claude")
	if err != nil {
		t.Fatalf("Get claude: %v", err)
	}
	if got != "sk-ant-xxx" {
		t.Fatalf("got %q, want sk-ant-xxx", got)
	}

	names := c.Names()
	if len(names) != 2 || names[0] != "claude" || names[1] != "openai" {
		t.Fatalf("Names sorted: got %v", names)
	}

	c.Delete("openai")
	if c.Has("openai") {
		t.Fatal("Delete should remove the entry")
	}
	c.Delete("nonexistent") // no-op, no panic
}

func TestCredentialStore_GetNotFound(t *testing.T) {
	c := provider.NewCredentialStore()
	_, err := c.Get("nope")
	if !errors.Is(err, provider.ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound, got %v", err)
	}
}

func TestCredentialStore_RoundtripDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json.encrypted")
	password := []byte("correct horse battery staple")

	orig := provider.NewCredentialStore()
	orig.Set("claude", "sk-ant-xxx")
	orig.Set("openai", "sk-yyy")
	if err := orig.Save(path, password); err != nil {
		t.Fatalf("save: %v", err)
	}

	// File mode 0600 per SPEC §10.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: got %v, want 0600", info.Mode().Perm())
	}

	loaded, err := provider.LoadCredentialStore(path, password)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := loaded.Get("claude")
	if err != nil {
		t.Fatalf("get after load: %v", err)
	}
	if got != "sk-ant-xxx" {
		t.Fatalf("after roundtrip: got %q, want sk-ant-xxx", got)
	}
	if loaded.Names()[0] != "claude" || loaded.Names()[1] != "openai" {
		t.Fatalf("names after roundtrip: %v", loaded.Names())
	}
}

func TestCredentialStore_LoadMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.encrypted")
	c, err := provider.LoadCredentialStore(path, []byte("any"))
	if err != nil {
		t.Fatalf("load missing file should succeed, got %v", err)
	}
	if len(c.Names()) != 0 {
		t.Fatalf("missing-file store should be empty, got %v", c.Names())
	}
	// Set + save should still work — first-run flow.
	c.Set("claude", "sk")
	if err := c.Save(path, []byte("any")); err != nil {
		t.Fatalf("save after first-run load: %v", err)
	}
}

func TestCredentialStore_WrongPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.encrypted")
	c := provider.NewCredentialStore()
	c.Set("claude", "secret")
	if err := c.Save(path, []byte("right")); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := provider.LoadCredentialStore(path, []byte("wrong"))
	if !errors.Is(err, provider.ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestCredentialStore_TamperedCiphertext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.encrypted")
	c := provider.NewCredentialStore()
	c.Set("claude", "secret")
	if err := c.Save(path, []byte("pw")); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Tamper the ciphertext field by string-replacing inside the JSON.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Flip a base64 byte in the ciphertext field. The simplest reliable way
	// is to change one character of the file's body in a region that is
	// definitely the ciphertext — use a marker.
	mutated := []byte(strings.Replace(string(raw), `"ciphertext": "`, `"ciphertext": "A`, 1))
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	_, err = provider.LoadCredentialStore(path, []byte("pw"))
	if err == nil {
		t.Fatal("tampered ciphertext should not decrypt")
	}
}

func TestCredentialStore_UnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.encrypted")
	if err := os.WriteFile(path, []byte(`{"version":99,"kdf":"argon2id","cipher":"aes-256-gcm"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := provider.LoadCredentialStore(path, []byte("pw"))
	if !errors.Is(err, provider.ErrUnsupportedFormat) {
		t.Fatalf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestCredentialStore_SetReplaces(t *testing.T) {
	c := provider.NewCredentialStore()
	c.Set("claude", "v1")
	c.Set("claude", "v2")
	got, _ := c.Get("claude")
	if got != "v2" {
		t.Fatalf("Set should replace; got %q", got)
	}
}
