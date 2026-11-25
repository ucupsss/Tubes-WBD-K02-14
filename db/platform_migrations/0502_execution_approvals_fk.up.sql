-- 0502_execution_approvals_fk.up.sql (A, Milestone 2)
-- Menyusul 0501: execution_approvals.execution_id kini merujuk executions (0400 milik C).
-- ON DELETE CASCADE mengikuti execution_batches (0600): approval tidak bermakna tanpa execution-nya.

-- Baris yatim hanya mungkin muncul dari data uji sebelum FK ada; tanpa ini ADD CONSTRAINT gagal.
DELETE FROM execution_approvals a
WHERE NOT EXISTS (SELECT 1 FROM executions e WHERE e.id = a.execution_id);

ALTER TABLE execution_approvals
  ADD CONSTRAINT execution_approvals_execution_id_fkey
  FOREIGN KEY (execution_id) REFERENCES executions(id) ON DELETE CASCADE;
