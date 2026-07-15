UPDATE templates SET name = $3, subject = $4, body_html = $5, body_text = $6, design = $7, updated_at = $8 WHERE tenant_id = $1 AND id = $2;
