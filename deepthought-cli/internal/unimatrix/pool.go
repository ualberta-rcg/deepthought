package unimatrix

import (
	"sync"
	"time"

	"deepthought-cli/internal/babel"
)

// Pool hands out one shared *babel.Client per provider. The chat loop, the
// summary engine, and future role clients all draw from the same pool so a
// provider's connection settings live in exactly one place. Clients are built
// lazily on first use and are safe for concurrent use (babel.Client is
// immutable after construction).
//
// The Pool takes connection fields as primitives rather than the config
// package's Provider type so unimatrix never imports config (config imports
// unimatrix — the other direction would cycle).
type Pool struct {
	mu      sync.Mutex
	clients map[string]*babel.Client
}

// NewPool builds an empty pool.
func NewPool() *Pool {
	return &Pool{clients: make(map[string]*babel.Client)}
}

// Client returns the pooled client for the named provider, building it on
// first use. wire is reserved for the future Anthropic adapter — today every
// provider is served by the OpenAI-compatible client.
func (p *Pool) Client(name, baseURL, apiKey, wire string, timeout ...time.Duration) *babel.Client {
	wait := 6 * time.Minute
	if len(timeout) > 0 {
		wait = timeout[0]
	}
	return p.ClientFor(name, baseURL, apiKey, wire, false, wait)
}

func (p *Pool) ClientFor(name, baseURL, apiKey, wire string, anonymous bool, timeout ...time.Duration) *babel.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	if c, ok := p.clients[name]; ok {
		return c
	}
	wait := 6 * time.Minute
	if len(timeout) > 0 && timeout[0] > 0 {
		wait = timeout[0]
	}
	c := babel.NewClientWithOptions(baseURL, apiKey, wire, wait)
	c.AllowAnonymous = anonymous
	p.clients[name] = c
	return c
}

// Drop evicts a provider's client (after its URL or key changes); the next
// Client call rebuilds it.
func (p *Pool) Drop(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.clients, name)
}
