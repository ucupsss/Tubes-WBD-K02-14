-- 0400_executions.up.sql (C, Milestone 2)
-- Identitas migration tetap di tabel migrations. SQL pindah ke revisi yang immutable setelah diajukan.
-- Satu permintaan jalan (apply / forward / rollback) adalah satu execution. Percobaan teknis dicatat
-- di execution_attempts. Antrian dan lock dibuat di sini supaya nomor migrasi tidak mundur; worker
-- mengisinya pada paket berikutnya.
--
-- Data Milestone 1 dipindahkan: sukses menjadi revisi irreversible + execution apply sukses;
-- migration_runs gagal menjadi execution failed. Draft tetap bisa diedit (frozen_at NULL).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE migrations
  ADD COLUMN current_revision_id BIGINT;

CREATE TABLE migration_revisions (
  id                  BIGSERIAL PRIMARY KEY,
  migration_id        BIGINT NOT NULL REFERENCES migrations(id) ON DELETE CASCADE,
  revision_no         INT NOT NULL,
  up_sql              TEXT NOT NULL,
  down_sql            TEXT,
  irreversible        BOOLEAN NOT NULL DEFAULT false,
  irreversible_reason TEXT,
  exec_mode           TEXT NOT NULL DEFAULT 'normal' CHECK (exec_mode IN ('normal', 'batch')),
  batch_table         TEXT,
  batch_column        TEXT,
  batch_size          INT,
  sql_hash            TEXT NOT NULL,
  frozen_at           TIMESTAMPTZ,
  created_by          BIGINT NOT NULL REFERENCES users(id),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (migration_id, revision_no),
  CHECK (irreversible = false OR irreversible_reason IS NOT NULL),
  CHECK (irreversible = true OR down_sql IS NOT NULL)
);

-- Hash: sha256(up || 0x00 || down || mode). Mode batch menambah tabel, kolom, dan ukuran.
INSERT INTO migration_revisions (
  migration_id, revision_no, up_sql, down_sql, irreversible, irreversible_reason,
  exec_mode, sql_hash, frozen_at, created_by, created_at
)
SELECT
  m.id,
  1,
  m.sql_text,
  CASE WHEN m.status = 'draft' THEN '-- belum diisi' ELSE NULL END,
  m.status <> 'draft',
  CASE WHEN m.status = 'draft' THEN NULL ELSE 'Berasal dari Milestone 1: tidak ada SQL down' END,
  'normal',
  encode(digest(
    convert_to(m.sql_text, 'UTF8')
      || '\x00'::bytea
      || convert_to(CASE WHEN m.status = 'draft' THEN '-- belum diisi' ELSE '' END, 'UTF8')
      || convert_to('normal', 'UTF8'),
    'sha256'), 'hex'),
  CASE WHEN m.status = 'draft' THEN NULL ELSE now() END,
  m.created_by,
  m.created_at
FROM migrations m;

UPDATE migrations m
SET current_revision_id = r.id
FROM migration_revisions r
WHERE r.migration_id = m.id AND r.revision_no = 1;

ALTER TABLE migrations
  ADD CONSTRAINT migrations_current_revision_id_fkey
  FOREIGN KEY (current_revision_id) REFERENCES migration_revisions(id);

CREATE TABLE executions (
  id                      BIGSERIAL PRIMARY KEY,
  database_id             BIGINT NOT NULL REFERENCES db_connections(id),
  migration_id            BIGINT NOT NULL REFERENCES migrations(id),
  revision_id             BIGINT NOT NULL REFERENCES migration_revisions(id),
  kind                    TEXT NOT NULL CHECK (kind IN ('apply', 'forward', 'rollback')),
  status                  TEXT NOT NULL CHECK (status IN (
                            'pending_review', 'approved', 'queued', 'running', 'success', 'failed',
                            'partial', 'partially_reverted', 'uncertain', 'rejected', 'cancelled')),
  requested_by            BIGINT NOT NULL REFERENCES users(id),
  rolls_back_execution_id BIGINT REFERENCES executions(id),
  approval_round          INT NOT NULL DEFAULT 1,
  version_applied         INT,
  batches_committed       INT NOT NULL DEFAULT 0,
  rows_committed          BIGINT NOT NULL DEFAULT 0,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at              TIMESTAMPTZ,
  finished_at             TIMESTAMPTZ,
  error_code              TEXT,
  error_message           TEXT
);

CREATE INDEX idx_exec_db ON executions (database_id, id);
CREATE INDEX idx_exec_migration ON executions (migration_id, id);

