SELECT id, tenant_id, email, password, name, role, two_factor_enabled, two_factor_secret, created_at, updated_at FROM users WHERE id = $1;
