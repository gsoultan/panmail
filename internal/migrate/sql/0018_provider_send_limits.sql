-- A ceiling on how fast a single provider may be sent through.
--
-- The tenant ceiling added in 0005 protects the shared sending reputation from
-- one tenant. It cannot protect a provider from a tenant spreading the same
-- volume across several of them, and it does not know that providers differ:
-- an ESP account on a trial plan, a relay on a small VPS and a warmed-up
-- dedicated IP tolerate wildly different rates, and the one that breaks first
-- decides what the tenant's mail looks like.
--
-- Charged together with the tenant's, as one decision: see AllowAll in
-- internal/ratelimit. Charging them in sequence would spend a tenant token on
-- a send the provider then refused.
--
-- Zero means unlimited, and zero is the default, so switching this on must not
-- start refusing mail for providers already configured.
ALTER TABLE email_providers ADD COLUMN send_rate_per_minute INTEGER NOT NULL DEFAULT 0;

-- How much may go at once before the rate binds. Zero falls back to one
-- minute's worth, matching the tenant bucket.
ALTER TABLE email_providers ADD COLUMN send_burst INTEGER NOT NULL DEFAULT 0;
