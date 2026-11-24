-- 0600_execution_batches.up.sql (B, Milestone 2)
-- Cermin checkpoint batch di database platform untuk tampilan UI & progres.
-- Sumber kebenaran tetap ada di _dbmp.batch_checkpoints pada database target (D37).

CREATE TABLE execution_batches (
  execution_id  BIGINT      NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
  direction     TEXT        NOT NULL CHECK (direction IN ('apply', 'revert')),
  batch_no      INT         NOT NULL,
  from_value    BIGINT      NOT NULL,
  to_value      BIGINT      NOT NULL,
  rows_affected BIGINT      NOT NULL,
  committed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (execution_id, direction, batch_no)
);

CREATE INDEX idx_execution_batches_exec ON execution_batches (execution_id, direction, batch_no);
