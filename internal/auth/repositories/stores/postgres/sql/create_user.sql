INSERT INTO users (id, tenant_id, email, password, name, role, two_factor_enabled, two_factor_secret, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);
