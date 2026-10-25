-- 0201_group_db_access_group_fk.down.sql

ALTER TABLE group_db_access DROP CONSTRAINT IF EXISTS group_db_access_group_id_fkey;
