package provider

import (
	"errors"
	"sort"
	"sync"
)

// ErrProviderNotFound is returned by Registry.Get when no provider with the
// requested name is registered.
var ErrProviderNotFound = errors.New("provider: not found")

// Registry holds the set of provider adapters wired into a daimon. It is
// safe for concurrent use.
//
// Registry is the daimon's runtime view of "what LLMs can I talk to?". It
// does not own credentials (CredentialStore does) or call policy (the RPC
// handler does); it is purely a name → adapter lookup.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds p under p.Name(). A second Register with the same name
// replaces the previous registration — the daimon owns the provider table
// and can swap implementations at will (e.g. for tests).
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// Get returns the provider with name, or ErrProviderNotFound.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, ErrProviderNotFound
	}
	return p, nil
}

// List returns all registered providers, ordered by name for determinism.
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Len returns the number of registered providers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers)
}
