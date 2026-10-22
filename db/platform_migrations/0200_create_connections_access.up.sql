-- 0200_create_connections_access.up.sql
-- Pemilik: Orang B
-- Tabel db_connections untuk target database dan group_db_access untuk grant akses grup ke database.

CREATE TABLE IF NOT EXISTS db_connections (
  id           BIGSERIAL PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  host         TEXT NOT NULL,
  port         INT NOT NULL DEFAULT 5432,
  db_name      TEXT NOT NULL,
  username     TEXT NOT NULL,
  password_enc BYTEA NOT NULL,
  sslmode      TEXT NOT NULL DEFAULT 'disable',
  created_by   BIGINT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_db_access (
  group_id    BIGINT NOT NULL,
  database_id BIGINT NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  level       TEXT NOT NULL CHECK (level IN ('view','edit')),
  granted_by  BIGINT,
  granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, database_id)
);

CREATE INDEX IF NOT EXISTS idx_group_db_access_database_id ON group_db_access(database_id);
