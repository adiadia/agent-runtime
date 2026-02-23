// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
)

type rateLimitDecision struct {
	Allowed           bool
	LimitPerMinute    int
	Remaining         int
	RetryAfterSeconds int
}

type tokenBucket struct {
	capacity        float64
	tokens          float64
	refillPerSecond float64
	lastRefill      time.Time
}

type inMemoryRateLimiter struct {
	mu      sync.Mutex
	buckets map[uuid.UUID]*tokenBucket
}

func newInMemoryRateLimiter() *inMemoryRateLimiter {
	return &inMemoryRateLimiter{
		buckets: make(map[uuid.UUID]*tokenBucket, 32),
	}
}

func (l *inMemoryRateLimiter) Allow(apiKeyID uuid.UUID, limitPerMinute int, now time.Time) rateLimitDecision {
	if limitPerMinute <= 0 {
		limitPerMinute = 1
	}

	capacity := float64(limitPerMinute)
	refillPerSecond := capacity / 60.0

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[apiKeyID]
	if !ok || bucket.capacity != capacity {
		bucket = &tokenBucket{
			capacity:        capacity,
			tokens:          capacity,
			refillPerSecond: refillPerSecond,
			lastRefill:      now,
		}
		l.buckets[apiKeyID] = bucket
	}

	elapsedSeconds := now.Sub(bucket.lastRefill).Seconds()
	if elapsedSeconds > 0 {
		bucket.tokens += elapsedSeconds * bucket.refillPerSecond
		if bucket.tokens > bucket.capacity {
			bucket.tokens = bucket.capacity
		}
		bucket.lastRefill = now
	}

	decision := rateLimitDecision{
		Allowed:        false,
		LimitPerMinute: limitPerMinute,
		Remaining:      int(math.Floor(bucket.tokens)),
	}

	if bucket.tokens >= 1 {
		bucket.tokens -= 1
		decision.Allowed = true
		decision.Remaining = int(math.Floor(bucket.tokens))
		return decision
	}

	missingTokens := 1 - bucket.tokens
	waitSeconds := int(math.Ceil(missingTokens / bucket.refillPerSecond))
	if waitSeconds < 1 {
		waitSeconds = 1
	}
	decision.RetryAfterSeconds = waitSeconds
	return decision
}