CREATE TABLE execution_attempts (
  id            BIGSERIAL PRIMARY KEY,
  execution_id  BIGINT NOT NULL REFERENCES executions(id),
  attempt_no    INT NOT NULL,
  worker_id     TEXT,
  status        TEXT NOT NULL CHECK (status IN ('running', 'success', 'failed', 'uncertain')),
  sql_executed  TEXT NOT NULL,
  sqlstate      TEXT,
  error_message TEXT,
  started_at    TIMESTAMPTZ NOT NULL,
  finished_at   TIMESTAMPTZ,
  trace_id      TEXT,
  UNIQUE (execution_id, attempt_no)
);

-- Sukses Milestone 1: satu execution apply yang sudah selesai, posisi rantai = version lama.
WITH inserted AS (
  INSERT INTO executions (
    database_id, migration_id, revision_id, kind, status, requested_by,
    version_applied, created_at, started_at, finished_at, error_message
  )
  SELECT
    m.database_id, m.id, r.id, 'apply', 'success', COALESCE(m.executed_by, m.created_by),
    m.version, COALESCE(m.executed_at, m.created_at), m.executed_at, m.finished_at, NULL
  FROM migrations m
  JOIN migration_revisions r ON r.id = m.current_revision_id
  WHERE m.status = 'success'
  RETURNING id, revision_id, status, started_at, finished_at, created_at, error_message
)
INSERT INTO execution_attempts (execution_id, attempt_no, status, sql_executed, error_message, started_at, finished_at)
SELECT i.id, 1, 'success', r.up_sql, NULL, COALESCE(i.started_at, i.created_at), i.finished_at
FROM inserted i
JOIN migration_revisions r ON r.id = i.revision_id;

-- Tiap percobaan gagal Milestone 1 tetap tercatat.
WITH inserted AS (
  INSERT INTO executions (
    database_id, migration_id, revision_id, kind, status, requested_by,
    created_at, started_at, finished_at, error_message
  )
  SELECT
    m.database_id, m.id, r.id, 'apply', 'failed', mr.executed_by,
    mr.started_at, mr.started_at, mr.finished_at, mr.error_message
  FROM migration_runs mr
  JOIN migrations m ON m.id = mr.migration_id
  JOIN migration_revisions r ON r.migration_id = m.id AND r.revision_no = 1
  WHERE mr.status = 'failed'
  RETURNING id, revision_id, started_at, finished_at, created_at, error_message
)
INSERT INTO execution_attempts (execution_id, attempt_no, status, sql_executed, error_message, started_at, finished_at)
SELECT i.id, 1, 'failed', r.up_sql, i.error_message, COALESCE(i.started_at, i.created_at), i.finished_at
FROM inserted i
JOIN migration_revisions r ON r.id = i.revision_id;

-- Gagal atau terputus tanpa baris migration_runs.
WITH inserted AS (
  INSERT INTO executions (
    database_id, migration_id, revision_id, kind, status, requested_by,
    created_at, started_at, finished_at, error_message
  )
  SELECT
    m.database_id, m.id, r.id, 'apply', 'failed', COALESCE(m.executed_by, m.created_by),
    COALESCE(m.executed_at, m.created_at), m.executed_at, m.finished_at,
    COALESCE(m.error_message, 'Migrasi Milestone 1 tidak selesai')
  FROM migrations m
  JOIN migration_revisions r ON r.id = m.current_revision_id
  WHERE m.status IN ('failed', 'running')
    AND NOT EXISTS (SELECT 1 FROM migration_runs mr WHERE mr.migration_id = m.id AND mr.status = 'failed')
  RETURNING id, revision_id, started_at, finished_at, created_at, error_message
)
INSERT INTO execution_attempts (execution_id, attempt_no, status, sql_executed, error_message, started_at, finished_at)
SELECT i.id, 1, 'failed', r.up_sql, i.error_message, COALESCE(i.started_at, i.created_at), i.finished_at
FROM inserted i
JOIN migration_revisions r ON r.id = i.revision_id;

ALTER TABLE migrations
  DROP COLUMN sql_text,
  DROP COLUMN status;

CREATE TABLE migration_jobs (
  id            BIGSERIAL PRIMARY KEY,
  execution_id  BIGINT NOT NULL UNIQUE REFERENCES executions(id),
  database_id   BIGINT NOT NULL,
  status        TEXT NOT NULL CHECK (status IN ('queued', 'running', 'done', 'dead')),
  run_after     TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempts      INT NOT NULL DEFAULT 0,
  claimed_by    TEXT,
  claimed_at    TIMESTAMPTZ,
  trace_context JSONB NOT NULL DEFAULT '{}',
  enqueued_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE database_locks (
  database_id      BIGINT PRIMARY KEY REFERENCES db_connections(id),
  execution_id     BIGINT NOT NULL REFERENCES executions(id),
  owner_worker     TEXT NOT NULL,
  lease_expires_at TIMESTAMPTZ NOT NULL,
  acquired_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
