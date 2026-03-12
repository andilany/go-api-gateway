package logstore

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides request log persistence backed by Postgres.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// Entry represents a single request log row to persist.
type Entry struct {
	RouteName       string
	Method          string
	Path            string
	StatusCode      int
	DurationMs      int64
	RequestHeaders  map[string]string
	ResponseHeaders map[string]string
}

// New creates a new Store using the given pgx pool and logger.
func New(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		pool: pool,
		log:  logger,
	}
}

// LogRequest inserts a single request log row into request_logs using a parameterized INSERT.
func (s *Store) LogRequest(ctx context.Context, e Entry) error {
	var (
		reqHeadersJSON  []byte
		respHeadersJSON []byte
		err             error
	)

	if e.RequestHeaders != nil {
		reqHeadersJSON, err = json.Marshal(e.RequestHeaders)
		if err != nil {
			s.log.Error("marshal request headers for logging", "err", err)
			reqHeadersJSON = nil
		}
	}
	if e.ResponseHeaders != nil {
		respHeadersJSON, err = json.Marshal(e.ResponseHeaders)
		if err != nil {
			s.log.Error("marshal response headers for logging", "err", err)
			respHeadersJSON = nil
		}
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO request_logs (route_name, method, path, status_code, duration_ms, request_headers, response_headers)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.RouteName,
		e.Method,
		e.Path,
		e.StatusCode,
		e.DurationMs,
		reqHeadersJSON,
		respHeadersJSON,
	)
	return err
}

var sensitiveHeaders = map[string]struct{}{
	"authorization":      {},
	"proxy-authorization": {},
	"cookie":             {},
	"set-cookie":         {},
	"x-api-key":          {},
	"x-api-key-id":       {},
	"x-auth-token":       {},
}

// SanitizeHeaders returns a map of header names to safe values for logging.
// Sensitive headers are redacted; others are joined with ", " if multiple values.
func SanitizeHeaders(h http.Header) map[string]string {
	if h == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(h))
	for name, values := range h {
		lower := strings.ToLower(name)
		if _, ok := sensitiveHeaders[lower]; ok {
			out[name] = "[REDACTED]"
			continue
		}
		out[name] = strings.Join(values, ", ")
	}
	return out
}

