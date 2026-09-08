SELECT enabled,
       bind_scope,
       port,
       tls_certificate,
       tls_private_key,
       allow_insecure_auth,
       updated_at
FROM smtp_submission
WHERE id = 1
