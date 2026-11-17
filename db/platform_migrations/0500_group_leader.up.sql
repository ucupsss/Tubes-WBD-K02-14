-- 0500_group_leader.up.sql (A, Milestone 2)
-- Setiap group boleh punya satu leader (anggota group). Leader menyetujui perubahan database
-- atas nama group-nya (D27, D28). NULL = belum ditetapkan; pengajuan tetap pending sampai ada leader.

ALTER TABLE groups
  ADD COLUMN leader_id BIGINT REFERENCES users(id) ON DELETE SET NULL;
