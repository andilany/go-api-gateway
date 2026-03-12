package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS_setsAllowedOriginOnly(t *testing.T) {
	const origin = "https://app.example.com"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := CORS(origin)(next)

	tests := []struct {
		name           string
		originHeader   string
		wantAllowOrigin string
		wantStatus     int
	}{
		{"matching origin", origin, origin, 200},
		{"no origin header", "", "", 200},
		{"different origin", "https://evil.com", "", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.originHeader != "" {
				req.Header.Set("Origin", tt.originHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllowOrigin {
				t.Errorf("Allow-Origin: got %q, want %q", got, tt.wantAllowOrigin)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status: got %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestCORS_optionsReturns204(t *testing.T) {
	handler := CORS("https://app.example.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("next should not be called") }))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status: got %d, want 204", rec.Code)
	}
}
