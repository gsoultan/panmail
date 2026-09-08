INSERT INTO smtp_submission (id, enabled, bind_scope, port, tls_certificate, tls_private_key,
                             allow_insecure_auth, updated_at)
VALUES (1, $1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled,
                               bind_scope = EXCLUDED.bind_scope,
                               port = EXCLUDED.port,
                               tls_certificate = EXCLUDED.tls_certificate,
                               tls_private_key = EXCLUDED.tls_private_key,
                               allow_insecure_auth = EXCLUDED.allow_insecure_auth,
                               updated_at = EXCLUDED.updated_at
