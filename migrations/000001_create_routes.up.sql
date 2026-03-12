-- routes: gateway route definitions (name, path prefix, upstream, inline header config)
CREATE TABLE IF NOT EXISTS routes (
    id                UUID         NOT NULL DEFAULT gen_random_uuid(),
    name              TEXT         NOT NULL,
    path_prefix       TEXT         NOT NULL,
    upstream          TEXT         NOT NULL,
    headers_to_forward TEXT[]      DEFAULT '{}',
    headers_to_set    JSONB        DEFAULT '{}',
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (name)
);
