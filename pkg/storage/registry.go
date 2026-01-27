package storage

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	backends map[string]StorageBackend
	mu       sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		backends: make(map[string]StorageBackend),
	}
}

func (r *Registry) Add(backend StorageBackend) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info := backend.Info()

	if _, exists := r.backends[info.Name]; exists {
		return fmt.Errorf("backend already exists: %s", info.Name)
	}

	r.backends[info.Name] = backend
	return nil
}

func (r *Registry) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.backends[name]; !exists {
		return fmt.Errorf("backend not found: %s", name)
	}

	delete(r.backends, name)
	return nil
}

func (r *Registry) Get(name string) (StorageBackend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	backend, exists := r.backends[name]
	if !exists {
		return nil, fmt.Errorf("backend not found: %s", name)
	}

	return backend, nil
}

func (r *Registry) List() []StorageBackend {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []StorageBackend
	for _, backend := range r.backends {
		list = append(list, backend)
	}

	// Sort by priority (lower = first)
	sort.Slice(list, func(i, j int) bool {
		return list[i].Info().Priority < list[j].Info().Priority
	})

	return list
}

func (r *Registry) ListByType(backendType BackendType) []StorageBackend {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []StorageBackend
	for _, backend := range r.backends {
		if backend.Info().Type == backendType {
			list = append(list, backend)
		}
	}

	return list
}

func (r *Registry) ListOnline() []StorageBackend {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []StorageBackend
	for _, backend := range r.backends {
		if backend.Info().Status == StatusOnline {
			list = append(list, backend)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Info().Priority < list[j].Info().Priority
	})

	return list
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.backends)
}

func (r *Registry) GetNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var names []string
	for name := range r.backends {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

func (r *Registry) HealthCheck() map[string]error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	unhealthy := make(map[string]error)

	for name, backend := range r.backends {
		if err := backend.Health(); err != nil {
			unhealthy[name] = err
		}
	}

	return unhealthy
}
