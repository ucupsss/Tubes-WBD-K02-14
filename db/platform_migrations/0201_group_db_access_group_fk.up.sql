-- 0201_group_db_access_group_fk.up.sql
-- Grant ikut terhapus saat group dihapus. Grant yatim (group sudah tidak ada) dibersihkan dulu.

DELETE FROM group_db_access gda
WHERE NOT EXISTS (SELECT 1 FROM groups g WHERE g.id = gda.group_id);

ALTER TABLE group_db_access
  ADD CONSTRAINT group_db_access_group_id_fkey
  FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE;
