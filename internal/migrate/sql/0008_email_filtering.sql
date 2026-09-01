-- Inbound and outbound filtering, and the queue of what it held.
--
-- Two tables because they answer two different questions. filter_rules is
-- configuration a tenant edits; filtered_messages is a log of decisions taken,
-- and a decision that has already been made must not change when someone later
-- edits or deletes the rule that made it.

CREATE TABLE IF NOT EXISTS filter_rules (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    name TEXT NOT NULL,

    -- inbound or outbound. A rule is written for one: "the sender is outside
    -- the company" means opposite things on the way in and the way out, so a
    -- rule that ran on both would be a trap rather than a convenience.
    direction TEXT NOT NULL,

    -- hold, reject, tag or allow.
    action TEXT NOT NULL,

    -- Lowest first, and the first match decides. An allow rule with a low
    -- priority is how a narrow exemption sits above a broad hold.
    priority INTEGER NOT NULL DEFAULT 100,

    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    -- The conditions as the engine understands them, stored whole rather than
    -- normalised into rows. A condition is only ever read and written as part
    -- of its rule, never queried across rules, so a table of condition rows
    -- would buy joins nobody performs and cost the ability to change the
    -- condition shape without a migration.
    conditions {{JSON}} NOT NULL,
    exceptions {{JSON}},

    -- The label recorded when action is tag.
    tag TEXT,

    created_at {{TIMESTAMP}} NOT NULL,
    updated_at {{TIMESTAMP}} NOT NULL
);

-- The read the send path performs on every message: this tenant's enabled
-- rules for one direction, in priority order. It is on the hot path, so it is
-- worth an index that answers it without a sort.
CREATE INDEX IF NOT EXISTS idx_filter_rules_evaluation
    ON filter_rules (tenant_id, direction, enabled, priority);

CREATE TABLE IF NOT EXISTS filtered_messages (
    id {{UUID}} PRIMARY KEY,
    tenant_id {{UUID}} NOT NULL,
    direction TEXT NOT NULL,

    -- The rule that decided, and its name copied at the time. The reference is
    -- nullable and the name is not: deleting a rule must not erase the reason a
    -- message was held, and "held by a rule that no longer exists" is still an
    -- answer a reviewer can act on.
    rule_id {{UUID}},
    rule_name TEXT NOT NULL,
    action TEXT NOT NULL,

    -- PENDING, RELEASED, REJECTED or EXPIRED.
    status TEXT NOT NULL DEFAULT 'PENDING',

    -- The envelope, which is small, bounded, and the only part a reviewer
    -- needs to triage. Subject is truncated on write rather than trusted.
    message_id {{UUID}},

    -- Carried so a released outbound message goes back out through the
    -- provider it was originally addressed to, and not whichever one happens
    -- to be default whenever a reviewer gets to it.
    provider_id {{UUID}},

    from_address TEXT NOT NULL,
    recipients {{JSON}} NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    attachment_count INTEGER NOT NULL DEFAULT 0,
    attachment_names {{JSON}},

    -- Which conditions fired. "Held by rule 7" is not reviewable; "held because
    -- the subject contained wire transfer and there was a .exe attached" is.
    matched {{JSON}} NOT NULL,

    -- Where the body and attachments live. Not inline: a held message carries
    -- whatever the sender attached, and a table that stores those bytes in the
    -- row grows without bound at exactly the rate someone is abusing the
    -- system. The row stays small and this points at the payload.
    payload_ref TEXT,

    reviewed_by TEXT,
    reviewed_at {{TIMESTAMP}},
    review_note TEXT,

    created_at {{TIMESTAMP}} NOT NULL,

    -- When this stops being worth keeping. Retention prunes on it, so a
    -- quarantine nobody reviews does not become a table nobody can query.
    expires_at {{TIMESTAMP}}
);

-- The review queue: one tenant's pending items, newest first.
CREATE INDEX IF NOT EXISTS idx_filtered_messages_queue
    ON filtered_messages (tenant_id, status, created_at);

-- Retention sweeps on this.
CREATE INDEX IF NOT EXISTS idx_filtered_messages_expiry
    ON filtered_messages (expires_at);
