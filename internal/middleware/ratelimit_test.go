package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute, func(r *http.Request) string { return "1.2.3.4" })
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if !rl.Allow(req) { t.Error("first request should be allowed") }
	if !rl.Allow(req) { t.Error("second request should be allowed") }
	if rl.Allow(req) { t.Error("third request should be denied") }
}

func TestRateLimiter_Middleware(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute, func(r *http.Request) string { return "ip1" })
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := rl.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != 200 {
		t.Errorf("first request: got status %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: got status %d, want 429", rec2.Code)
	}
}

func TestRateLimitByPath_usesApiLimiterForApi(t *testing.T) {
	apiLimiter := NewRateLimiter(1, time.Minute, func(r *http.Request) string { return "ip" })
	globalLimiter := NewRateLimiter(100, time.Minute, func(r *http.Request) string { return "ip" })
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := RateLimitByPath("/api", apiLimiter, globalLimiter)(next)

	req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != 200 {
		t.Errorf("first /api request: got %d, want 200", rec1.Code)
	}
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second /api request: got %d, want 429", rec2.Code)
	}
}

func TestRateLimitByPath_usesGlobalLimiterForNonApi(t *testing.T) {
	apiLimiter := NewRateLimiter(1, time.Minute, func(r *http.Request) string { return "ip" })
	globalLimiter := NewRateLimiter(2, time.Minute, func(r *http.Request) string { return "ip" })
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := RateLimitByPath("/api", apiLimiter, globalLimiter)(next)

	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("request %d to /foo: got %d, want 200", i+1, rec.Code)
		}
	}
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusTooManyRequests {
		t.Errorf("third /foo request: got %d, want 429", rec3.Code)
	}
}
