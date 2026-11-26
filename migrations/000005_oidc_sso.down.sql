ALTER TABLE user_groups ADD CONSTRAINT user_groups_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id);
ALTER TABLE user_groups DROP COLUMN IF EXISTS auto;
ALTER TABLE users DROP COLUMN IF EXISTS auth_source;
