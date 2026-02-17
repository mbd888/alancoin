// Package health provides a registry of named subsystem health checkers.
package health

import (
	"context"
	"sync"
)

// Status represents the health of a single subsystem.
type Status struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// Checker is a function that checks the health of a subsystem.
type Checker func(ctx context.Context) Status

// Registry holds named health checkers and runs them on demand.
type Registry struct {
	mu       sync.RWMutex
	checkers []namedChecker
}

type namedChecker struct {
	name  string
	check Checker
}

// NewRegistry creates a new health check registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a named health checker.
func (r *Registry) Register(name string, check Checker) {
	r.mu.Lock()
	r.checkers = append(r.checkers, namedChecker{name: name, check: check})
	r.mu.Unlock()
}

// CheckAll runs all registered checkers and returns the aggregate health
// status plus individual subsystem results.
func (r *Registry) CheckAll(ctx context.Context) (healthy bool, statuses []Status) {
	r.mu.RLock()
	checkers := make([]namedChecker, len(r.checkers))
	copy(checkers, r.checkers)
	r.mu.RUnlock()

	healthy = true
	statuses = make([]Status, len(checkers))

	for i, nc := range checkers {
		statuses[i] = nc.check(ctx)
		if !statuses[i].Healthy {
			healthy = false
		}
	}

	return healthy, statuses
}
