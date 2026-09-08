-- Members of one tenant, which since 0017 is a membership question rather than
-- a users.tenant_id question: a guest assigned to this tenant belongs in the
-- list even though their home tenant is elsewhere.
--
-- The role reported is the role held HERE, not the user's home role, so an
-- administrator visiting as a viewer is listed as a viewer. Super admin is the
-- exception because it is global and outranks any membership.
SELECT u.id,
       u.tenant_id,
       u.email,
       u.password,
       u.name,
       CASE WHEN u.role = 'USER_ROLE_SUPER_ADMIN' THEN u.role ELSE m.role END AS role,
       u.two_factor_enabled,
       u.two_factor_secret,
       u.created_at,
       u.updated_at
FROM users u
JOIN user_tenants m ON m.user_id = u.id
WHERE m.tenant_id = $1
ORDER BY u.created_at DESC;
