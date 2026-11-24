-- 0400_executions.down.sql (C, Milestone 2)

DROP TABLE IF EXISTS database_locks;
DROP TABLE IF EXISTS migration_jobs;
DROP TABLE IF EXISTS execution_attempts;
DROP TABLE IF EXISTS executions;

ALTER TABLE migrations
  ADD COLUMN sql_text TEXT NOT NULL DEFAULT '',
  ADD COLUMN status TEXT NOT NULL DEFAULT 'draft';

UPDATE migrations m
SET sql_text = r.up_sql,
    status = CASE WHEN m.version IS NOT NULL THEN 'success' ELSE 'draft' END
FROM migration_revisions r
WHERE r.id = m.current_revision_id;

ALTER TABLE migrations
  DROP CONSTRAINT IF EXISTS migrations_current_revision_id_fkey,
  DROP COLUMN current_revision_id;

ALTER TABLE migrations
  ALTER COLUMN sql_text DROP DEFAULT,
  ALTER COLUMN status DROP DEFAULT;

DROP TABLE IF EXISTS migration_revisions;
