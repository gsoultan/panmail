-- Receipts for posts to the HTTP inbound endpoint, so a provider that retries
-- the same post does not have it stored twice.
--
-- Every inbound-parse service retries a post it did not see a 2xx for, and a
-- slow answer reads to them exactly like a failure. Each retry used to be a new
-- message: stored again, screened again, its bounce recorded again, and the
-- tenant's MAIL_INBOUND webhook fired again.
--
-- The receipt lives here rather than beside the mail in the Pebble inbound
-- store because that store is per instance, and a provider's retry lands on
-- whichever instance the load balancer picks. The primary key is what makes two
-- concurrent posts of one message safe: exactly one INSERT wins.
--
-- dedupe_key is a SHA-256 of the provider's event id, or of the Message-ID and
-- the content, never the values themselves, so the table holds nothing a
-- reader could learn the mail from. Times are Unix seconds rather than a
-- timestamp type so the lease comparison means the same thing on every engine.
--
-- Rows are pruned on a fixed horizon (internal/retention), well past the
-- longest retry schedule of any provider panmail receives from.
CREATE TABLE IF NOT EXISTS inbound_receipts (
    tenant_id {{UUID}} NOT NULL,
    provider_id {{UUID}} NOT NULL,
    dedupe_key TEXT NOT NULL,

    -- 'PROCESSING' while a post holds the claim, 'DONE' once the message is
    -- committed. A PROCESSING row whose claim is older than the lease belongs
    -- to a post that died, and may be taken over.
    state TEXT NOT NULL,
    claim_token {{UUID}} NOT NULL,
    claimed_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL,

    PRIMARY KEY (tenant_id, provider_id, dedupe_key)
);

CREATE INDEX IF NOT EXISTS idx_inbound_receipts_created_at
    ON inbound_receipts (created_at);
