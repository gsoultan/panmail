SELECT m.user_id, m.tenant_id, t.name, m.role, m.created_at, m.updated_at
FROM user_tenants m
JOIN tenants t ON t.id = m.tenant_id
WHERE m.user_id = $1
ORDER BY t.name ASC;
