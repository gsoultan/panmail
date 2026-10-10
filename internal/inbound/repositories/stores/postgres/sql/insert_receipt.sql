-- The claim itself. The primary key decides between concurrent posts of one
-- message: exactly one INSERT lands, and every other sees no row affected.
-- The conflict target is left bare so the statement is portable across both
-- engines this store runs on.
INSERT INTO inbound_receipts (tenant_id, provider_id, dedupe_key, state, claim_token, claimed_at, created_at)
VALUES ($1, $2, $3, 'PROCESSING', $4, $5, $5)
ON CONFLICT DO NOTHING;
