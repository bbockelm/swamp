package oauth2

import (
	"net"
	"sync"
	"time"
)

// registrationRateLimiter implements a per-IP token-bucket rate limiter
// for dynamic client registration requests.
type registrationRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens replenished per second
	burst   int     // maximum tokens (burst capacity)
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// newRegistrationRateLimiter creates a rate limiter.
// rate is tokens per second, burst is the maximum burst size.
func newRegistrationRateLimiter(rate float64, burst int) *registrationRateLimiter {
	return &registrationRateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		burst:   burst,
	}
}

// Allow checks whether the given IP is allowed to make a request.
func (rl *registrationRateLimiter) Allow(ip string) bool {
	key := normalizeIP(ip)
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		// First request from this IP — start with burst-1 tokens.
		rl.buckets[key] = &bucket{
			tokens:   float64(rl.burst) - 1,
			lastSeen: now,
		}
		return true
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Cleanup removes stale buckets older than maxAge to prevent unbounded growth.
func (rl *registrationRateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for ip, b := range rl.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(rl.buckets, ip)
		}
	}
}

// normalizeIP normalizes an IP address for rate limiting.
// IPv4 addresses are used as-is. IPv6 addresses are masked to /64
// to prevent bypass via address rotation within a single prefix.
func normalizeIP(raw string) string {
	// Strip port if present.
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return raw
	}

	if ip.To4() != nil {
		return ip.String()
	}

	// IPv6 — aggregate to /64.
	masked := ip.Mask(net.CIDRMask(64, 128))
	return masked.String() + "/64"
}
