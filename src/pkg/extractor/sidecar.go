package extractor

import (
	"fmt"
	"sync"
	"time"
)

type CircuitBreaker struct {
	mu           sync.RWMutex
	failures     int
	threshold    int
	openUntil    time.Time
	openDuration time.Duration
}

func NewCircuitBreaker(threshold int, openDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:    threshold,
		openDuration: openDuration,
	}
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if !cb.openUntil.IsZero() && time.Now().Before(cb.openUntil) {
		return false
	}
	return true
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	if cb.failures >= cb.threshold {
		cb.openUntil = time.Now().Add(cb.openDuration)
		fmt.Printf("Circuit breaker OPEN until %v\n", cb.openUntil)
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.openUntil = time.Time{}
}
