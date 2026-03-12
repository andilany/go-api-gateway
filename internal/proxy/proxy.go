package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"gateway/internal/logstore"
	"gateway/internal/store"
)

// Handler implements the reverse proxy: route matching, upstream request, header handling, and logging.
type Handler struct {
	Store *store.Store
	Logs  *logstore.Store
	Log   *slog.Logger
	// Client is the HTTP client used for upstream requests. If nil, a default client with timeout is used.
	Client *http.Client
}

// Mount configures the router so that all non-/api requests are handled by the proxy.
// It should be called after config API routes are mounted.
func (h *Handler) Mount(r interface{ NotFound(http.HandlerFunc) }) {
	r.NotFound(h.ServeHTTP)
}

func (h *Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{
		Timeout: 30 * time.Second,
	}
}

// ServeHTTP implements http.Handler for proxying requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger := h.Log
	if logger == nil {
		logger = slog.Default()
	}

	ctx := r.Context()

	routes, err := h.Store.ListRoutes(ctx)
	if err != nil {
		logger.Error("list routes for proxy", "err", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	route, ok := matchRoute(routes, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	globalHeaders, err := h.Store.GetGlobalHeaderConfig(ctx)
	if err != nil {
		logger.Error("get global header config for proxy", "err", err)
		globalHeaders = store.GlobalHeaderConfig{
			HeadersToForward: []string{},
			HeadersToSet:     map[string]string{},
		}
	}

	upstreamURL := buildUpstreamURL(route.Upstream, r.URL.Path, r.URL.RawQuery)

	upstreamReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
	if err != nil {
		status := http.StatusBadGateway
		http.Error(w, http.StatusText(status), status)
		h.logRequest(ctx, logger, route.Name, r, nil, status, start)
		return
	}

	applyHeaders(upstreamReq, r, globalHeaders, route)

	resp, err := h.client().Do(upstreamReq)

	var status int
	if err != nil {
		status = classifyUpstreamError(err)
		http.Error(w, http.StatusText(status), status)
		h.logRequest(ctx, logger, route.Name, r, nil, status, start)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status = resp.StatusCode
	w.WriteHeader(status)
	_, _ = io.Copy(w, resp.Body)

	h.logRequest(ctx, logger, route.Name, r, resp, status, start)
}

func (h *Handler) logRequest(ctx context.Context, logger *slog.Logger, routeName string, r *http.Request, resp *http.Response, status int, start time.Time) {
	durationMs := time.Since(start).Milliseconds()

	reqHeaders := logstore.SanitizeHeaders(r.Header)
	var respHeaders map[string]string
	if resp != nil {
		respHeaders = logstore.SanitizeHeaders(resp.Header)
	}

	entry := logstore.Entry{
		RouteName:       routeName,
		Method:          r.Method,
		Path:            r.URL.Path,
		StatusCode:      status,
		DurationMs:      durationMs,
		RequestHeaders:  reqHeaders,
		ResponseHeaders: respHeaders,
	}

	if err := h.Logs.LogRequest(ctx, entry); err != nil {
		logger.Error("log proxied request", "err", err)
	}
}

// matchRoute selects the best matching route for the given path following longest-prefix and exact-match rules.
func matchRoute(routes []store.Route, path string) (store.Route, bool) {
	var (
		selected     store.Route
		hasSelected  bool
		bestLen      int
		bestIsExact  bool
		bestName     string
	)

	for _, r := range routes {
		var (
			matched  bool
			length   int
			isExact  bool
		)

		if r.Path != "" && path == r.Path {
			matched = true
			length = len(r.Path)
			isExact = true
		} else if r.PathPrefix != "" && strings.HasPrefix(path, r.PathPrefix) {
			matched = true
			length = len(r.PathPrefix)
			isExact = false
		}

		if !matched {
			continue
		}

		if !hasSelected ||
			length > bestLen ||
			(length == bestLen && isExact && !bestIsExact) ||
			(length == bestLen && isExact == bestIsExact && r.Name < bestName) {
			selected = r
			hasSelected = true
			bestLen = length
			bestIsExact = isExact
			bestName = r.Name
		}
	}

	return selected, hasSelected
}

func buildUpstreamURL(upstreamBase, path, rawQuery string) string {
	u := upstreamBase + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

func applyHeaders(upstreamReq *http.Request, original *http.Request, global store.GlobalHeaderConfig, route store.Route) {
	forwardSet := make(map[string]struct{})
	for _, h := range global.HeadersToForward {
		forwardSet[strings.ToLower(h)] = struct{}{}
	}
	for _, h := range route.HeadersToForward {
		forwardSet[strings.ToLower(h)] = struct{}{}
	}

	for name, values := range original.Header {
		if _, ok := forwardSet[strings.ToLower(name)]; !ok {
			continue
		}
		for _, v := range values {
			upstreamReq.Header.Add(name, v)
		}
	}

	effectiveSet := make(map[string]string)
	for k, v := range global.HeadersToSet {
		effectiveSet[http.CanonicalHeaderKey(k)] = v
	}
	for k, v := range route.HeadersToSet {
		effectiveSet[http.CanonicalHeaderKey(k)] = v
	}

	for k, v := range effectiveSet {
		upstreamReq.Header.Set(k, v)
	}
}

func classifyUpstreamError(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

