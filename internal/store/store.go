package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Route is the in-memory route shape. path_prefix is the single DB column;
// Path and PathPrefix are mutually exclusive in API: Path is exact match (stored as path_prefix).
type Route struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Path               string            `json:"path,omitempty"`        // exact match (set when path_prefix has no trailing slash)
	PathPrefix         string            `json:"path_prefix,omitempty"` // prefix match
	Upstream           string            `json:"upstream"`
	HeadersToForward   []string          `json:"headers_to_forward,omitempty"`
	HeadersToSet       map[string]string `json:"headers_to_set,omitempty"`
}

// GlobalHeaderConfig is the global default header config.
type GlobalHeaderConfig struct {
	HeadersToForward []string          `json:"headers_to_forward"`
	HeadersToSet     map[string]string `json:"headers_to_set"`
}

// Store provides route and global header config persistence.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store using the given pgx pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ListRoutes returns all routes. Order is by name.
func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, path_prefix, upstream, headers_to_forward, headers_to_set
		 FROM routes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var routes []Route
	for rows.Next() {
		var r Route
		var id, name, pathPrefix, upstream string
		var headersForward []string
		var headersSet []byte
		if err := rows.Scan(&id, &name, &pathPrefix, &upstream, &headersForward, &headersSet); err != nil {
			return nil, err
		}
		r.ID = id
		r.Name = name
		r.Upstream = upstream
		r.HeadersToForward = headersForward
		if len(headersForward) == 0 {
			r.HeadersToForward = []string{}
		}
		if len(headersSet) > 0 {
			_ = json.Unmarshal(headersSet, &r.HeadersToSet)
		}
		if r.HeadersToSet == nil {
			r.HeadersToSet = map[string]string{}
		}
		setPathOrPathPrefix(&r, pathPrefix)
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// pathPrefixToAPI sets r.Path or r.PathPrefix from stored path_prefix:
// if path_prefix ends with '/' it's a prefix (return as path_prefix), else exact (return as path).
func setPathOrPathPrefix(r *Route, pathPrefix string) {
	if pathPrefix == "" {
		return
	}
	if len(pathPrefix) > 0 && pathPrefix[len(pathPrefix)-1] == '/' {
		r.PathPrefix = pathPrefix
	} else {
		r.Path = pathPrefix
	}
}

// apiPathToPathPrefix returns the value to store in path_prefix from API path or path_prefix.
// For exact path we store as-is; for path_prefix we store as-is.
func apiPathToPathPrefix(path, pathPrefix string) string {
	if pathPrefix != "" {
		return pathPrefix
	}
	return path
}

// CreateRoute inserts a route. pathPrefixValue is the value to store in path_prefix column.
func (s *Store) CreateRoute(ctx context.Context, name, pathPrefixValue, upstream string, headersToForward []string, headersToSet map[string]string) (Route, error) {
	headersSetJSON, _ := json.Marshal(headersToSet)
	if headersToSet == nil {
		headersSetJSON = []byte("{}")
	}

	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO routes (name, path_prefix, upstream, headers_to_forward, headers_to_set)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		name, pathPrefixValue, upstream, headersToForward, headersSetJSON).Scan(&id)
	if err != nil {
		return Route{}, err
	}
	return s.GetRouteByID(ctx, id)
}

