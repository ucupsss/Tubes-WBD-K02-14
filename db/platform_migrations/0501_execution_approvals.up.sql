-- 0501_execution_approvals.up.sql (A, Milestone 2)
-- Approval per (execution, ronde, group). Satu baris per group yang punya akses (view/edit) ke database
-- target saat ronde dibuka. leader_user_id = leader yang memutuskan (NULL selama group belum punya leader).
--
-- execution_id BELUM memakai FOREIGN KEY karena tabel executions milik C (0400) belum ada saat
-- file ini dibuat. Setelah 0400 masuk main, tambahkan FK lewat migrasi 0502 (lihat panduan bagian 4.2).

CREATE TABLE execution_approvals (
  execution_id   BIGINT NOT NULL,
  round          INT    NOT NULL DEFAULT 1,
  group_id       BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  leader_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  decision       TEXT   NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending', 'approved', 'rejected')),
  comment        TEXT,
  decided_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
  decided_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (execution_id, round, group_id)
);

CREATE INDEX idx_execution_approvals_pending
  ON execution_approvals (group_id) WHERE decision = 'pending';
