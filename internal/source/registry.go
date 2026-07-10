package source

import (
	"fmt"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

func (r *Registry) New(cfg Config) (Source, error) {
	r.mu.RLock()
	factory := r.factories[cfg.Name]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("source %q is not registered", cfg.Name)
	}
	return factory(cfg)
}
