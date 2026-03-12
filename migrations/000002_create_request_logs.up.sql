-- request_logs: append-only log of each proxied request (headers must be sanitized by application)
CREATE TABLE IF NOT EXISTS request_logs (
    id                 UUID        NOT NULL DEFAULT gen_random_uuid(),
    route_name         TEXT        NOT NULL,
    method             TEXT        NOT NULL,
    path               TEXT        NOT NULL,
    status_code        INT         NULL,
    duration_ms        BIGINT      NULL,
    request_headers    JSONB       NULL,
    response_headers   JSONB       NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    CONSTRAINT request_logs_status_code_check CHECK (status_code IS NULL OR (status_code >= 100 AND status_code < 600)),
    CONSTRAINT request_logs_duration_ms_check CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE INDEX IF NOT EXISTS request_logs_created_at_idx ON request_logs (created_at);
CREATE INDEX IF NOT EXISTS request_logs_route_name_idx ON request_logs (route_name);
