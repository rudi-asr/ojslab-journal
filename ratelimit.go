package main

import (
	"sync"
	"time"
)

// rateLimiter - simple in-memory sliding/token-bucket per-key limiter.
type rateLimiter struct {
	mu    sync.Mutex
	limit int
	win   time.Duration
	hits  map[string][]time.Time
}

func newRateLimiter(limit int, win time.Duration) *rateLimiter {
	return &rateLimiter{
		limit: limit,
		win:   win,
		hits:  make(map[string][]time.Time),
	}
}

// Allow returns true if a call for key is within the limit.
// Old entries are pruned lazily to avoid unbounded growth.
func (rl *rateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.win)
	// prune this key and occasionally the whole map
	kept := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	rl.hits[key] = kept
	if len(rl.hits) > 10000 {
		for k, v := range rl.hits {
			nv := v[:0]
			for _, t := range v {
				if t.After(cutoff) {
					nv = append(nv, t)
				}
			}
			if len(nv) == 0 {
				delete(rl.hits, k)
			} else {
				rl.hits[k] = nv
			}
		}
	}
	if len(kept) >= rl.limit {
		return false
	}
	rl.hits[key] = append(rl.hits[key], now)
	return true
}