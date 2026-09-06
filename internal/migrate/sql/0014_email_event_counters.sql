-- Lifetime event counts per tenant and type.
--
-- Two things are wrong without this, and both were measured rather than
-- guessed. Migration 0012 moved delivery events to the database and
-- GetMetrics became SELECT type, count(*) ... GROUP BY type, which:
--
--   1. Costs a full scan. At ten million events that is a parallel sequential
--      scan of every row and 965 ms, on every dashboard load. The Pebble store
--      it replaced kept incrementing counters and answered in O(1).
--
--   2. Counts the wrong thing. Pebble's counters are lifetime totals -- its
--      retention pass prunes event keys and never touches the metrics keys --
--      so "Emails Sent" is how many were ever sent. count(*) is how many rows
--      are *retained*, so the headline figure would visibly drop the first
--      time retention ran, which for a lifetime total is alarming and wrong.
--
-- Incremented per flush rather than per event: one upsert per (tenant, type)
-- in a batch of 500, not 500 upserts.
--
-- Deliberately never decremented. Retention prunes rows and leaves these
-- alone, exactly as Pebble did. A counter that fell when data aged out would
-- be a different statistic wearing the same label.

CREATE TABLE IF NOT EXISTS email_event_counters (
    tenant_id {{UUID}} NOT NULL,
    type TEXT NOT NULL,
    count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, type)
);