// GetRouteByID returns the route by id, or nil if not found.
func (s *Store) GetRouteByID(ctx context.Context, id string) (Route, error) {
	var r Route
	var pathPrefix string
	var headersSet []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, path_prefix, upstream, headers_to_forward, headers_to_set
		 FROM routes WHERE id = $1`,
		id).Scan(&r.ID, &r.Name, &pathPrefix, &r.Upstream, &r.HeadersToForward, &headersSet)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Route{}, ErrNotFound
		}
		return Route{}, err
	}
	if r.HeadersToForward == nil {
		r.HeadersToForward = []string{}
	}
	if len(headersSet) > 0 {
		_ = json.Unmarshal(headersSet, &r.HeadersToSet)
	}
	if r.HeadersToSet == nil {
		r.HeadersToSet = map[string]string{}
	}
	setPathOrPathPrefix(&r, pathPrefix)
	return r, nil
}

// UpdateRoute updates a route by id. pathPrefixValue is the value to store in path_prefix.
func (s *Store) UpdateRoute(ctx context.Context, id, name, pathPrefixValue, upstream string, headersToForward []string, headersToSet map[string]string) (Route, error) {
	headersSetJSON, _ := json.Marshal(headersToSet)
	if headersToSet == nil {
		headersSetJSON = []byte("{}")
	}
	cmd, err := s.pool.Exec(ctx,
		`UPDATE routes SET name = $1, path_prefix = $2, upstream = $3, headers_to_forward = $4, headers_to_set = $5, updated_at = now()
		 WHERE id = $6`,
		name, pathPrefixValue, upstream, headersToForward, headersSetJSON, id)
	if err != nil {
		return Route{}, err
	}
	if cmd.RowsAffected() == 0 {
		return Route{}, ErrNotFound
	}
	return s.GetRouteByID(ctx, id)
}

// DeleteRoute deletes a route by id. Returns ErrNotFound if not found.
func (s *Store) DeleteRoute(ctx context.Context, id string) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrNotFound is returned when a route or config row is not found.
var ErrNotFound = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }

// ErrConflict is returned when name or path overlaps.
var ErrConflict = errConflict{}

type errConflict struct{ msg string }

func (e errConflict) Error() string { return e.msg }

// NameExists returns true if a route with the given name exists (optionally excluding id).
func (s *Store) NameExists(ctx context.Context, name, excludeID string) (bool, error) {
	var exists bool
	if excludeID != "" {
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM routes WHERE name = $1 AND id != $2)`, name, excludeID).Scan(&exists)
		return exists, err
	}
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM routes WHERE name = $1)`, name).Scan(&exists)
	return exists, err
}

// PathPrefixOverlap returns true if any route has path_prefix that overlaps with the given value
// (equal or one is prefix of the other). excludeID is optional (empty = check all routes).
func (s *Store) PathPrefixOverlap(ctx context.Context, pathPrefixValue, excludeID string) (bool, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT path_prefix FROM routes WHERE $1 = '' OR id::text != $1`,
		excludeID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var existing string
		if err := rows.Scan(&existing); err != nil {
			return false, err
		}
		if pathPrefixValue == existing || hasPrefix(pathPrefixValue, existing) || hasPrefix(existing, pathPrefixValue) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// GetGlobalHeaderConfig returns the global header config (single row). Returns empty arrays/map if row missing.
func (s *Store) GetGlobalHeaderConfig(ctx context.Context) (GlobalHeaderConfig, error) {
	var out GlobalHeaderConfig
	out.HeadersToForward = []string{}
	out.HeadersToSet = map[string]string{}

	var headersForward []string
	var headersSet []byte
	err := s.pool.QueryRow(ctx,
		`SELECT headers_to_forward, headers_to_set FROM global_header_config WHERE id = 1`).
		Scan(&headersForward, &headersSet)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	if headersForward != nil {
		out.HeadersToForward = headersForward
	}
	if len(headersSet) > 0 {
		_ = json.Unmarshal(headersSet, &out.HeadersToSet)
	}
	if out.HeadersToSet == nil {
		out.HeadersToSet = map[string]string{}
	}
	return out, nil
}

// SetGlobalHeaderConfig upserts the global header config (single row id=1).
func (s *Store) SetGlobalHeaderConfig(ctx context.Context, headersToForward []string, headersToSet map[string]string) (GlobalHeaderConfig, error) {
	headersSetJSON, _ := json.Marshal(headersToSet)
	if headersToSet == nil {
		headersSetJSON = []byte("{}")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO global_header_config (id, headers_to_forward, headers_to_set) VALUES (1, $1, $2)
		 ON CONFLICT (id) DO UPDATE SET headers_to_forward = $1, headers_to_set = $2, updated_at = now()`,
		headersToForward, headersSetJSON)
	if err != nil {
		return GlobalHeaderConfig{}, err
	}
	return s.GetGlobalHeaderConfig(ctx)
}
