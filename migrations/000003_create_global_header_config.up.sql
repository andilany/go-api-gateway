-- global_header_config: single-row config for default headers (forward list, add/override map)
CREATE TABLE IF NOT EXISTS global_header_config (
    id                  INT          NOT NULL PRIMARY KEY DEFAULT 1,
    headers_to_forward  TEXT[]       NOT NULL DEFAULT '{}',
    headers_to_set      JSONB        NOT NULL DEFAULT '{}',
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT global_header_config_single_row CHECK (id = 1)
);

-- Insert default row so GET after migration returns empty arrays/map
INSERT INTO global_header_config (id, headers_to_forward, headers_to_set)
VALUES (1, '{}', '{}')
ON CONFLICT (id) DO NOTHING;
