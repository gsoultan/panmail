-- A ceiling on how fast a tenant may send.
--
-- Nothing capped this before, so a runaway loop or a leaked API key could send
-- without bound. Because tenants share a provider and a sending IP, the damage
-- is not confined to the tenant that caused it: a spike gets the shared domain
-- rate-limited or blocklisted, and every other tenant's mail degrades with it.
--
-- Zero means unlimited, and zero is the default. Switching this on must not
-- start refusing mail for tenants already running.
ALTER TABLE tenants ADD COLUMN send_rate_per_minute INTEGER NOT NULL DEFAULT 0;

-- How much may go at once before the rate binds. Sending is naturally bursty —
-- a campaign is queued in one go — so a bucket holding only one minute's worth
-- would reject the ordinary case. Zero falls back to one minute's worth.
ALTER TABLE tenants ADD COLUMN send_burst INTEGER NOT NULL DEFAULT 0;
