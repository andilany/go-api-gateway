package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter is an in-memory per-IP rate limiter using a sliding window.
// It limits requests per window duration; older entries are pruned.
type RateLimiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time
	limit   int
	window  time.Duration
	getIP   func(*http.Request) string
	pruneAt time.Time
}

// NewRateLimiter returns a rate limiter that allows limit requests per window per IP.
// getIP extracts the client IP from the request (e.g. X-Forwarded-For or RemoteAddr).
func NewRateLimiter(limit int, window time.Duration, getIP func(*http.Request) string) *RateLimiter {
	return &RateLimiter{
		hits:  make(map[string][]time.Time),
		limit: limit,
		window: window,
		getIP: getIP,
	}
}

func (rl *RateLimiter) prune(now time.Time) {
	if now.Before(rl.pruneAt) {
		return
	}
	rl.pruneAt = now.Add(rl.window / 4)
	cutoff := now.Add(-rl.window)
	for ip, ts := range rl.hits {
		i := 0
		for _, t := range ts {
			if t.After(cutoff) {
				ts[i] = t
				i++
			}
		}
		if i == 0 {
			delete(rl.hits, ip)
		} else {
			rl.hits[ip] = ts[:i]
		}
	}
}

// Allow reports whether the request from this IP is within the rate limit.
func (rl *RateLimiter) Allow(r *http.Request) bool {
	ip := rl.getIP(r)
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.prune(now)
	ts := rl.hits[ip]
	cutoff := now.Add(-rl.window)
	n := 0
	for _, t := range ts {
		if t.After(cutoff) {
			n++
		}
	}
	if n >= rl.limit {
		return false
	}
	rl.hits[ip] = append(ts, now)
	return true
}

// Middleware returns a chi-compatible middleware that returns 429 Too Many Requests
// when the client exceeds the limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(r) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetIP returns RemoteAddr, or the first X-Forwarded-For hop when present (no validation).
func GetIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return strings.TrimSpace(xff[:i])
			}
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// RateLimitByPath returns a middleware that applies apiLimiter when path has prefix apiPathPrefix, otherwise globalLimiter.
func RateLimitByPath(apiPathPrefix string, apiLimiter, globalLimiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var allow bool
			if strings.HasPrefix(r.URL.Path, apiPathPrefix) {
				allow = apiLimiter.Allow(r)
			} else {
				allow = globalLimiter.Allow(r)
			}
			if !allow {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
