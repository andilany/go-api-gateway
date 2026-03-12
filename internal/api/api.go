package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"gateway/internal/store"

	"github.com/go-chi/chi/v5"
)

// Handler holds dependencies for the config API.
type Handler struct {
	Store *store.Store
	Log   *slog.Logger
}

// RouteCreateUpdateBody is the request body for create and update (path or path_prefix, exactly one).
type RouteCreateUpdateBody struct {
	Name               string            `json:"name"`
	Path               string            `json:"path"`
	PathPrefix         string            `json:"path_prefix"`
	Upstream           string            `json:"upstream"`
	HeadersToForward   []string          `json:"headers_to_forward"`
	HeadersToSet       map[string]string `json:"headers_to_set"`
}

// GlobalHeaderConfigBody is the request/response body for global header config.
type GlobalHeaderConfigBody struct {
	HeadersToForward []string          `json:"headers_to_forward"`
	HeadersToSet     map[string]string `json:"headers_to_set"`
}

// Mount mounts the config API routes on r under /api.
func (h *Handler) Mount(r chi.Router) {
	r.Route("/api", func(r chi.Router) {
		r.Get("/routes", h.ListRoutes)
		r.Post("/routes", h.CreateRoute)
		r.Get("/routes/{id}", h.GetRoute)
		r.Put("/routes/{id}", h.UpdateRoute)
		r.Delete("/routes/{id}", h.DeleteRoute)
		r.Get("/config/headers", h.GetGlobalHeaderConfig)
		r.Put("/config/headers", h.SetGlobalHeaderConfig)
	})
}

func (h *Handler) ListRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.Store.ListRoutes(r.Context())
	if err != nil {
		h.Log.Error("list routes", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	if routes == nil {
		routes = []store.Route{}
	}
	writeJSON(w, http.StatusOK, routes)
}

func (h *Handler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	var body RouteCreateUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	pathPrefixValue, err := validatePathOrPathPrefix(body.Path, body.PathPrefix, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if body.Upstream == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upstream is required"})
		return
	}
	if err := validateURL(body.Upstream); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid upstream URL: " + err.Error()})
		return
	}
	if err := validateHeaderNames(body.HeadersToForward, body.HeadersToSet); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	headersToForward := body.HeadersToForward
	if headersToForward == nil {
		headersToForward = []string{}
	}
	headersToSet := body.HeadersToSet
	if headersToSet == nil {
		headersToSet = map[string]string{}
	}

	exists, err := h.Store.NameExists(r.Context(), body.Name, "")
	if err != nil {
		h.Log.Error("check name exists", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	if exists {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "route with this name already exists"})
		return
	}
	overlap, err := h.Store.PathPrefixOverlap(r.Context(), pathPrefixValue, "")
	if err != nil {
		h.Log.Error("check path overlap", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	if overlap {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "path or path_prefix overlaps with an existing route"})
		return
	}

	route, err := h.Store.CreateRoute(r.Context(), body.Name, pathPrefixValue, body.Upstream, headersToForward, headersToSet)
	if err != nil {
		h.Log.Error("create route", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}

	w.Header().Set("Location", "/api/routes/"+route.ID)
	writeJSON(w, http.StatusCreated, route)
}

func (h *Handler) GetRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	route, err := h.Store.GetRouteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		h.Log.Error("get route", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (h *Handler) UpdateRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var body RouteCreateUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	pathPrefixValue, err := validatePathOrPathPrefix(body.Path, body.PathPrefix, true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if body.Upstream == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upstream is required"})
		return
	}
	if err := validateURL(body.Upstream); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid upstream URL: " + err.Error()})
		return
	}
	if err := validateHeaderNames(body.HeadersToForward, body.HeadersToSet); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	headersToForward := body.HeadersToForward
	if headersToForward == nil {
		headersToForward = []string{}
	}
	headersToSet := body.HeadersToSet
	if headersToSet == nil {
		headersToSet = map[string]string{}
	}

	exists, err := h.Store.NameExists(r.Context(), body.Name, id)
	if err != nil {
		h.Log.Error("check name exists", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	if exists {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "route with this name already exists"})
		return
	}
	overlap, err := h.Store.PathPrefixOverlap(r.Context(), pathPrefixValue, id)
	if err != nil {
		h.Log.Error("check path overlap", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	if overlap {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "path or path_prefix overlaps with an existing route"})
		return
	}

	route, err := h.Store.UpdateRoute(r.Context(), id, body.Name, pathPrefixValue, body.Upstream, headersToForward, headersToSet)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		h.Log.Error("update route", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (h *Handler) DeleteRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	err := h.Store.DeleteRoute(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.Log.Error("delete route", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetGlobalHeaderConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.Store.GetGlobalHeaderConfig(r.Context())
	if err != nil {
		h.Log.Error("get global header config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	writeJSON(w, http.StatusOK, GlobalHeaderConfigBody{
		HeadersToForward: cfg.HeadersToForward,
		HeadersToSet:     cfg.HeadersToSet,
	})
}

func (h *Handler) SetGlobalHeaderConfig(w http.ResponseWriter, r *http.Request) {
	var body GlobalHeaderConfigBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := validateHeaderNames(body.HeadersToForward, body.HeadersToSet); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	headersToForward := body.HeadersToForward
	if headersToForward == nil {
		headersToForward = []string{}
	}
	headersToSet := body.HeadersToSet
	if headersToSet == nil {
		headersToSet = map[string]string{}
	}
	cfg, err := h.Store.SetGlobalHeaderConfig(r.Context(), headersToForward, headersToSet)
	if err != nil {
		h.Log.Error("set global header config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	writeJSON(w, http.StatusOK, GlobalHeaderConfigBody{
		HeadersToForward: cfg.HeadersToForward,
		HeadersToSet:     cfg.HeadersToSet,
	})
}

func validatePathOrPathPrefix(path, pathPrefix string, required bool) (string, error) {
	hasPath := strings.TrimSpace(path) != ""
	hasPrefix := strings.TrimSpace(pathPrefix) != ""
	if hasPath && hasPrefix {
		return "", errors.New("exactly one of path or path_prefix must be set")
	}
	if !hasPath && !hasPrefix {
		if required {
			return "", errors.New("path or path_prefix is required")
		}
		return "", nil
	}
	if hasPath {
		return strings.TrimSpace(path), nil
	}
	return strings.TrimSpace(pathPrefix), nil
}

func validateURL(s string) error {
	if s == "" {
		return errors.New("empty")
	}
	u, err := url.ParseRequestURI(s)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if strings.TrimSpace(u.Host) == "" {
		return errors.New("host is required")
	}
	return nil
}

func validateHeaderNames(forward []string, set map[string]string) error {
	for _, n := range forward {
		if err := validHeaderName(n); err != nil {
			return err
		}
	}
	for k := range set {
		if err := validHeaderName(k); err != nil {
			return err
		}
	}
	return nil
}

func validHeaderName(name string) error {
	if name == "" {
		return errors.New("header name cannot be empty")
	}
	if strings.ContainsAny(name, "\r\n") {
		return errors.New("header name contains invalid characters")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
