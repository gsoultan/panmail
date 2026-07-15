SELECT id, tenant_id, name, subject, body_html, body_text, design, created_at, updated_at FROM templates WHERE tenant_id = $1 ORDER BY created_at DESC;
