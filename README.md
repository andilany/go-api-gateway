# Go API Gateway

HTTP API gateway with a configuration API, reverse proxy, and request logging to PostgreSQL.

> This is a learning / demo project used to experiment with AI-assisted development workflows.

## Prerequisites

- Go 1.22+
- PostgreSQL (for persistence: routes, global header config, request logs)

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `8080` | HTTP listen port. |
| `DATABASE_URL` | Yes | — | PostgreSQL connection string (e.g. `postgres://user:pass@localhost:5432/gateway?sslmode=disable`). Loaded automatically from `.env` if present. |
| `CORS_ALLOWED_ORIGIN` | No | — | Single origin allowed for CORS (e.g. `https://app.example.com`). If empty, no CORS headers are set. **Never use `*` in production.** |

The gateway automatically loads environment variables from a `.env` file in the project root (using `godotenv`) if it exists. You can either:

- Put variables into `.env` (see `.env.example`), or
- Export them via your shell as usual.

## Running migrations

Apply migrations before starting the gateway:

```bash
# From project root
go run ./cmd/migrate up
```

Migrations use `DATABASE_URL`. To revert:

```bash
go run ./cmd/migrate down
```

Migration order: `000001` (routes), `000002` (request_logs), `000003` (global_header_config). See `docs/create/go-api-gateway/phase/migrations.md` for details.

## Running the gateway

From the project root, create a `.env` file or export required environment variables (see above), then run:

```bash
go run ./cmd/gateway
```

Example with inline env:

```bash
PORT=8080 DATABASE_URL="postgres://user:pass@localhost:5432/gateway?sslmode=disable" go run ./cmd/gateway
```

Optional: set `CORS_ALLOWED_ORIGIN` (e.g. `https://app.example.com`) to allow browser CORS; never use `*` in production.

To build a binary and run it:

```bash
go build -o gateway ./cmd/gateway
export PORT=8080
export DATABASE_URL="postgres://user:pass@localhost:5432/gateway?sslmode=disable"
./gateway
```

The server listens on `PORT`, shuts down on SIGINT/SIGTERM, and responds to:

- `GET /` — health check (`gateway ok`).
- **Config API** under `/api` (see below).
- **Proxy** for all other paths (matched against configured routes).

## API overview

### Config API (base path `/api`)

- **GET /api/routes** — List all routes.
- **POST /api/routes** — Create a route (body: `name`, `path` or `path_prefix`, `upstream`, optional `headers_to_forward`, `headers_to_set`).
- **GET /api/routes/:id** — Get a route by ID.
- **PUT /api/routes/:id** — Update a route (full replace).
- **DELETE /api/routes/:id** — Delete a route.
- **GET /api/config/headers** — Get global header config (`headers_to_forward`, `headers_to_set`).
- **PUT /api/config/headers** — Set global header config.

Request/response bodies are JSON. See `docs/create/go-api-gateway/design/api.md` for full contract (validation, status codes, conflict handling).

### Proxy behaviour

- Requests **not** under `/api` are matched against configured routes (longest path prefix wins).
- Matched requests are forwarded to the route’s upstream (method, full path, query, body) with a 30s timeout.
- Headers: only names in the effective forward list (global + route) are forwarded; global then route `headers_to_set` are applied (route overrides).
- Each proxied request is logged to `request_logs` (method, path, route name, status, duration, sanitized headers). Sensitive header values are redacted before storage.

See `docs/create/go-api-gateway/design/proxy.md` for path matching, header merge, and logging details.

## Rate limiting

- **Global:** 100 requests per hour per client IP (for proxy and health check).
- **Config API (`/api/*`):** 20 requests per hour per client IP.

When exceeded, the gateway responds with **429 Too Many Requests**. Client IP is taken from `X-Forwarded-For` (first hop) when present, otherwise `RemoteAddr`.

## Security notes

- No secrets in code; use `DATABASE_URL` and optional `CORS_ALLOWED_ORIGIN` from the environment.
- CORS: only the configured origin is allowed; wildcard `*` is never set.
- All database access uses parameterized queries; request logs store sanitized headers only (no raw `Authorization`, `Cookie`, or API keys).
- Auth for the config API is not implemented in this version; restrict network access or add auth (e.g. API key) for production.

## Tests

```bash
go test ./...
```

Config and migrate tests run without a database when `DATABASE_URL` is unset (they skip). API and proxy integration tests require a running Postgres and `DATABASE_URL` pointing at a test database.
