-- API keys carry an explicit capability set instead of inheriting a user role.
-- Keys predating this column decode to the least-privilege default.
ALTER TABLE api_keys ADD COLUMN scopes TEXT;
