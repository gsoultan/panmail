-- Every table whose foreign key to tenants would refuse the delete. A user's
-- home membership is counted with the user, so user_tenants counts only the
-- guests lent to this tenant from elsewhere.
SELECT
    (SELECT COUNT(*) FROM users WHERE tenant_id = $1),
    (SELECT COUNT(*) FROM user_tenants ut
      WHERE ut.tenant_id = $1
        AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = ut.user_id AND u.tenant_id = $1)),
    (SELECT COUNT(*) FROM email_providers WHERE tenant_id = $1),
    (SELECT COUNT(*) FROM api_keys WHERE tenant_id = $1),
    (SELECT COUNT(*) FROM templates WHERE tenant_id = $1),
    (SELECT COUNT(*) FROM suppressions WHERE tenant_id = $1),
    (SELECT COUNT(*) FROM webhooks WHERE tenant_id = $1),
    (SELECT COUNT(*) FROM outbox WHERE tenant_id = $1);
