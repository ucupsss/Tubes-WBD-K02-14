-- 0300_create_migrations.up.sql (C)
-- Linear migration history per target database (D10) and each execution attempt.

CREATE TABLE migrations (
  id            BIGSERIAL PRIMARY KEY,
  database_id   BIGINT NOT NULL REFERENCES db_connections(id) ON DELETE RESTRICT,
  version       INT,
  title         TEXT NOT NULL,
  sql_text      TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'running', 'success', 'failed')),
  created_by    BIGINT NOT NULL REFERENCES users(id),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by    BIGINT REFERENCES users(id),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  executed_by   BIGINT REFERENCES users(id),
  executed_at   TIMESTAMPTZ,
  finished_at   TIMESTAMPTZ,
  duration_ms   INT,
  error_message TEXT,
  UNIQUE (database_id, version)
);

CREATE INDEX idx_migrations_database_id ON migrations (database_id);

CREATE TABLE migration_runs (
  id            BIGSERIAL PRIMARY KEY,
  migration_id  BIGINT NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
  executed_by   BIGINT NOT NULL REFERENCES users(id),
  sql_snapshot  TEXT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN ('success', 'failed')),
  error_message TEXT,
  started_at    TIMESTAMPTZ NOT NULL,
  finished_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_migration_runs_migration_id ON migration_runs (migration_id);
